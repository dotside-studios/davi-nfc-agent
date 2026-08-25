package tagrouter_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/multimanager"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/tagrouter"
)

// stack is what the agent composes: the driver's device endpoint behind the
// credential check, and the router draining the bridge behind both. The tests
// build it the same way so they exercise the composition, not a stand-in.
type stack struct {
	URL     string
	Router  *tagrouter.Router
	Client  *fakeClient
	Auth    *server.DeviceAuth
	Remote  *remotenfc.Manager
	Readers *nfc.Supervisor
}

// fakeClient stands in for the client server: it receives the scans the driver
// produces, which is what the agent wires up in the shipped binary.
type fakeClient struct {
	mu   sync.Mutex
	tags []nfc.NFCData
	ch   chan nfc.NFCData
}

func newFakeClient() *fakeClient {
	return &fakeClient{ch: make(chan nfc.NFCData, 16)}
}

func (c *fakeClient) Broadcast(data nfc.NFCData) {
	c.mu.Lock()
	c.tags = append(c.tags, data)
	c.mu.Unlock()
	select {
	case c.ch <- data:
	default:
	}
}

func (c *fakeClient) BroadcastDeviceStatus(nfc.DeviceStatus) {}

// await waits for the next scan to arrive.
func (c *fakeClient) await(t *testing.T) nfc.NFCData {
	t.Helper()
	select {
	case data := <-c.ch:
		return data
	case <-time.After(3 * time.Second):
		t.Fatal("no scan reached the client")
		return nfc.NFCData{}
	}
}

type stackConfig struct {
	// Hardware is a manager whose devices are opened as readers. Nil leaves the
	// agent with none, which is a build that only serves paired devices.
	Hardware nfc.Manager

	// Mode is what the readers are allowed to do. The zero value is read/write.
	Mode nfc.ReaderMode

	APISecret     string
	TokenVerifier server.TokenVerifier
	RequirePaired bool
	PublicKeyPin  string

	// NoDriver omits the device driver, leaving nothing to serve a device.
	NoDriver bool
}

func newStack(t *testing.T, cfg stackConfig) *stack {
	t.Helper()

	auth := server.NewDeviceAuth(cfg.APISecret, cfg.TokenVerifier, cfg.RequirePaired)
	client := newFakeClient()

	var remote *remotenfc.Manager
	endpoint := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no device driver configured", http.StatusServiceUnavailable)
	}))
	if !cfg.NoDriver {
		remote = remotenfc.NewManager(30 * time.Second)
		endpoint = remote.Handler(remotenfc.ServerOptions{
			Authenticate:         auth.Check,
			AllowTagModification: func() bool { return cfg.Mode != nfc.ModeReadOnly },
			PublicKeyPin:         func() string { return cfg.PublicKeyPin },
		})
	}

	// One supervisor over every manager, which is how the agent composes them:
	// readers are polled, and the tags a device holds are answered for by the
	// driver behind the same supervisor.
	readers := supervisorOver(t, cfg.Hardware, remote, cfg.Mode)

	router := tagrouter.New(tagrouter.Config{
		Tags:                 readers,
		AllowTagModification: func() bool { return readers.Mode() != nfc.ModeReadOnly },
	})

	if remote != nil {
		remote.Scans().Connect(func(scan nfc.ScannedTag) {
			// What the supervisor does with a raw scan, which is what
			// stands between the driver and the clients in a real agent.
			data := nfc.NFCData{Device: scan.Device, Err: scan.Err}
			if scan.Tag != nil {
				data.Card = nfc.NewCard(scan.Tag)
			}
			client.Broadcast(data)
		})
	}

	ts := httptest.NewServer(endpoint)

	t.Cleanup(func() {
		ts.Close()
		if remote != nil {
			remote.Close()
		}
		readers.Stop()
	})

	return &stack{
		URL:     "ws" + strings.TrimPrefix(ts.URL, "http") + "?mode=device",
		Router:  router,
		Client:  client,
		Auth:    auth,
		Remote:  remote,
		Readers: readers,
	}
}

// supervisorOver builds the supervisor the stack routes through. A manager with
// no readers to open still answers for the tags its devices hold.
func supervisorOver(t *testing.T, hardware nfc.Manager, remote *remotenfc.Manager, mode nfc.ReaderMode) *nfc.Supervisor {
	t.Helper()

	var entries []multimanager.ManagerEntry
	if hardware != nil {
		entries = append(entries, multimanager.ManagerEntry{Name: nfc.ManagerTypeHardware, Manager: hardware})
	}
	if remote != nil {
		entries = append(entries, multimanager.ManagerEntry{Name: nfc.ManagerTypeSmartphone, Manager: remote})
	}

	readers, err := nfc.NewSupervisor(multimanager.NewMultiManager(entries...), time.Second)
	if err != nil {
		t.Fatalf("NewSupervisor: %v", err)
	}
	if err := readers.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	readers.SetMode(mode)
	return readers
}
