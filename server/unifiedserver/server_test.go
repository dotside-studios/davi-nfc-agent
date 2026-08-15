package unifiedserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/clientserver"
	"github.com/dotside-studios/davi-nfc-agent/server/deviceserver"
	"github.com/dotside-studios/davi-nfc-agent/server/unifiedserver"
	"github.com/gorilla/websocket"
)

// newTestServer wires a unified server with its background workers running and
// exposes it over an httptest listener. It returns the ws:// base URL.
func newTestServer(t *testing.T) string {
	t.Helper()

	bridge := server.NewServerBridge()
	deviceMgr := remotenfc.NewManager(30 * time.Second)

	device := deviceserver.New(deviceserver.Config{
		DeviceManager: deviceMgr,
	}, bridge)
	client := clientserver.New(clientserver.Config{}, bridge)

	u := unifiedserver.New(unifiedserver.Config{}, device, client)

	ctx, cancel := context.WithCancel(context.Background())
	device.StartBackground(ctx)
	client.StartBackground(ctx)

	ts := httptest.NewServer(u.Handler())

	t.Cleanup(func() {
		ts.Close()
		cancel()
		bridge.Close()
		deviceMgr.Close()
	})

	return "ws" + strings.TrimPrefix(ts.URL, "http")
}

// dialAndProbe connects to the given ws URL, sends a bogus-typed message, and
// returns the "code" field from the error response the handler sends back. The
// device and client handlers use distinct codes, which lets us confirm each
// connection was routed to the correct handler on the single port.
func dialAndProbe(t *testing.T, wsURL string) string {
	t.Helper()

	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial %s failed: %v (status %d)", wsURL, err, status)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.WriteJSON(protocol.WebSocketRequest{Type: "bogus"}); err != nil {
		t.Fatalf("write to %s failed: %v", wsURL, err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var out protocol.WebSocketResponse
	if err := conn.ReadJSON(&out); err != nil {
		t.Fatalf("read from %s failed: %v", wsURL, err)
	}

	payload, ok := out.Payload.(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected payload shape from %s: %#v", wsURL, out.Payload)
	}
	code, _ := payload["code"].(string)
	return code
}

// TestWSDispatch verifies that a single /ws endpoint routes device connections
// (?mode=device) to the device handler and everything else to the client
// handler, using the handler-specific error codes as the signal.
func TestWSDispatch(t *testing.T) {
	base := newTestServer(t)

	// Client connection: unknown message type -> client handler's UNKNOWN_TYPE.
	if code := dialAndProbe(t, base+"/ws"); code != "UNKNOWN_TYPE" {
		t.Errorf("client /ws routed to wrong handler: got code %q, want UNKNOWN_TYPE", code)
	}

	// Device connection: first message must be registerDevice, so a bogus type
	// yields the device handler's INVALID_MESSAGE_TYPE.
	if code := dialAndProbe(t, base+"/ws?mode=device"); code != "INVALID_MESSAGE_TYPE" {
		t.Errorf("device /ws?mode=device routed to wrong handler: got code %q, want INVALID_MESSAGE_TYPE", code)
	}
}

// TestHealthEndpoints verifies both health endpoints are served on the single
// port and report the unified agent type.
func TestHealthEndpoints(t *testing.T) {
	base := newTestServer(t)
	httpBase := "http" + strings.TrimPrefix(base, "ws")

	for _, path := range []string{"/health", "/api/v1/health"} {
		resp, err := http.Get(httpBase + path)
		if err != nil {
			t.Fatalf("GET %s failed: %v", path, err)
		}
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s status = %d, want 200", path, resp.StatusCode)
		}
		var body map[string]interface{}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatalf("decode %s body failed: %v", path, err)
		}
		_ = resp.Body.Close()
		if body["type"] != "agent" {
			t.Errorf("GET %s type = %v, want agent", path, body["type"])
		}
	}
}
