package remotenfc

import (
	"strings"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/gorilla/websocket"
)

// TestOversizedFrameEndsSession checks that a device cannot make the agent
// allocate for a frame of any size it likes. Without a read limit the frame is
// read whole and the session carries on.
func TestOversizedFrameEndsSession(t *testing.T) {
	m, url := serveManager(t, time.Minute)

	conn, deviceID := connectDevice(t, url)
	awaitDeviceCount(t, m, "after registration", 1)

	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type: WSTypeTagScanned,
		Payload: map[string]any{
			"uid":       strings.Repeat("a", MaxDeviceMessageSize+1),
			"tagType":   "NTAG215",
			"deviceID":  deviceID,
			"timestamp": time.Now().UnixMilli(),
		},
	}); err != nil {
		t.Fatalf("write oversized frame: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		_, _, err := conn.ReadMessage()
		if err == nil {
			continue // an error reply on the way to the close
		}
		if !websocket.IsCloseError(err, websocket.CloseMessageTooBig) {
			t.Fatalf("read after oversized frame: got %v, want a message-too-big close", err)
		}
		break
	}

	awaitDeviceCount(t, m, "after oversized frame", 0)
}

// TestLargeFrameWithinLimitIsServed guards the other side of the limit: a write
// carrying a real NDEF payload is well under it and must still be read.
func TestLargeFrameWithinLimitIsServed(t *testing.T) {
	m, url := serveManager(t, time.Minute)

	conn, deviceID := connectDevice(t, url)
	awaitDeviceCount(t, m, "after registration", 1)

	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type: WSTypeTagScanned,
		Payload: map[string]any{
			"uid":        "04A1B2C3D4E5F6",
			"tagType":    "NTAG215",
			"technology": "ISO14443A",
			"deviceID":   deviceID,
			"timestamp":  time.Now().UnixMilli(),
			"rawData":    strings.Repeat("b", 32<<10),
		},
	}); err != nil {
		t.Fatalf("write large frame: %v", err)
	}

	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type:    WSTypeDeviceHeartbeat,
		Payload: map[string]any{"deviceID": deviceID},
	}); err != nil {
		t.Fatalf("write heartbeat: %v", err)
	}

	// Still registered a moment later: the session survived the large frame.
	time.Sleep(100 * time.Millisecond)
	if got := m.GetDeviceCount(); got != 1 {
		t.Fatalf("device count = %d, want 1: a legitimate large frame ended the session", got)
	}
}
