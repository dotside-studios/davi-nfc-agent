package clientserver

import (
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/server"
)

// The UID a browser puts in its request has to survive the trip across the
// bridge, or the agent resolves the route against nothing and refuses every
// write. These names are easy to get wrong silently: the field is optional, so
// a typo reads as "the client named no tag".
//
// Sent over a real connection, because that is the only path that exercises the
// JSON names the client library actually writes.
func TestRequestPayloadUIDReachesTheBridge(t *testing.T) {
	const uid = "04:A1:B2:C3"
	const device = "phone-1"

	t.Run("write", func(t *testing.T) {
		s := newTestServer(nil)
		conn := dial(t, s, "https://app.example.com")

		_ = conn.WriteJSON(map[string]any{
			"type": "writeRequest",
			"payload": map[string]any{
				"records":         []map[string]any{{"type": "text", "content": "x"}},
				"uid":             uid,
				"deviceID":        device,
				"allowUntargeted": true,
			},
		})

		select {
		case m := <-s.bridge.WriteRequest:
			assertTarget(t, m.TagUID, m.TargetDevice, m.AllowUntargeted, uid, device)
		case <-time.After(2 * time.Second):
			t.Fatal("no write reached the bridge")
		}
	})

	t.Run("lock", func(t *testing.T) {
		s := newTestServer(nil)
		conn := dial(t, s, "https://app.example.com")

		_ = conn.WriteJSON(map[string]any{
			"type":    "lockRequest",
			"payload": map[string]any{"uid": uid, "deviceID": device, "allowUntargeted": true},
		})

		select {
		case m := <-s.bridge.LockRequest:
			assertTarget(t, m.TagUID, m.TargetDevice, m.AllowUntargeted, uid, device)
		case <-time.After(2 * time.Second):
			t.Fatal("no lock reached the bridge")
		}
	})

	t.Run("transceive", func(t *testing.T) {
		s := newTestServer(nil)
		conn := dial(t, s, "https://app.example.com")

		_ = conn.WriteJSON(map[string]any{
			"type": "transceiveRequest",
			"payload": map[string]any{
				"data": "MAA=", "uid": uid, "deviceID": device, "allowUntargeted": true,
			},
		})

		select {
		case m := <-s.bridge.Transceive:
			assertTarget(t, m.TagUID, m.TargetDevice, m.AllowUntargeted, uid, device)
		case <-time.After(2 * time.Second):
			t.Fatal("no transceive reached the bridge")
		}
	})

	t.Run("capabilities", func(t *testing.T) {
		s := newTestServer(nil)
		conn := dial(t, s, "https://app.example.com")

		_ = conn.WriteJSON(map[string]any{
			"type":    "capabilitiesRequest",
			"payload": map[string]any{"uid": uid, "deviceID": device, "allowUntargeted": true},
		})

		select {
		case m := <-s.bridge.CapabilitiesRequest:
			assertTarget(t, m.TagUID, m.TargetDevice, m.AllowUntargeted, uid, device)
		case <-time.After(2 * time.Second):
			t.Fatal("no capabilities query reached the bridge")
		}
	})
}

func assertTarget(t *testing.T, gotUID, gotDevice string, gotAllow bool, wantUID, wantDevice string) {
	t.Helper()

	if gotUID != wantUID {
		t.Errorf("TagUID = %q, want %q", gotUID, wantUID)
	}
	if gotDevice != wantDevice {
		t.Errorf("TargetDevice = %q, want %q", gotDevice, wantDevice)
	}
	if !gotAllow {
		t.Error("AllowUntargeted did not survive the trip")
	}
}

var _ = server.NewServerBridge
