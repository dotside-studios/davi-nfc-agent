package clientserver

import (
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/gorilla/websocket"
)

// newModedTestServer is newWriteTestServer with a hardware reader attached in a
// given mode, so a test can exercise the policy a bare device server has no
// reader to read it from.
func newModedTestServer(t *testing.T, mode nfc.ReaderMode) (string, *stack) {
	t.Helper()

	st := newStack(t, stackConfig{Hardware: nfc.NewMockManager(), Mode: mode})
	return st.URL, st
}

func sampleLock(requestID string) server.LockOp {
	return sampleLockFor(requestID, scannedUID)
}

func sampleLockFor(requestID, uid string) server.LockOp {
	return server.LockOp{
		Target:         server.Target{TagUID: uid},
		IdempotencyKey: requestID,
	}
}

// A lock is irreversible, so it must reach the tag the client is looking at.
// Before the device route existed this was applied to whatever card happened to
// be on the hardware reader instead.
func TestLockRoutesToDeviceHoldingTag(t *testing.T) {
	url, st := newWriteTestServer(t)

	conn, deviceID := registerCapableV1(t, url)
	scanTag(t, conn, st, deviceID, scannedUID)

	msg := sampleLock("l1")
	done := goLock(st.Router, msg)

	req := readDeviceWriteRequest(t, conn)

	if got, _ := req.Payload["tagUID"].(string); got != scannedUID {
		t.Errorf("tagUID = %q, want the scanned UID", got)
	}
	if got, _ := req.Payload["deviceID"].(string); got != deviceID {
		t.Errorf("deviceID = %q, want %q", got, deviceID)
	}
	if lock, _ := req.Payload["lock"].(bool); !lock {
		t.Error("lock request did not set lock")
	}
	// A lock carries no message; sending one would rewrite the tag on the way.
	if _, ok := req.Payload["ndefBytes"]; ok {
		t.Error("lock request carried ndefBytes")
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
			t.Fatalf("lock failed: %s", resp.Error)
		}
		lr, ok := resp.Payload.(*nfc.LockResult)
		if !ok {
			t.Fatalf("payload = %#v, want *nfc.LockResult", resp.Payload)
		}
		if lr.UID != scannedUID || !lr.Locked {
			t.Errorf("result = %+v, want the scanned UID reported as locked", lr)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no lock response reached the caller")
	}
}

func TestLockReportsDeviceFailure(t *testing.T) {
	url, st := newWriteTestServer(t)

	conn, deviceID := registerCapableV1(t, url)
	scanTag(t, conn, st, deviceID, scannedUID)

	msg := sampleLock("l2")
	done := goLock(st.Router, msg)

	req := readDeviceWriteRequest(t, conn)
	requestID, _ := req.Payload["requestID"].(string)

	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type: remotenfc.WSTypeDeviceWriteResponse,
		Payload: map[string]any{
			"requestID": requestID,
			"success":   false,
			"error":     "tag does not support locking",
			"errorCode": string(protocol.ErrCodeNotSupported),
		},
	}); err != nil {
		t.Fatalf("write response: %v", err)
	}

	select {
	case resp := <-done:
		if resp.Success {
			t.Error("expected the lock to be reported as failed")
		}
		if resp.Error != "tag does not support locking" {
			t.Errorf("error = %q, want the device's message", resp.Error)
		}
		if resp.ErrorCode != protocol.ErrCodeNotSupported {
			t.Errorf("errorCode = %q, want %q", resp.ErrorCode, protocol.ErrCodeNotSupported)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no lock response reached the caller")
	}
}

// A device that vanishes mid-lock must release the waiter immediately.
func TestLockFailsFastWhenDeviceDisconnects(t *testing.T) {
	url, st := newWriteTestServer(t)

	conn, deviceID := registerCapableV1(t, url)
	scanTag(t, conn, st, deviceID, scannedUID)

	msg := sampleLock("l3")
	done := goLock(st.Router, msg)

	readDeviceWriteRequest(t, conn)
	_ = conn.Close()

	select {
	case resp := <-done:
		if resp.Success {
			t.Error("expected failure after the device disconnected")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waiter was not released when the device disconnected")
	}
}

// With no device holding a tag and no hardware reader, the lock is refused
// rather than being applied to something else.
func TestLockWithoutActiveTagIsRefused(t *testing.T) {
	url, st := newWriteTestServer(t)

	registerCapableV1(t, url)

	msg := sampleLock("l4")
	done := goLock(st.Router, msg)

	select {
	case resp := <-done:
		if resp.Success {
			t.Error("expected refusal when no tag is present")
		}
		if resp.ErrorCode != protocol.ErrCodeNoCard {
			t.Errorf("errorCode = %q, want %q", resp.ErrorCode, protocol.ErrCodeNoCard)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no lock response reached the caller")
	}
}

// Read-only mode gates the device route, not just the hardware one. The reader
// enforces it inside its own write path, which a request routed to a phone
// never reaches.
func TestLockViaDeviceRefusedInReadOnlyMode(t *testing.T) {
	url, st := newModedTestServer(t, nfc.ModeReadOnly)

	conn, deviceID := registerCapableV1(t, url)
	scanTag(t, conn, st, deviceID, scannedUID)

	msg := sampleLock("l5")
	done := goLock(st.Router, msg)

	select {
	case resp := <-done:
		if resp.Success {
			t.Fatal("read-only mode allowed a lock")
		}
		if resp.ErrorCode != protocol.ErrCodeReadOnly {
			t.Errorf("errorCode = %q, want %q", resp.ErrorCode, protocol.ErrCodeReadOnly)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no lock response reached the caller")
	}

	assertDeviceGotNothing(t, conn)
}

func TestWriteViaDeviceRefusedInReadOnlyMode(t *testing.T) {
	url, st := newModedTestServer(t, nfc.ModeReadOnly)

	conn, deviceID := registerCapableV1(t, url)
	scanTag(t, conn, st, deviceID, scannedUID)

	msg := sampleWrite("w5")
	done := goWrite(st.Router, msg)

	select {
	case resp := <-done:
		if resp.Success {
			t.Fatal("read-only mode allowed a write")
		}
		if resp.ErrorCode != protocol.ErrCodeReadOnly {
			t.Errorf("errorCode = %q, want %q", resp.ErrorCode, protocol.ErrCodeReadOnly)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no write response reached the caller")
	}

	assertDeviceGotNothing(t, conn)
}

// Read/write mode must not refuse on mode grounds; the request should reach the
// device instead.
func TestLockViaDeviceAllowedInReadWriteMode(t *testing.T) {
	url, st := newModedTestServer(t, nfc.ModeReadWrite)

	conn, deviceID := registerCapableV1(t, url)
	scanTag(t, conn, st, deviceID, scannedUID)

	msg := sampleLock("l6")
	_ = goLock(st.Router, msg)

	req := readDeviceWriteRequest(t, conn)
	if lock, _ := req.Payload["lock"].(bool); !lock {
		t.Error("read/write mode did not forward the lock to the device")
	}
}

// assertDeviceGotNothing checks the refusal never reached the wire. A mode
// refusal the agent forwards anyway is not a refusal.
func assertDeviceGotNothing(t *testing.T, conn *websocket.Conn) {
	t.Helper()

	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	var msg protocol.WebSocketRequest
	err := conn.ReadJSON(&msg)
	if err == nil {
		t.Fatalf("device received %s despite the refusal", msg.Type)
	}
	if !isTimeout(err) {
		t.Fatalf("unexpected read error: %v", err)
	}
}

func isTimeout(err error) bool {
	type timeout interface{ Timeout() bool }
	te, ok := err.(timeout)
	return ok && te.Timeout()
}

// scanLockableTag reports a tag declaring every modifying capability, so a test
// can see which of them survive the agent's mode.
func scanLockableTag(t *testing.T, conn *websocket.Conn, st *stack, deviceID string) nfc.Tag {
	t.Helper()

	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type: remotenfc.WSTypeTagScanned,
		Payload: map[string]any{
			"deviceID":   deviceID,
			"uid":        scannedUID,
			"technology": "ISO14443A",
			"type":       "NTAG215",
			"capabilities": map[string]any{
				"canRead":       true,
				"canWrite":      true,
				"canLock":       true,
				"canTransceive": true,
				"supportsNdef":  true,
			},
		},
	}); err != nil {
		t.Fatalf("write tagScanned: %v", err)
	}

	return awaitTag(t, st)
}

// A tag must not advertise an operation the agent would then refuse, so
// read-only mode withdraws the modifying capabilities rather than leaving a
// client to discover the refusal by attempting one.
func TestRemoteTagCapabilitiesWithdrawnInReadOnlyMode(t *testing.T) {
	url, st := newModedTestServer(t, nfc.ModeReadOnly)

	conn, deviceID := registerCapableV1(t, url)
	caps := nfc.GetTagCapabilities(scanLockableTag(t, conn, st, deviceID))

	if caps.CanWrite {
		t.Error("tag advertised canWrite in read-only mode")
	}
	if caps.CanLock {
		t.Error("tag advertised canLock in read-only mode")
	}
	if caps.CanTransceive {
		t.Error("tag advertised canTransceive in read-only mode")
	}
	if !caps.CanRead {
		t.Error("read-only mode withdrew canRead")
	}
}

func TestRemoteTagCapabilitiesKeptInReadWriteMode(t *testing.T) {
	url, st := newModedTestServer(t, nfc.ModeReadWrite)

	conn, deviceID := registerCapableV1(t, url)
	caps := nfc.GetTagCapabilities(scanLockableTag(t, conn, st, deviceID))

	if !caps.CanWrite {
		t.Error("tag did not advertise canWrite in read/write mode")
	}
	if !caps.CanLock {
		t.Error("tag did not advertise canLock in read/write mode")
	}
	if !caps.CanTransceive {
		t.Error("tag did not advertise canTransceive in read/write mode")
	}
}
