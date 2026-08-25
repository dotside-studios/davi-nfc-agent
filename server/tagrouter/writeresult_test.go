package tagrouter_test

import (
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

// TestDeviceWriteReportsAResult pins what a client is told after a write to a
// phone: the same six fields the reader route fills in, so the two routes
// answer in one shape. The router returns *nfc.WriteResult, so a route that
// reported nothing would once have compiled; what is still worth pinning is
// that the fields carry the tag's own facts rather than zero values.
func TestDeviceWriteReportsAResult(t *testing.T) {
	url, st := newWriteTestServer(t)

	conn, deviceID := registerV1(t, url)
	scanTag(t, conn, st, deviceID, scannedUID)

	msg := sampleWrite("wr1")
	msg.Request.Lock = true
	done := goWrite(st.Router, msg)

	req := readDeviceWriteRequest(t, conn)
	requestID, _ := req.Payload["requestID"].(string)

	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type: remotenfc.WSTypeDeviceWriteResponse,
		Payload: map[string]any{
			"requestID": requestID,
			"success":   true,
		},
	}); err != nil {
		t.Fatalf("write response: %v", err)
	}

	select {
	case res := <-done:
		if !res.Success {
			t.Fatalf("write failed: %s", res.Error)
		}

		result, ok := res.Payload.(*nfc.WriteResult)
		if !ok {
			t.Fatalf("payload is %T, want *nfc.WriteResult", res.Payload)
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
		// The device answers a read from the snapshot it captured when it
		// scanned, so a write there cannot be confirmed by reading it back.
		if result.Verified {
			t.Error("Verified = true, but a tag whose reads are a snapshot cannot confirm a write")
		}

	case <-time.After(3 * time.Second):
		t.Fatal("no write result reached the caller")
	}
}
