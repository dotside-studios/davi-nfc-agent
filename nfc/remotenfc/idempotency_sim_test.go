package remotenfc

import (
	"context"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

// TestPhone_RetriedWriteCarriesSameIdempotencyKey pins the idempotency contract
// of the phone bridge: the agent does NOT dedup writes itself. A write retried
// with the same idempotency key is forwarded to the device again — with the same
// key and a fresh request ID — so the device is the party that recognises the
// replay and applies the operation once. This is deliberate (the device holds
// the tag and is the only thing that can know whether the earlier attempt
// landed); the test guards that the key actually reaches the device unchanged on
// every attempt, which is what makes device-side dedup possible.
func TestPhone_RetriedWriteCarriesSameIdempotencyKey(t *testing.T) {
	m, url := serveManager(t, time.Minute)
	phone, deviceID := connectDevice(t, url)

	const key = "stable-idempotency-key"

	// Two attempts of the same logical write, carrying the same key.
	done := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			msg := nfc.NewNDEFMessage()
			msg.AddRecord((&nfc.NDEFText{Content: "hello", Language: "en"}).ToRecord())
			_, err := m.WriteTag(context.Background(), deviceID, "04A1B2C3", msg, false, key)
			done <- err
		}()
	}

	// The phone receives two write requests; both must carry the same key, with
	// distinct request IDs (each attempt is its own in-flight request).
	reqIDs := map[string]bool{}
	_ = phone.SetReadDeadline(time.Now().Add(3 * time.Second))
	for i := 0; i < 2; i++ {
		var req protocol.WebSocketRequest
		if err := phone.ReadJSON(&req); err != nil {
			t.Fatalf("phone did not receive write request %d: %v", i, err)
		}
		if k, _ := req.Payload["idempotencyKey"].(string); k != key {
			t.Errorf("attempt %d carried idempotency key %q, want %q — the device cannot dedup without it", i, k, key)
		}
		id, _ := req.Payload["requestID"].(string)
		reqIDs[id] = true

		// Answer so the caller unblocks.
		_ = phone.WriteJSON(protocol.WebSocketResponse{
			Type:    WSTypeDeviceWriteResponse,
			Payload: map[string]any{"requestID": id, "success": true},
		})
	}

	if len(reqIDs) != 2 {
		t.Errorf("two attempts should have two distinct request IDs, got %d", len(reqIDs))
	}

	for i := 0; i < 2; i++ {
		if err := <-done; err != nil {
			t.Errorf("write attempt failed: %v", err)
		}
	}
}
