package remotenfc

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/gorilla/websocket"
)

// serveManager exposes a manager's device endpoint over a test server.
func serveManager(t *testing.T, timeout time.Duration) (*Manager, string) {
	t.Helper()

	m := NewManager(timeout)
	ts := httptest.NewServer(m.Handler(ServerOptions{}))

	t.Cleanup(func() {
		ts.Close()
		m.Close()
	})

	return m, "ws" + strings.TrimPrefix(ts.URL, "http") + "?mode=device"
}

// connectDevice registers a device and returns its connection and ID.
func connectDevice(t *testing.T, url string) (*websocket.Conn, string) {
	t.Helper()

	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type: protocol.WSTypeHello,
		Payload: map[string]any{
			"protocolVersion": protocol.DeviceProtocolV1,
			"deviceName":      "Test Device",
			"platform":        "android",
		},
	}); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var resp struct {
		Payload struct {
			DeviceID string `json:"deviceID"`
		} `json:"payload"`
	}
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("read hello response: %v", err)
	}
	if resp.Payload.DeviceID == "" {
		t.Fatal("registration returned no deviceID")
	}
	_ = conn.SetReadDeadline(time.Time{})

	return conn, resp.Payload.DeviceID
}

// awaitDeviceCount waits for the registry to settle on want.
func awaitDeviceCount(t *testing.T, m *Manager, want string, count int) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if m.GetDeviceCount() == count {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s: device count = %d, want %d", want, m.GetDeviceCount(), count)
}

func TestSessionRegistersAndUnregisters(t *testing.T) {
	m, url := serveManager(t, DeviceTimeout)

	conn, deviceID := connectDevice(t, url)
	awaitDeviceCount(t, m, "after connect", 1)

	if _, ok := m.session(deviceID); !ok {
		t.Error("registered device has no session")
	}

	_ = conn.Close()
	awaitDeviceCount(t, m, "after disconnect", 0)

	if _, ok := m.session(deviceID); ok {
		t.Error("session outlived the connection")
	}
}

// The registry and the sessions are one thing now. A device swept for silence
// must lose its connection too, or a live socket stays bound to a device the
// manager no longer knows.
func TestSilentDeviceLosesItsSession(t *testing.T) {
	m, url := serveManager(t, 50*time.Millisecond)

	conn, deviceID := connectDevice(t, url)
	awaitDeviceCount(t, m, "after connect", 1)

	// Let the device fall past the inactivity timeout without heartbeating.
	time.Sleep(100 * time.Millisecond)

	m.cleanupInactiveDevices()
	awaitDeviceCount(t, m, "after sweep", 0)

	if _, ok := m.session(deviceID); ok {
		t.Error("swept device kept its session")
	}

	// The device sees the close rather than being left talking to nobody.
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Error("swept device's connection stayed open")
	}
}

// A heartbeat keeps a device registered, so the sweep only reaps real silence.
func TestHeartbeatSurvivesTheSweep(t *testing.T) {
	m, url := serveManager(t, time.Minute)

	conn, deviceID := connectDevice(t, url)
	awaitDeviceCount(t, m, "after connect", 1)

	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type:    protocol.WSTypeDeviceHeartbeat,
		Payload: map[string]any{"deviceID": deviceID},
	}); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}

	m.cleanupInactiveDevices()

	if m.GetDeviceCount() != 1 {
		t.Error("sweep dropped a device that had just been heard from")
	}
	if _, ok := m.session(deviceID); !ok {
		t.Error("sweep dropped the session of a live device")
	}
}

// DeviceTimeout has to leave room for missed heartbeats. Equal values put a
// device that only heartbeats exactly on the sweep boundary.
func TestDeviceTimeoutAllowsMissedHeartbeats(t *testing.T) {
	if DeviceTimeout <= HeartbeatInterval {
		t.Fatalf("DeviceTimeout %v must exceed HeartbeatInterval %v", DeviceTimeout, HeartbeatInterval)
	}
	if DeviceTimeout < 3*HeartbeatInterval {
		t.Errorf("DeviceTimeout %v leaves under three heartbeats of %v", DeviceTimeout, HeartbeatInterval)
	}
}
