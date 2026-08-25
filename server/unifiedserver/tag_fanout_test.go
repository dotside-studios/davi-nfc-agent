package unifiedserver_test

import (
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/gorilla/websocket"
)

// dial opens a connection to the unified server and fails the test if it cannot.
func dial(t *testing.T, url string) *websocket.Conn {
	t.Helper()

	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial %s: %v (status %d)", url, err, status)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// registerDevice performs the v0 handshake a phone falls back to and returns the
// device ID the agent assigned.
func registerDevice(t *testing.T, conn *websocket.Conn) string {
	t.Helper()

	err := conn.WriteJSON(protocol.WebSocketRequest{
		Type: remotenfc.WSTypeRegisterDevice,
		Payload: map[string]any{
			"deviceName": "Test Phone",
			"platform":   "android",
			"appVersion": "1.0.0",
			"capabilities": map[string]any{
				"canRead": true,
				"nfcType": "nfca",
			},
		},
	})
	if err != nil {
		t.Fatalf("registerDevice: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var resp protocol.WebSocketResponse
	if err := conn.ReadJSON(&resp); err != nil {
		t.Fatalf("read registration response: %v", err)
	}
	if !resp.Success {
		t.Fatalf("registration refused: %s", resp.Error)
	}

	payload, ok := resp.Payload.(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected registration payload: %#v", resp.Payload)
	}
	id, _ := payload["deviceID"].(string)
	if id == "" {
		t.Fatalf("registration returned no device ID: %#v", payload)
	}
	return id
}

// A tag a phone scans has to reach the applications watching the agent, the
// control center among them, which speaks the same client endpoint as any other
// consumer. This is the whole path: device socket, manager, bridge, fanout.
func TestPhoneScanReachesAClient(t *testing.T) {
	base := newTestServer(t)

	client := dial(t, base+"/ws")
	device := dial(t, base+"/ws?mode=device")
	deviceID := registerDevice(t, device)

	err := device.WriteJSON(protocol.WebSocketRequest{
		Type: remotenfc.WSTypeTagScanned,
		Payload: map[string]any{
			"deviceID":   deviceID,
			"uid":        "04:A2:B3:C4",
			"technology": "ISO14443A",
			"type":       "MIFARE Ultralight",
			"scannedAt":  time.Now().Format(time.RFC3339),
		},
	})
	if err != nil {
		t.Fatalf("send tagScanned: %v", err)
	}

	// The client may see unrelated traffic first (device status), so read until
	// the tag arrives or the deadline says it never will.
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	for {
		var msg protocol.WebSocketMessage
		if err := client.ReadJSON(&msg); err != nil {
			t.Fatalf("client never received the scanned tag: %v", err)
		}
		if msg.Type != "tagData" {
			continue
		}

		payload, ok := msg.Payload.(map[string]interface{})
		if !ok {
			t.Fatalf("unexpected tagData payload: %#v", msg.Payload)
		}
		if uid, _ := payload["uid"].(string); uid != "04:A2:B3:C4" {
			t.Fatalf("tagData carried UID %q, want 04:A2:B3:C4 (payload %#v)", uid, payload)
		}

		// deviceStatus describes the agent's own reader, and reports no card
		// while a phone holds one. A client showing this tag needs to know the
		// status has nothing to say about it.
		if got, _ := payload["deviceID"].(string); got != deviceID {
			t.Fatalf("tagData named device %q, want %q (payload %#v)", got, deviceID, payload)
		}
		return
	}
}
