package remotenfc

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/gorilla/websocket"
)

func newDeviceTestServer(t *testing.T) string {
	t.Helper()

	// Exercising the protocol, not the credential.
	_, url := serveManager(t, DeviceTimeout)
	return url
}

// servePinnedManager is the endpoint of an agent serving a certificate, which
// is what it reports the pin of.
func servePinnedManager(t *testing.T, pin string) string {
	t.Helper()

	m := NewManager(DeviceTimeout)
	ts := httptest.NewServer(m.Handler(ServerOptions{
		AllowUnauthenticated: true,
		PublicKeyPin:         func() string { return pin },
	}))

	t.Cleanup(func() {
		ts.Close()
		m.Close()
	})

	return "ws" + strings.TrimPrefix(ts.URL, "http") + "?mode=device"
}

func dialOffering(t *testing.T, url string, subprotocols []string) (*websocket.Conn, string) {
	t.Helper()

	dialer := websocket.Dialer{Subprotocols: subprotocols}
	conn, resp, err := dialer.Dial(url, nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("dial failed: %v (status %d)", err, status)
	}
	t.Cleanup(func() { _ = conn.Close() })

	return conn, conn.Subprotocol()
}

func readResponse(t *testing.T, conn *websocket.Conn) (protocol.WebSocketResponse, map[string]any) {
	t.Helper()

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var out protocol.WebSocketResponse
	if err := conn.ReadJSON(&out); err != nil {
		t.Fatalf("read response: %v", err)
	}

	payload, ok := out.Payload.(map[string]any)
	if !ok {
		t.Fatalf("unexpected payload shape: %#v", out.Payload)
	}
	return out, payload
}

func TestHelloHandshakeNegotiatesV1(t *testing.T) {
	url := newDeviceTestServer(t)

	conn, negotiated := dialOffering(t, url, []string{SubprotocolDeviceV1})
	if negotiated != SubprotocolDeviceV1 {
		t.Errorf("negotiated subprotocol = %q, want %q", negotiated, SubprotocolDeviceV1)
	}

	if err := conn.WriteJSON(protocol.WebSocketRequest{
		ID:   "h1",
		Type: WSTypeHello,
		Payload: map[string]any{
			"protocolVersion": DeviceProtocolV1,
			"deviceName":      "Test Device",
			"platform":        "android",
		},
	}); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	out, payload := readResponse(t, conn)

	if out.Type != WSTypeHelloResponse {
		t.Errorf("response type = %q, want %q", out.Type, WSTypeHelloResponse)
	}
	if !out.Success {
		t.Errorf("response success = false, error = %q", out.Error)
	}
	if got := payload["protocolVersion"]; got != float64(DeviceProtocolV1) {
		t.Errorf("protocolVersion = %v, want %d", got, DeviceProtocolV1)
	}
	if id, _ := payload["deviceID"].(string); id == "" {
		t.Error("helloResponse carried no deviceID")
	}
}

// A device declaring a version we do not implement is answered at our maximum
// rather than refused.
func TestHelloClampsFutureVersion(t *testing.T) {
	url := newDeviceTestServer(t)

	conn, _ := dialOffering(t, url, []string{SubprotocolDeviceV1})
	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type: WSTypeHello,
		Payload: map[string]any{
			"protocolVersion": 99,
			"deviceName":      "Future Device",
			"platform":        "android",
		},
	}); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	_, payload := readResponse(t, conn)
	if got := payload["protocolVersion"]; got != float64(DeviceProtocolMax) {
		t.Errorf("protocolVersion = %v, want %d", got, DeviceProtocolMax)
	}
}

// The first frame decides the dialect, so hello works even from a device that
// never offered a subprotocol.
func TestHelloWithoutSubprotocolOffer(t *testing.T) {
	url := newDeviceTestServer(t)

	conn, negotiated := dialOffering(t, url, nil)
	if negotiated != "" {
		t.Errorf("negotiated subprotocol = %q, want empty", negotiated)
	}

	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type:    WSTypeHello,
		Payload: map[string]any{"deviceName": "Bare Device", "platform": "ios"},
	}); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	out, payload := readResponse(t, conn)
	if out.Type != WSTypeHelloResponse {
		t.Errorf("response type = %q, want %q", out.Type, WSTypeHelloResponse)
	}
	if got := payload["protocolVersion"]; got != float64(DeviceProtocolV1) {
		t.Errorf("protocolVersion = %v, want %d", got, DeviceProtocolV1)
	}
}

// Devices predating versioning must see a byte-identical exchange: no
// subprotocol, registerDeviceResponse, and no protocolVersion field.
func TestLegacyRegisterDeviceUnchanged(t *testing.T) {
	url := newDeviceTestServer(t)

	conn, negotiated := dialOffering(t, url, nil)
	if negotiated != "" {
		t.Errorf("negotiated subprotocol = %q, want empty", negotiated)
	}

	if err := conn.WriteJSON(protocol.WebSocketRequest{
		ID:   "r1",
		Type: WSTypeRegisterDevice,
		Payload: map[string]any{
			"deviceName": "Legacy Device",
			"platform":   "ios",
		},
	}); err != nil {
		t.Fatalf("write registerDevice: %v", err)
	}

	out, payload := readResponse(t, conn)

	if out.Type != WSTypeRegisterDeviceResponse {
		t.Errorf("response type = %q, want %q", out.Type, WSTypeRegisterDeviceResponse)
	}
	if out.ID != "r1" {
		t.Errorf("response ID = %q, want r1", out.ID)
	}
	if id, _ := payload["deviceID"].(string); id == "" {
		t.Error("registerDeviceResponse carried no deviceID")
	}
	if _, ok := payload["protocolVersion"]; ok {
		t.Error("legacy response must not carry protocolVersion")
	}
}

func TestRegistrationRequiresDeviceName(t *testing.T) {
	url := newDeviceTestServer(t)

	conn, _ := dialOffering(t, url, []string{SubprotocolDeviceV1})
	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type:    WSTypeHello,
		Payload: map[string]any{"protocolVersion": 1},
	}); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	out, payload := readResponse(t, conn)
	if out.Success {
		t.Error("expected failure for missing device name")
	}
	if code, _ := payload["code"].(string); code != "INVALID_REQUEST" {
		t.Errorf("error code = %q, want INVALID_REQUEST", code)
	}
}

func TestFirstFrameMustBeHelloOrRegister(t *testing.T) {
	url := newDeviceTestServer(t)

	conn, _ := dialOffering(t, url, []string{SubprotocolDeviceV1})
	if err := conn.WriteJSON(protocol.WebSocketRequest{Type: "tagScanned"}); err != nil {
		t.Fatalf("write: %v", err)
	}

	out, payload := readResponse(t, conn)
	if out.Success {
		t.Error("expected failure for out-of-order first frame")
	}
	if code, _ := payload["code"].(string); code != "INVALID_MESSAGE_TYPE" {
		t.Errorf("error code = %q, want INVALID_MESSAGE_TYPE", code)
	}
}

// registerV1 completes a v1 handshake and returns the connection and device ID.
func registerV1(t *testing.T, url string) (*websocket.Conn, string) {
	t.Helper()

	conn, _ := dialOffering(t, url, []string{SubprotocolDeviceV1})
	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type: WSTypeHello,
		Payload: map[string]any{
			"protocolVersion": DeviceProtocolV1,
			"deviceName":      "Test Device",
			"platform":        "android",
		},
	}); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	_, payload := readResponse(t, conn)
	deviceID, _ := payload["deviceID"].(string)
	if deviceID == "" {
		t.Fatal("registration returned no deviceID")
	}
	return conn, deviceID
}

// A tag scan the agent cannot parse is a permanent failure. Reporting it as
// retryable invites a device to resend the same broken payload forever.
func TestMalformedTagScanIsNotRetryable(t *testing.T) {
	url := newDeviceTestServer(t)
	conn, deviceID := registerV1(t, url)

	if err := conn.WriteJSON(protocol.WebSocketRequest{
		ID:   "scan1",
		Type: WSTypeTagScanned,
		Payload: map[string]any{
			"deviceID":   deviceID,
			"uid":        "not a valid uid",
			"technology": "ISO14443A",
		},
	}); err != nil {
		t.Fatalf("write tagScanned: %v", err)
	}

	out, payload := readResponse(t, conn)

	if out.Success {
		t.Fatal("expected failure for an unparseable UID")
	}
	if code, _ := payload["code"].(string); code != string(protocol.ErrCodeInvalidData) {
		t.Errorf("code = %q, want INVALID_DATA", code)
	}
	if retryable, _ := payload["retryable"].(bool); retryable {
		t.Error("a malformed UID cannot be fixed by retrying")
	}
	if op, _ := payload["op"].(string); op != "ConvertTagData" {
		t.Errorf("op = %q, want ConvertTagData", op)
	}
}

// Errors raised by the bridge itself keep their original code strings so
// existing clients continue to switch on them.
func TestProtocolErrorsCarryRetryableFlag(t *testing.T) {
	url := newDeviceTestServer(t)
	conn, _ := registerV1(t, url)

	if err := conn.WriteJSON(protocol.WebSocketRequest{Type: "nonsense"}); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, payload := readResponse(t, conn)

	if code, _ := payload["code"].(string); code != "UNKNOWN_TYPE" {
		t.Errorf("code = %q, want UNKNOWN_TYPE", code)
	}
	if retryable, _ := payload["retryable"].(bool); retryable {
		t.Error("an unknown message type will not become known on retry")
	}
}

// A goodbye must be acknowledged with a close handshake so the device knows the
// agent heard it, and must unregister the device.
func TestGoodbyeIsAcknowledged(t *testing.T) {
	url := newDeviceTestServer(t)
	conn, deviceID := registerV1(t, url)

	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type:    WSTypeGoodbye,
		Payload: map[string]any{"deviceID": deviceID, "reason": "user stopped scanning"},
	}); err != nil {
		t.Fatalf("write goodbye: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _, err := conn.ReadMessage()
	if err == nil {
		t.Fatal("expected the connection to close after goodbye")
	}
	if !websocket.IsCloseError(err, websocket.CloseNormalClosure) {
		t.Errorf("close error = %v, want a normal closure", err)
	}
}

func TestDisconnectReasonExpected(t *testing.T) {
	if !DisconnectGoodbye.Expected() {
		t.Error("goodbye should be an expected departure")
	}
	if !DisconnectClosed.Expected() {
		t.Error("a clean close should be an expected departure")
	}
	if DisconnectDropped.Expected() {
		t.Error("a dropped connection is not an expected departure")
	}
}

// registerCapableV1 registers a device declaring write and transceive support.

// A device learns the agent's key pin during the handshake it already performs,
// so it can recognize the same agent later without a certificate authority.
func TestHelloReportsPublicKeyPin(t *testing.T) {
	const pin = "sha256/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA="
	url := servePinnedManager(t, pin)
	conn, _ := dialOffering(t, url, []string{SubprotocolDeviceV1})

	if err := conn.WriteJSON(protocol.WebSocketRequest{
		Type: WSTypeHello,
		Payload: map[string]any{
			"protocolVersion": DeviceProtocolV1,
			"deviceName":      "Pinning Device",
			"platform":        "android",
		},
	}); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	_, payload := readResponse(t, conn)
	info, ok := payload["serverInfo"].(map[string]any)
	if !ok {
		t.Fatalf("no serverInfo in response: %#v", payload)
	}
	if got, _ := info["publicKeyPin"].(string); got != pin {
		t.Errorf("publicKeyPin = %q, want %q", got, pin)
	}
}

// An agent with no certificate of its own has no pin to report, and must omit
// the field rather than send an empty one a device might record.
func TestHelloOmitsAbsentPin(t *testing.T) {
	url := newDeviceTestServer(t)
	conn, _ := registerV1(t, url)
	_ = conn

	// registerV1 already read the response; re-register on a fresh connection
	// to inspect the payload.
	conn2, _ := dialOffering(t, url, []string{SubprotocolDeviceV1})
	if err := conn2.WriteJSON(protocol.WebSocketRequest{
		Type: WSTypeHello,
		Payload: map[string]any{
			"protocolVersion": DeviceProtocolV1,
			"deviceName":      "Unpinned Device",
			"platform":        "ios",
		},
	}); err != nil {
		t.Fatalf("write hello: %v", err)
	}

	_, payload := readResponse(t, conn2)
	info, _ := payload["serverInfo"].(map[string]any)
	if _, present := info["publicKeyPin"]; present {
		t.Error("publicKeyPin present when the agent has no certificate of its own")
	}
}
