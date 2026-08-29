package remotenfc

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

// TestPhone_ResponseFromWrongDeviceDoesNotCancelVictim: two phones are
// connected and one has a write in flight. A response that arrives from a
// DIFFERENT phone carrying the victim's request ID must not cancel the victim's
// pending request. Request IDs on the tag-holder path are predictable
// (write-1, write-2, ...), so a buggy or hostile second phone could otherwise
// strand another phone's write until its full 20s timeout by echoing an ID it
// does not own.
func TestPhone_ResponseFromWrongDeviceDoesNotCancelVictim(t *testing.T) {
	m, url := serveManager(t, time.Minute)
	victim, victimID := connectDevice(t, url)
	attacker, attackerID := connectDevice(t, url)
	if victimID == attackerID {
		t.Fatal("expected two distinct devices")
	}

	// Start a write to the victim; it blocks until the victim answers.
	writeErr := make(chan error, 1)
	go func() {
		_, err := m.WriteToDevice(context.Background(), victimID, DeviceWriteRequest{
			RequestID: "victim-req-1",
			TagUID:    "04A1B2C3",
			NDEFBytes: []byte{0x01},
		})
		writeErr <- err
	}()

	// The victim receives the write request, so its pending entry now exists.
	var req protocol.WebSocketRequest
	_ = victim.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err := victim.ReadJSON(&req); err != nil {
		t.Fatalf("victim did not receive the write request: %v", err)
	}
	reqID, _ := req.Payload["requestID"].(string)
	if reqID == "" {
		reqID = req.ID
	}

	// The ATTACKER answers with the victim's request ID.
	if err := attacker.WriteJSON(protocol.WebSocketResponse{
		Type:    WSTypeDeviceWriteResponse,
		Payload: map[string]any{"requestID": reqID, "success": true},
	}); err != nil {
		t.Fatalf("attacker response: %v", err)
	}
	// Let the server process the cross-device response.
	time.Sleep(300 * time.Millisecond)

	// The victim now answers its own write. It must still be delivered.
	if err := victim.WriteJSON(protocol.WebSocketResponse{
		Type:    WSTypeDeviceWriteResponse,
		Payload: map[string]any{"requestID": reqID, "success": true},
	}); err != nil {
		t.Fatalf("victim response: %v", err)
	}

	select {
	case err := <-writeErr:
		if err != nil {
			t.Fatalf("victim's write failed after another device answered with its ID: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("victim's write hung: a response from another device cancelled its pending request")
	}
}

// TestPhone_DisconnectDuringWriteFailsFast: a phone that drops mid-write must
// release the waiter promptly with a typed error, not leave it blocked for the
// full 20s device-write timeout.
func TestPhone_DisconnectDuringWriteFailsFast(t *testing.T) {
	m, url := serveManager(t, time.Minute)
	phone, deviceID := connectDevice(t, url)

	writeErr := make(chan error, 1)
	go func() {
		_, err := m.WriteToDevice(context.Background(), deviceID, DeviceWriteRequest{
			RequestID: "w-1", TagUID: "04A1B2C3", NDEFBytes: []byte{0x01},
		})
		writeErr <- err
	}()

	var req protocol.WebSocketRequest
	_ = phone.SetReadDeadline(time.Now().Add(3 * time.Second))
	if err := phone.ReadJSON(&req); err != nil {
		t.Fatalf("phone did not receive the write request: %v", err)
	}
	// The phone vanishes without answering.
	_ = phone.Close()

	select {
	case err := <-writeErr:
		if err == nil {
			t.Fatal("expected an error when the phone disconnected mid-write")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("write did not fail promptly after the phone disconnected (waited past the fail-fast window)")
	}
}

// TestPhone_ConcurrentWritesCorrelateByRequestID: two writes are in flight on one
// session and the phone answers them in the reverse order. Each caller must
// receive its own outcome, proving responses route by request ID rather than by
// arrival order.
func TestPhone_ConcurrentWritesCorrelateByRequestID(t *testing.T) {
	m, url := serveManager(t, time.Minute)
	phone, deviceID := connectDevice(t, url)

	type writeOutcome struct {
		resp DeviceWriteResponse
		err  error
	}
	outA := make(chan writeOutcome, 1)
	outB := make(chan writeOutcome, 1)
	go func() {
		resp, err := m.WriteToDevice(context.Background(), deviceID, DeviceWriteRequest{
			RequestID: "wA", TagUID: "04AA", NDEFBytes: []byte{0x0A},
		})
		outA <- writeOutcome{resp, err}
	}()
	go func() {
		resp, err := m.WriteToDevice(context.Background(), deviceID, DeviceWriteRequest{
			RequestID: "wB", TagUID: "04BB", NDEFBytes: []byte{0x0B},
		})
		outB <- writeOutcome{resp, err}
	}()

	// Read both requests (order not guaranteed) and note their IDs.
	ids := map[string]bool{}
	_ = phone.SetReadDeadline(time.Now().Add(3 * time.Second))
	for i := 0; i < 2; i++ {
		var req protocol.WebSocketRequest
		if err := phone.ReadJSON(&req); err != nil {
			t.Fatalf("phone did not receive request %d: %v", i, err)
		}
		id, _ := req.Payload["requestID"].(string)
		ids[id] = true
	}
	if !ids["wA"] || !ids["wB"] {
		t.Fatalf("phone received requests %v, want both wA and wB", ids)
	}

	// Answer in reverse order: wB succeeds, wA fails with a distinct message.
	if err := phone.WriteJSON(protocol.WebSocketResponse{
		Type:    WSTypeDeviceWriteResponse,
		Payload: map[string]any{"requestID": "wB", "success": true},
	}); err != nil {
		t.Fatalf("answer wB: %v", err)
	}
	if err := phone.WriteJSON(protocol.WebSocketResponse{
		Type:    WSTypeDeviceWriteResponse,
		Payload: map[string]any{"requestID": "wA", "success": false, "error": "A-specific-failure"},
	}); err != nil {
		t.Fatalf("answer wA: %v", err)
	}

	select {
	case o := <-outB:
		if o.err != nil || !o.resp.Success {
			t.Errorf("wB caller should have received its own success, got resp=%+v err=%v", o.resp, o.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wB response was not delivered to its caller")
	}
	select {
	case o := <-outA:
		if o.err != nil {
			t.Errorf("wA transport should not error, got %v", o.err)
		}
		if o.resp.Success || !strings.Contains(o.resp.Error, "A-specific-failure") {
			t.Errorf("wA caller should have received its own failure, got resp=%+v", o.resp)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("wA response was not delivered to its caller")
	}
}
