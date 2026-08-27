package clientserver

import (
	"github.com/dotside-studios/davi-nfc-agent/deviceid"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/multimanager"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/gorilla/websocket"
)

// stack is what the agent composes: the driver's device endpoint behind the
// credential check, and the router draining the bridge behind both. The tests
// build it the same way so they exercise the composition, not a stand-in.
type stack struct {
	URL     string
	Router  *tagOps
	Client  *fakeClient
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

// deviceGate is what agent.ServerPlugin.Authenticate does, over a static
// config rather than a running agent.
func deviceGate(cfg stackConfig) func(w http.ResponseWriter, r *http.Request) (string, bool) {
	return func(w http.ResponseWriter, r *http.Request) (string, bool) {
		if cfg.RequirePaired {
			return server.CheckPairedDevice(w, r, cfg.TokenVerifier)
		}
		return server.CheckAuth(w, r, cfg.APISecret, cfg.TokenVerifier)
	}
}

// admit is the credential check standing in front of a device endpoint, the
// way the paired-device manager mounts one: it names the device it admitted, and
// the driver registers under that identity.
func admit(check func(w http.ResponseWriter, r *http.Request) (string, bool), next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, ok := check(w, r)
		if !ok {
			return
		}
		next.ServeHTTP(w, deviceid.With(r, id))
	})
}

func newStack(t *testing.T, cfg stackConfig) *stack {
	t.Helper()

	auth := deviceGate(cfg)
	client := newFakeClient()

	var remote *remotenfc.Manager
	endpoint := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no device driver configured", http.StatusServiceUnavailable)
	}))
	if !cfg.NoDriver {
		remote = remotenfc.NewManager(30 * time.Second)
		endpoint = admit(auth, remote.Handler(remotenfc.ServerOptions{
			AllowTagModification: func() bool { return cfg.Mode != nfc.ModeReadOnly },
			PublicKeyPin:         func() string { return cfg.PublicKeyPin },
		}))
	}

	// One supervisor over every manager, which is how the agent composes them:
	// readers are polled, and the tags a device holds are answered for by the
	// driver behind the same supervisor.
	readers := supervisorOver(t, cfg.Hardware, remote, cfg.Mode)

	router := newTagOps(Config{
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

// A device the tests can act as. The protocol itself is the driver's to test;
// what matters here is a registered device holding a tag, which is what an
// operation routes to.

// registerCapableV1 registers a device declaring it can do everything, so a
// refusal in a test is the agent's policy rather than the device's limits.
func registerCapableV1(t *testing.T, url string) (*websocket.Conn, string) {
	t.Helper()
	return registerDevice(t, url, "Capable Device", map[string]any{
		"canRead":       true,
		"canWrite":      true,
		"canTransceive": true,
		"canLock":       true,
		"nfcType":       "nfca",
	})
}

// registerV1 registers a device that declares no capabilities, which is what
// the agent has to assume the least about.
func registerV1(t *testing.T, url string) (*websocket.Conn, string) {
	t.Helper()
	return registerDevice(t, url, "Test Device", nil)
}

func registerDevice(t *testing.T, url, name string, capabilities map[string]any) (*websocket.Conn, string) {
	t.Helper()

	conn, _, err := (&websocket.Dialer{
		HandshakeTimeout: 3 * time.Second,
		Subprotocols:     []string{remotenfc.SubprotocolDeviceV1},
	}).Dial(url, nil)
	if err != nil {
		t.Fatalf("dial device: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	hello := map[string]any{
		"protocolVersion": remotenfc.DeviceProtocolV1,
		"deviceName":      name,
		"platform":        "android",
	}
	if capabilities != nil {
		hello["capabilities"] = capabilities
	}

	if err := conn.WriteJSON(protocol.WebSocketRequest{Type: remotenfc.WSTypeHello, Payload: hello}); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	_, payload := readDeviceResponse(t, conn)
	deviceID, _ := payload["deviceID"].(string)
	if deviceID == "" {
		t.Fatal("registration returned no deviceID")
	}
	return conn, deviceID
}

// readDeviceResponse reads one response from a device connection.
func readDeviceResponse(t *testing.T, conn *websocket.Conn) (protocol.WebSocketResponse, map[string]any) {
	t.Helper()

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var out protocol.WebSocketResponse
	if err := conn.ReadJSON(&out); err != nil {
		t.Fatalf("read response: %v", err)
	}

	payload, ok := out.Payload.(map[string]any)
	if !ok {
		t.Fatalf("unexpected payload shape: %#v", out.Payload)
	}
	return out, payload
}
