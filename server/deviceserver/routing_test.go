package deviceserver_test

import (
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/gorilla/websocket"
)

// capabilities runs one capabilities query through the bridge.
func capabilities(t *testing.T, bridge *server.ServerBridge, target string) server.CapabilitiesResponseMessage {
	t.Helper()

	msg := server.CapabilitiesRequestMessage{
		RequestID:    "cap-1",
		TargetDevice: target,
		ResponseCh:   make(chan server.CapabilitiesResponseMessage, 1),
	}
	go func() { bridge.CapabilitiesRequest <- msg }()

	select {
	case resp := <-msg.ResponseCh:
		return resp
	case <-time.After(3 * time.Second):
		t.Fatal("no capabilities response reached the bridge")
		return server.CapabilitiesResponseMessage{}
	}
}

// A capabilities query about a phone-held tag used to be answered by the
// hardware reader, which is a different tag.
func TestCapabilitiesAnswersFromTheDeviceHoldingTheTag(t *testing.T) {
	url, bridge := newWriteTestServer(t)

	conn, deviceID := registerCapableV1(t, url)
	scanLockableTag(t, conn, bridge, deviceID)

	resp := capabilities(t, bridge, "")
	if !resp.Success {
		t.Fatalf("capabilities failed: %s", resp.Error)
	}

	caps, ok := resp.Payload.(*nfc.TagCapabilities)
	if !ok {
		t.Fatalf("payload = %#v, want *nfc.TagCapabilities", resp.Payload)
	}
	if !caps.CanWrite || !caps.CanLock {
		t.Errorf("caps = %+v, want the capabilities the device declared", caps)
	}
	if caps.TagFamily != "NTAG215" {
		t.Errorf("tagFamily = %q, want the scanned tag's", caps.TagFamily)
	}
}

func TestCapabilitiesWithoutTagIsRefused(t *testing.T) {
	url, bridge := newWriteTestServer(t)
	registerCapableV1(t, url)

	resp := capabilities(t, bridge, "")
	if resp.Success {
		t.Error("expected refusal when no tag is present")
	}
	if resp.ErrorCode != protocol.ErrCodeNoCard {
		t.Errorf("errorCode = %q, want %q", resp.ErrorCode, protocol.ErrCodeNoCard)
	}
}

// Two phones can each hold a tag. A request naming one must reach that one,
// not whichever scanned most recently.
func TestWriteReachesTheNamedDevice(t *testing.T) {
	url, bridge := newWriteTestServer(t)

	first, firstID := registerCapableV1(t, url)
	second, secondID := registerCapableV1(t, url)

	scanTag(t, first, bridge, firstID, "04:AA:AA:AA")
	scanTag(t, second, bridge, secondID, "04:BB:BB:BB")

	// The second device scanned last, so an untargeted write would go there.
	msg := sampleWrite("t1")
	msg.TargetDevice = firstID
	go func() { bridge.WriteRequest <- msg }()

	req := readDeviceWriteRequest(t, first)
	if got, _ := req.Payload["tagUID"].(string); got != "04:AA:AA:AA" {
		t.Errorf("tagUID = %q, want the named device's tag", got)
	}
	if got, _ := req.Payload["deviceID"].(string); got != firstID {
		t.Errorf("deviceID = %q, want %q", got, firstID)
	}

	assertNoWriteRequest(t, second)
}

// Without a target the most recent scan still wins, which is what a client
// watching a single phone expects.
func TestUntargetedWriteGoesToTheLatestScan(t *testing.T) {
	url, bridge := newWriteTestServer(t)

	first, firstID := registerCapableV1(t, url)
	second, secondID := registerCapableV1(t, url)

	scanTag(t, first, bridge, firstID, "04:AA:AA:AA")
	scanTag(t, second, bridge, secondID, "04:BB:BB:BB")

	msg := sampleWrite("t2")
	go func() { bridge.WriteRequest <- msg }()

	req := readDeviceWriteRequest(t, second)
	if got, _ := req.Payload["tagUID"].(string); got != "04:BB:BB:BB" {
		t.Errorf("tagUID = %q, want the most recent scan", got)
	}

	assertNoWriteRequest(t, first)
}

// One device withdrawing its tag must not take the other's routing with it.
func TestTagRemovalLeavesTheOtherDeviceRoutable(t *testing.T) {
	url, bridge := newWriteTestServer(t)

	first, firstID := registerCapableV1(t, url)
	second, secondID := registerCapableV1(t, url)

	scanTag(t, first, bridge, firstID, "04:AA:AA:AA")
	scanTag(t, second, bridge, secondID, "04:BB:BB:BB")

	if err := second.WriteJSON(protocol.WebSocketRequest{
		Type: protocol.WSTypeTagRemoved,
		Payload: map[string]any{
			"deviceID": secondID,
			"uid":      "04:BB:BB:BB",
		},
	}); err != nil {
		t.Fatalf("write tagRemoved: %v", err)
	}

	select {
	case <-bridge.TagData:
	case <-time.After(3 * time.Second):
		t.Fatal("tag removal never reached the bridge")
	}

	// The remaining device is now the only one holding a tag, so an untargeted
	// write must find it.
	msg := sampleWrite("t3")
	go func() { bridge.WriteRequest <- msg }()

	req := readDeviceWriteRequest(t, first)
	if got, _ := req.Payload["tagUID"].(string); got != "04:AA:AA:AA" {
		t.Errorf("tagUID = %q, want the device still holding a tag", got)
	}
}

// The key identifies the logical write, so a client's retry can be recognised.
func TestWriteCarriesTheClientsIdempotencyKey(t *testing.T) {
	url, bridge := newWriteTestServer(t)

	conn, deviceID := registerCapableV1(t, url)
	scanTag(t, conn, bridge, deviceID, "04:A1:B2:C3")

	msg := sampleWrite("t4")
	msg.IdempotencyKey = "client-key"
	go func() { bridge.WriteRequest <- msg }()

	req := readDeviceWriteRequest(t, conn)
	if got, _ := req.Payload["idempotencyKey"].(string); got != "client-key" {
		t.Errorf("idempotencyKey = %q, want the client's", got)
	}
}

func TestLockCarriesAnIdempotencyKey(t *testing.T) {
	url, bridge := newWriteTestServer(t)

	conn, deviceID := registerCapableV1(t, url)
	scanTag(t, conn, bridge, deviceID, "04:A1:B2:C3")

	msg := sampleLock("t5")
	msg.IdempotencyKey = "lock-key"
	go func() { bridge.LockRequest <- msg }()

	req := readDeviceWriteRequest(t, conn)
	if got, _ := req.Payload["idempotencyKey"].(string); got != "lock-key" {
		t.Errorf("idempotencyKey = %q, want the client's", got)
	}
}

// assertNoWriteRequest checks a device was left alone.
func assertNoWriteRequest(t *testing.T, conn *websocket.Conn) {
	t.Helper()

	_ = conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
	var msg protocol.WebSocketRequest
	err := conn.ReadJSON(&msg)
	if err == nil {
		t.Fatalf("device received %s, but the request was for another device", msg.Type)
	}
	if !isTimeout(err) {
		t.Fatalf("unexpected read error: %v", err)
	}
}
