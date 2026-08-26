package remotenfc

import (
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/event"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/gorilla/websocket"
)

// fakeRevocations stands in for the agent's device registry: something that
// says which credentials have stopped being valid.
type fakeRevocations struct {
	revoked event.Signal[[]string]
}

func (f *fakeRevocations) OnRevoke(fn func(ids []string)) *event.Connection {
	return f.revoked.Connect(fn)
}

// serveManagerWithRevocations mounts the endpoint with a revocation source
// attached, and returns the source so a test can revoke.
func serveManagerWithRevocations(t *testing.T) (*Manager, string, *fakeRevocations) {
	t.Helper()

	src := &fakeRevocations{}
	m := NewManager(time.Minute)
	ts := newDeviceServer(t, m, ServerOptions{
		AllowUnauthenticated: true,
		Revocations:          src,
	})

	return m, ts, src
}

// A credential is checked once, at the upgrade. Revoking a device used to leave
// its established session streaming scans and accepting writes until the device
// chose to reconnect, which for a heartbeating device is never.
func TestRevokingADeviceEndsItsSession(t *testing.T) {
	m, url, revocations := serveManagerWithRevocations(t)

	conn, deviceID := connectDevice(t, url)
	awaitDeviceCount(t, m, "after registration", 1)

	revocations.revoked.Emit([]string{deviceID})

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, _, err := conn.ReadMessage()
		if err == nil {
			continue
		}
		if !websocket.IsCloseError(err, websocket.ClosePolicyViolation) {
			t.Fatalf("read after revocation: got %v, want a policy-violation close", err)
		}
		break
	}

	awaitDeviceCount(t, m, "after revocation", 0)
}

// Revoking one device must not disturb the others: that separation is the whole
// reason per-device credentials exist.
func TestRevokingOneDeviceLeavesTheOthers(t *testing.T) {
	m, url, revocations := serveManagerWithRevocations(t)

	doomed, doomedID := connectDevice(t, url)
	survivor, survivorID := connectDevice(t, url)
	awaitDeviceCount(t, m, "after registration", 2)

	revocations.revoked.Emit([]string{doomedID})
	awaitDeviceCount(t, m, "after revocation", 1)

	if err := survivor.WriteJSON(protocol.WebSocketRequest{
		Type:    WSTypeDeviceHeartbeat,
		Payload: map[string]any{"deviceID": survivorID},
	}); err != nil {
		t.Fatalf("the surviving device's session was closed too: %v", err)
	}
	if _, ok := m.GetDevice(survivorID); !ok {
		t.Fatal("the surviving device was unregistered")
	}

	_ = doomed.Close()
}

// Revoking a device that is not connected is a no-op, not a panic or a stray
// teardown of somebody else's session.
func TestRevokingADisconnectedDeviceIsHarmless(t *testing.T) {
	m, url, revocations := serveManagerWithRevocations(t)

	conn, deviceID := connectDevice(t, url)
	awaitDeviceCount(t, m, "after registration", 1)

	revocations.revoked.Emit([]string{"never-connected"})

	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type:    WSTypeDeviceHeartbeat,
		Payload: map[string]any{"deviceID": deviceID},
	}); err != nil {
		t.Fatalf("an unrelated revocation closed the session: %v", err)
	}
	awaitDeviceCount(t, m, "after an unrelated revocation", 1)
}
