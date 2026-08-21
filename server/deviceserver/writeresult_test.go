package deviceserver_test

import (
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

// TestDeviceWriteReportsAResult pins what a client is told after a write to a
// phone. The client server reads the outcome by asserting *nfc.WriteResult, so
// anything else reaches the client as a bare success with none of the fields
// the protocol documents -- the same shape the reader route fills in.
func TestDeviceWriteReportsAResult(t *testing.T) {
	url, bridge := newWriteTestServer(t)

	conn, deviceID := registerV1(t, url)
	scanTag(t, conn, bridge, deviceID, scannedUID)

	msg := sampleWrite("wr1")
	msg.Request.Lock = true
	go func() { bridge.WriteRequest <- msg }()

	req := readDeviceWriteRequest(t, conn)
	requestID, _ := req.Payload["requestID"].(string)

	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type: protocol.WSTypeDeviceWriteResponse,
		Payload: map[string]any{
			"requestID": requestID,
			"success":   true,
		},
	}); err != nil {
		t.Fatalf("write response: %v", err)
	}

	select {
	case resp := <-msg.ResponseCh:
		if !resp.Success {
			t.Fatalf("write failed: %s", resp.Error)
		}

		result, ok := resp.Payload.(*nfc.WriteResult)
		if !ok {
			t.Fatalf("Payload is %T, want *nfc.WriteResult -- the client server "+
				"reads the outcome by that assertion and reports nothing without it", resp.Payload)
		}
		if result.UID != scannedUID {
			t.Errorf("UID = %q, want %q", result.UID, scannedUID)
		}
		if result.TagType == "" {
			t.Error("TagType is empty, want the scanned tag's type")
		}
		if result.BytesWritten == 0 {
			t.Error("BytesWritten = 0, want the size of the encoded message")
		}
		if result.Attempts != 1 {
			t.Errorf("Attempts = %d, want 1", result.Attempts)
		}
		if !result.Locked {
			t.Error("Locked = false, but the request asked for a lock the device applied")
		}
		// A device answers from the snapshot it captured, so a write there
		// cannot be confirmed by reading it back.
		if result.Verified {
			t.Error("Verified = true, but a tag whose reads are a snapshot cannot confirm a write")
		}

	case <-time.After(3 * time.Second):
		t.Fatal("no write response reached the bridge")
	}
}

// TestDeviceWriteWithoutLockIsNotReportedLocked keeps the lock field honest: it
// reports what was asked for and applied, not a constant.
func TestDeviceWriteWithoutLockIsNotReportedLocked(t *testing.T) {
	url, bridge := newWriteTestServer(t)

	conn, deviceID := registerV1(t, url)
	scanTag(t, conn, bridge, deviceID, scannedUID)

	msg := sampleWrite("wr2")
	go func() { bridge.WriteRequest <- msg }()

	req := readDeviceWriteRequest(t, conn)
	requestID, _ := req.Payload["requestID"].(string)

	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type:    protocol.WSTypeDeviceWriteResponse,
		Payload: map[string]any{"requestID": requestID, "success": true},
	}); err != nil {
		t.Fatalf("write response: %v", err)
	}

	select {
	case resp := <-msg.ResponseCh:
		result, ok := resp.Payload.(*nfc.WriteResult)
		if !ok {
			t.Fatalf("Payload is %T, want *nfc.WriteResult", resp.Payload)
		}
		if result.Locked {
			t.Error("Locked = true, but the request did not ask for one")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no write response reached the bridge")
	}
}
