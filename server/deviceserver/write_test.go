package deviceserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/deviceserver"
	"github.com/gorilla/websocket"
)

// newWriteTestServer exposes the bridge alongside the device endpoint so a test
// can submit a write the way the client server would.
func newWriteTestServer(t *testing.T) (string, *server.ServerBridge) {
	t.Helper()

	bridge := server.NewServerBridge()
	deviceMgr := remotenfc.NewManager(30 * time.Second)

	dev := deviceserver.New(deviceserver.Config{DeviceManager: deviceMgr}, bridge)

	ctx, cancel := context.WithCancel(context.Background())
	dev.StartBackground(ctx)

	ts := httptest.NewServer(http.HandlerFunc(dev.ServeWS))

	t.Cleanup(func() {
		ts.Close()
		cancel()
		bridge.Close()
		deviceMgr.Close()
	})

	return "ws" + strings.TrimPrefix(ts.URL, "http") + "?mode=device", bridge
}

// scanTag makes the device the holder of the active tag, which is what a write
// request routes on. It waits for the tag to reach the bridge, so a write
// submitted next cannot overtake the scan it depends on.
func scanTag(t *testing.T, conn *websocket.Conn, bridge *server.ServerBridge, deviceID, uid string) {
	t.Helper()

	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type: protocol.WSTypeTagScanned,
		Payload: map[string]any{
			"deviceID":   deviceID,
			"uid":        uid,
			"technology": "ISO14443A",
			"type":       "NTAG215",
		},
	}); err != nil {
		t.Fatalf("write tagScanned: %v", err)
	}

	select {
	case <-bridge.TagData:
	case <-time.After(3 * time.Second):
		t.Fatal("scanned tag never reached the bridge")
	}
}

func sampleWrite(requestID string) server.WriteRequestMessage {
	return server.WriteRequestMessage{
		RequestID: requestID,
		Request: server.WriteRequest{
			Records: []server.WriteRecord{{Type: "text", Content: "Hello, NFC!"}},
		},
		IdempotencyKey: "key-" + requestID,
		ResponseCh:     make(chan server.WriteResponseMessage, 1),
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
		if msg.Type == protocol.WSTypeDeviceWriteRequest {
			return msg
		}
	}
}

func TestWriteRoutesToDeviceHoldingTag(t *testing.T) {
	url, bridge := newWriteTestServer(t)

	conn, deviceID := registerV1(t, url)
	scanTag(t, conn, bridge, deviceID, "04:A1:B2:C3")

	msg := sampleWrite("w1")
	go func() { bridge.WriteRequest <- msg }()

	req := readDeviceWriteRequest(t, conn)

	if got, _ := req.Payload["tagUID"].(string); got != "04:A1:B2:C3" {
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
			t.Errorf("write failed: %s", resp.Error)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no write response reached the bridge")
	}
}

func TestWriteReportsDeviceFailure(t *testing.T) {
	url, bridge := newWriteTestServer(t)

	conn, deviceID := registerV1(t, url)
	scanTag(t, conn, bridge, deviceID, "04:A1:B2:C3")

	msg := sampleWrite("w2")
	go func() { bridge.WriteRequest <- msg }()

	req := readDeviceWriteRequest(t, conn)
	requestID, _ := req.Payload["requestID"].(string)

	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type: protocol.WSTypeDeviceWriteResponse,
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
	case resp := <-msg.ResponseCh:
		if resp.Success {
			t.Error("expected the write to be reported as failed")
		}
		if resp.Error != "tag is read-only" {
			t.Errorf("error = %q, want the device's message", resp.Error)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no write response reached the bridge")
	}
}

// A device that vanishes mid-write must release the waiter immediately rather
// than making it sit out the full timeout.
func TestWriteFailsFastWhenDeviceDisconnects(t *testing.T) {
	url, bridge := newWriteTestServer(t)

	conn, deviceID := registerV1(t, url)
	scanTag(t, conn, bridge, deviceID, "04:A1:B2:C3")

	msg := sampleWrite("w3")
	go func() { bridge.WriteRequest <- msg }()

	readDeviceWriteRequest(t, conn)
	conn.Close()

	select {
	case resp := <-msg.ResponseCh:
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
	url, bridge := newWriteTestServer(t)

	// Register a device but never scan, so nothing holds a tag.
	registerV1(t, url)

	msg := sampleWrite("w4")
	go func() { bridge.WriteRequest <- msg }()

	select {
	case resp := <-msg.ResponseCh:
		if resp.Success {
			t.Error("expected refusal when no tag is present")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("write request was neither routed nor refused")
	}
}

// A tag leaving the field clears the routing target.
func TestTagRemovalClearsWriteTarget(t *testing.T) {
	url, bridge := newWriteTestServer(t)

	conn, deviceID := registerV1(t, url)
	scanTag(t, conn, bridge, deviceID, "04:A1:B2:C3")

	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type: protocol.WSTypeTagRemoved,
		Payload: map[string]any{
			"deviceID":  deviceID,
			"uid":       "04:A1:B2:C3",
			"removedAt": time.Now().Format(time.RFC3339),
		},
	}); err != nil {
		t.Fatalf("write tagRemoved: %v", err)
	}

	// Give the removal time to land before submitting the write.
	time.Sleep(200 * time.Millisecond)

	msg := sampleWrite("w5")
	go func() { bridge.WriteRequest <- msg }()

	select {
	case resp := <-msg.ResponseCh:
		if resp.Success {
			t.Error("expected refusal after the tag left the field")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("write request was neither routed nor refused")
	}
}
