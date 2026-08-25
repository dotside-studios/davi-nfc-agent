package tagrouter_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/tagrouter"
)

// stack is what the agent composes: the driver's device endpoint behind the
// credential check, and the router draining the bridge behind both. The tests
// build it the same way so they exercise the composition, not a stand-in.
type stack struct {
	URL    string
	Router *tagrouter.Router
	Client *fakeClient
	Auth   *server.DeviceAuth
	Remote *remotenfc.Manager
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
	Readers       *nfc.Supervisor
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
			AllowTagModification: tagModificationPolicy(cfg.Readers),
			PublicKeyPin:         func() string { return cfg.PublicKeyPin },
		})
	}

	router := tagrouter.New(tagrouter.Config{Readers: cfg.Readers, Devices: remote})

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
		if cfg.Readers != nil {
			cfg.Readers.Stop()
		}
	})

	return &stack{
		URL:    "ws" + strings.TrimPrefix(ts.URL, "http") + "?mode=device",
		Router: router,
		Client: client,
		Auth:   auth,
		Remote: remote,
	}
}

// tagModificationPolicy captures the readers' mode as a predicate, so the
// driver can refuse a modifying operation the agent's mode forbids.
func tagModificationPolicy(readers *nfc.Supervisor) func() bool {
	if readers == nil {
		return nil
	}
	return func() bool { return readers.Mode() != nfc.ModeReadOnly }
}
