package tagrouter_test

import (
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/gorilla/websocket"
)

// newWriteTestServer exposes the bridge alongside the device endpoint so a test
// can submit a write the way the client server would.
func newWriteTestServer(t *testing.T) (string, *stack) {
	t.Helper()
	st := newStack(t, stackConfig{})
	return st.URL, st
}

// scanTag makes the device the holder of the active tag, which is what a write
// request routes on. It waits for the tag to reach the bridge, so a write
// submitted next cannot overtake the scan it depends on.
func scanTag(t *testing.T, conn *websocket.Conn, st *stack, deviceID, uid string) {
	t.Helper()

	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type: remotenfc.WSTypeTagScanned,
		Payload: map[string]any{
			"deviceID":   deviceID,
			"uid":        uid,
			"technology": "ISO14443A",
			"type":       "NTAG215",
		},
	}); err != nil {
		t.Fatalf("write tagScanned: %v", err)
	}

	// Wait for the scan to land, so a request submitted next cannot overtake
	// the scan it depends on.
	st.Client.await(t)
}

// scannedUID is the tag every test in this package presents. Requests name it,
// because the agent no longer guesses which tag a request means.
const scannedUID = "04:A1:B2:C3"

func sampleWrite(requestID string) server.WriteOp {
	return sampleWriteFor(requestID, scannedUID)
}

func sampleWriteFor(requestID, uid string) server.WriteOp {
	return server.WriteOp{
		Target: server.Target{TagUID: uid},
		Request: server.WriteRequest{
			Records: []server.WriteRecord{{Type: "text", Content: "Hello, NFC!"}},
		},
		IdempotencyKey: "key-" + requestID,
	}
}

// readDeviceWriteRequest waits for the agent to forward a write to the device.
func readDeviceWriteRequest(t *testing.T, conn *websocket.Conn) protocol.WebSocketRequest {
	t.Helper()

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	for {
		var msg protocol.WebSocketRequest
		if err := conn.ReadJSON(&msg); err != nil {
			t.Fatalf("read device message: %v", err)
		}
		if msg.Type == remotenfc.WSTypeDeviceWriteRequest {
			return msg
		}
	}
}

func TestWriteRoutesToDeviceHoldingTag(t *testing.T) {
	url, st := newWriteTestServer(t)

	conn, deviceID := registerV1(t, url)
	scanTag(t, conn, st, deviceID, scannedUID)

	msg := sampleWrite("w1")
	done := goWrite(st.Router, msg)

	req := readDeviceWriteRequest(t, conn)

	if got, _ := req.Payload["tagUID"].(string); got != scannedUID {
		t.Errorf("tagUID = %q, want the scanned UID", got)
	}
	if got, _ := req.Payload["idempotencyKey"].(string); got != "key-w1" {
		t.Errorf("idempotencyKey = %q, want key-w1", got)
	}
	if got, _ := req.Payload["deviceID"].(string); got != deviceID {
		t.Errorf("deviceID = %q, want %q", got, deviceID)
	}
	// The encoded message travels alongside the records and is authoritative.
	if _, ok := req.Payload["ndefBytes"]; !ok {
		t.Error("write request carried no ndefBytes")
	}

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
	case resp := <-done:
		if !resp.Success {
			t.Errorf("write failed: %s", resp.Error)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no write response reached the caller")
	}
}

func TestWriteReportsDeviceFailure(t *testing.T) {
	url, st := newWriteTestServer(t)

	conn, deviceID := registerV1(t, url)
	scanTag(t, conn, st, deviceID, scannedUID)

	msg := sampleWrite("w2")
	done := goWrite(st.Router, msg)

	req := readDeviceWriteRequest(t, conn)
	requestID, _ := req.Payload["requestID"].(string)

	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type: remotenfc.WSTypeDeviceWriteResponse,
		Payload: map[string]any{
			"requestID": requestID,
			"success":   false,
			"error":     "tag is read-only",
			"errorCode": string(protocol.ErrCodeReadOnly),
		},
	}); err != nil {
		t.Fatalf("write response: %v", err)
	}

	select {
	case resp := <-done:
		if resp.Success {
			t.Error("expected the write to be reported as failed")
		}
		if resp.Error != "tag is read-only" {
			t.Errorf("error = %q, want the device's message", resp.Error)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no write response reached the caller")
	}
}

// A device that vanishes mid-write must release the waiter immediately rather
// than making it sit out the full timeout.
func TestWriteFailsFastWhenDeviceDisconnects(t *testing.T) {
	url, st := newWriteTestServer(t)

	conn, deviceID := registerV1(t, url)
	scanTag(t, conn, st, deviceID, scannedUID)

	msg := sampleWrite("w3")
	done := goWrite(st.Router, msg)

	readDeviceWriteRequest(t, conn)
	_ = conn.Close()

	select {
	case resp := <-done:
		if resp.Success {
			t.Error("expected failure after the device disconnected")
		}
		if resp.Error == "" {
			t.Error("expected an explanation for the failure")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiter was not released when the device disconnected")
	}
}

// With no device holding a tag and no hardware reader, the write is refused
// rather than being sent nowhere.
func TestWriteWithoutActiveTagIsRefused(t *testing.T) {
	url, st := newWriteTestServer(t)

	// Register a device but never scan, so nothing holds a tag.
	registerV1(t, url)

	msg := sampleWrite("w4")
	done := goWrite(st.Router, msg)

	select {
	case resp := <-done:
		if resp.Success {
			t.Error("expected refusal when no tag is present")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("write request was neither routed nor refused")
	}
}

// A tag leaving the field clears the routing target.
func TestTagRemovalClearsWriteTarget(t *testing.T) {
	url, st := newWriteTestServer(t)

	conn, deviceID := registerV1(t, url)
	scanTag(t, conn, st, deviceID, scannedUID)

	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type: remotenfc.WSTypeTagRemoved,
		Payload: map[string]any{
			"deviceID":  deviceID,
			"uid":       scannedUID,
			"removedAt": time.Now().Format(time.RFC3339),
		},
	}); err != nil {
		t.Fatalf("write tagRemoved: %v", err)
	}

	// Give the removal time to land before submitting the write.
	time.Sleep(200 * time.Millisecond)

	msg := sampleWrite("w5")
	done := goWrite(st.Router, msg)

	select {
	case resp := <-done:
		if resp.Success {
			t.Error("expected refusal after the tag left the field")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("write request was neither routed nor refused")
	}
}

// awaitTag returns the tag the agent built from a device's scan, which is what
// internal consumers operate on.
func awaitTag(t *testing.T, st *stack) nfc.Tag {
	t.Helper()

	data := st.Client.await(t)
	if data.Card == nil {
		t.Fatal("the scan carried no card")
	}
	return data.Card.GetUnderlyingTag()
}

// scanCapableTag reports a tag whose device declared write and transceive, and
// returns the resulting Tag.
func scanCapableTag(t *testing.T, conn *websocket.Conn, st *stack, deviceID string) nfc.Tag {
	t.Helper()

	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type: remotenfc.WSTypeTagScanned,
		Payload: map[string]any{
			"deviceID":   deviceID,
			"uid":        scannedUID,
			"technology": "ISO14443A",
			"type":       "Type4",
			"capabilities": map[string]any{
				"canTransceive": true,
				"canWrite":      true,
			},
		},
	}); err != nil {
		t.Fatalf("write tagScanned: %v", err)
	}

	return awaitTag(t, st)
}

func TestTransceiveRoundTrip(t *testing.T) {
	url, st := newWriteTestServer(t)

	conn, deviceID := registerCapableV1(t, url)
	tag := scanCapableTag(t, conn, st, deviceID)

	if !nfc.GetTagCapabilities(tag).CanTransceive {
		t.Fatal("tag does not report transceive despite device and tag declaring it")
	}

	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		data, err := tag.Transceive([]byte{0x00, 0xA4, 0x04, 0x00})
		done <- result{data, err}
	}()

	// The device receives the exchange and answers it.
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var req protocol.WebSocketRequest
	for {
		if err := conn.ReadJSON(&req); err != nil {
			t.Fatalf("read device message: %v", err)
		}
		if req.Type == remotenfc.WSTypeDeviceTransceiveRequest {
			break
		}
	}

	if raw, _ := req.Payload["raw"].(bool); raw {
		t.Error("Tag.Transceive requested raw framing, want APDU-level")
	}
	if got, _ := req.Payload["tagUID"].(string); got != scannedUID {
		t.Errorf("tagUID = %q, want the scanned UID", got)
	}

	requestID, _ := req.Payload["requestID"].(string)
	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type: remotenfc.WSTypeDeviceTransceiveResponse,
		Payload: map[string]any{
			"requestID": requestID,
			"success":   true,
			// 0x9000, ISO 7816 success.
			"data": []byte{0x90, 0x00},
		},
	}); err != nil {
		t.Fatalf("write transceive response: %v", err)
	}

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("Transceive: %v", r.err)
		}
		if len(r.data) != 2 || r.data[0] != 0x90 || r.data[1] != 0x00 {
			t.Errorf("response = %v, want 9000", r.data)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("transceive never completed")
	}
}

// A device reporting a failure yields a typed error carrying the code it sent.
func TestTransceiveFailureKeepsCode(t *testing.T) {
	url, st := newWriteTestServer(t)

	conn, deviceID := registerCapableV1(t, url)
	tag := scanCapableTag(t, conn, st, deviceID)

	done := make(chan error, 1)
	go func() {
		_, err := tag.Transceive([]byte{0x00})
		done <- err
	}()

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var req protocol.WebSocketRequest
	for {
		if err := conn.ReadJSON(&req); err != nil {
			t.Fatalf("read device message: %v", err)
		}
		if req.Type == remotenfc.WSTypeDeviceTransceiveRequest {
			break
		}
	}

	requestID, _ := req.Payload["requestID"].(string)
	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type: remotenfc.WSTypeDeviceTransceiveResponse,
		Payload: map[string]any{
			"requestID": requestID,
			"success":   false,
			"error":     "tag left the field",
			"errorCode": string(protocol.ErrCodeTagRemoved),
		},
	}); err != nil {
		t.Fatalf("write transceive response: %v", err)
	}

	select {
	case err := <-done:
		if !nfc.IsTagRemovedError(err) {
			t.Errorf("error = %v, want the device's TAG_REMOVED to survive", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("transceive never completed")
	}
}
