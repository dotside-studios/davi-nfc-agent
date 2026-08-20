package unifiedserver_test

import (
	"context"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/clientserver"
	"github.com/dotside-studios/davi-nfc-agent/server/unifiedserver"
	"github.com/gorilla/websocket"
)

// newTestServer wires a unified server with its background workers running and
// exposes it over an httptest listener. It returns the ws:// base URL.
func newTestServer(t *testing.T) string {
	t.Helper()

	deviceMgr := remotenfc.NewManager(30 * time.Second)

	// The device endpoint is the driver's handler behind the credential check,
	// which is how the agent mounts it.
	auth := server.NewDeviceAuth("", nil, false)
	device := deviceMgr.Handler(remotenfc.ServerOptions{Authenticate: auth.Check})
	client := clientserver.New(clientserver.Config{})

	u := unifiedserver.New(unifiedserver.Config{})
	if err := u.Mount("/ws", server.CORS(server.RouteByMode(
		http.HandlerFunc(client.ServeWS),
		map[string]http.Handler{server.ModeDevice: device},
	))); err != nil {
		t.Fatalf("mount /ws: %v", err)
	}

	// The driver feeds the client server directly, which is what the agent
	// wires up.
	ctx, cancel := context.WithCancel(context.Background())
	go pumpTo(ctx, deviceMgr.Data(), client)

	ts := httptest.NewServer(u.Handler())

	t.Cleanup(func() {
		ts.Close()
		cancel()
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

// pumpTo forwards a driver's scans to the client server.
func pumpTo(ctx context.Context, src <-chan nfc.NFCData, sink *clientserver.Server) {
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-src:
			if !ok {
				return
			}
			sink.Broadcast(data)
		}
	}
}
