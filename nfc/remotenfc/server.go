package remotenfc

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/wsconn"
	"github.com/gorilla/websocket"
)

// ServerOptions configures the device endpoint. Everything here is agent policy
// that the driver honours but does not decide. Authentication is not among
// them: the caller wraps the handler.
type ServerOptions struct {
	// CheckOrigin admits or rejects an upgrade by Origin. Nil admits any.
	CheckOrigin func(r *http.Request) bool

	// AllowTagModification reports whether writes, locks and raw exchanges are
	// currently permitted. Nil permits them.
	AllowTagModification func() bool

	// PublicKeyPin is reported at registration so a device can recognise this
	// agent on later connections. Nil, or one returning empty, omits it.
	//
	// A function because it is read per registration: the pin follows the
	// certificate, which can be reissued while the endpoint stays up.
	PublicKeyPin func() string

	// Authenticate admits or rejects a device before the upgrade, writing its
	// own response when it rejects. This driver speaks the device protocol and
	// has no idea what a credential is here, so the check is supplied; see
	// server.DeviceAuth for the agent's.
	//
	// Required. A nil Authenticate is refused at Handler unless
	// AllowUnauthenticated says otherwise, because the endpoint is otherwise
	// open to anyone who can reach the port.
	Authenticate func(w http.ResponseWriter, r *http.Request) bool

	// AllowUnauthenticated serves devices with no check at all. For a driver
	// reached only over a trusted transport, and for tests.
	AllowUnauthenticated bool
}

// Handler returns the HTTP handler serving device connections.
//
// It also stores opts on the manager, since the capabilities a tag reports
// depend on them. Call it once, before serving.
//
// Without opts.Authenticate, and without AllowUnauthenticated to say that is
// deliberate, the returned handler refuses every connection rather than serving
// an open device endpoint. Forgetting the check is otherwise silent: the
// upgrade succeeds and the device registers.
func (m *Manager) Handler(opts ServerOptions) http.Handler {
	m.mu.Lock()
	m.publicKeyPin = opts.PublicKeyPin
	m.allowTagModification = opts.AllowTagModification
	m.mu.Unlock()

	if opts.Authenticate == nil && !opts.AllowUnauthenticated {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.Printf("[device] Connection from %s refused: no authenticator configured", r.RemoteAddr)
			http.Error(w, "device endpoint is not configured for authentication", http.StatusServiceUnavailable)
		})
	}

	return &deviceEndpoint{
		manager:      m,
		authenticate: opts.Authenticate,
		upgrader: websocket.Upgrader{
			CheckOrigin:  opts.CheckOrigin,
			Subprotocols: DeviceSubprotocols,
		},
	}
}

// IsDeviceConnection reports whether a request is a device asking to connect
// rather than a client.
func IsDeviceConnection(r *http.Request) bool {
	return r.Header.Get("X-Device-Mode") == "true" || r.URL.Query().Get("mode") == "device"
}

type deviceEndpoint struct {
	manager      *Manager
	authenticate func(w http.ResponseWriter, r *http.Request) bool
	upgrader     websocket.Upgrader
}

func (e *deviceEndpoint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if e.authenticate != nil && !e.authenticate(w, r) {
		return
	}

	wsConn, err := e.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[device] WebSocket upgrade error: %v", err)
		return
	}

	conn := wsconn.NewSafeConn(wsConn)
	log.Printf("[device] WebSocket connected from %s (subprotocol=%q)", r.RemoteAddr, wsConn.Subprotocol())

	e.manager.serveSession(conn)
}

// serveSession registers the device on its first frame and then serves it until
// the connection ends.
func (m *Manager) serveSession(conn *wsconn.SafeConn) {
	var deviceID string
	reason := DisconnectDropped

	defer func() {
		_ = conn.Close()
		if deviceID != "" {
			m.endSession(deviceID, reason)
		}
		log.Printf("[device] WebSocket disconnected: %s (%s)", deviceID, reason)
	}()

	deviceID, ok := m.awaitRegistration(conn)
	if !ok {
		return
	}

	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			// A close handshake means the device meant to leave. Anything else,
			// such as a TCP reset or a dead radio, is a drop.
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				reason = DisconnectClosed
			}
			return
		}
		if messageType != websocket.TextMessage {
			continue
		}

		var req protocol.WebSocketRequest
		if err := json.Unmarshal(message, &req); err != nil {
			log.Printf("[device] Failed to parse message: %v", err)
			m.sendError(conn, "", protocol.ErrCodeParse, "Invalid message format")
			continue
		}

		var handlerErr error
		switch req.Type {
		case WSTypeTagScanned:
			handlerErr = m.handleTagScanned(conn, deviceID, req)
		case WSTypeTagRemoved:
			handlerErr = m.handleTagRemoved(conn, deviceID, req)
		case WSTypeDeviceHeartbeat:
			handlerErr = m.handleDeviceHeartbeat(deviceID, req)
		case WSTypeGoodbye:
			reason = DisconnectGoodbye
			m.handleGoodbye(conn, deviceID, req)
			return
		case WSTypeDeviceWriteResponse, WSTypeDeviceTransceiveResponse:
			handlerErr = m.handleDeviceResponse(deviceID, req)
		default:
			log.Printf("[device] Unknown message type: %s", req.Type)
			m.sendError(conn, req.ID, protocol.ErrCodeUnknownType, fmt.Sprintf("Unknown message type: %s", req.Type))
			continue
		}

		if handlerErr != nil {
			log.Printf("[device] Handler error for message type '%s': %v", req.Type, handlerErr)
		}
	}
}

// awaitRegistration reads the first frame and registers the device it describes.
//
// The frame's type selects the dialect: hello is v1, registerDevice is the
// legacy v0 exchange. The negotiated subprotocol is advisory, so a device that
// offered none is still served whichever it actually speaks.
func (m *Manager) awaitRegistration(conn *wsconn.SafeConn) (string, bool) {
	messageType, message, err := conn.ReadMessage()
	if err != nil {
		log.Printf("[device] Failed to read registration message: %v", err)
		m.sendError(conn, "", protocol.ErrCodeReadError, "Failed to read message")
		return "", false
	}

	if messageType != websocket.TextMessage {
		log.Printf("[device] Expected text message, got type %d", messageType)
		m.sendError(conn, "", protocol.ErrCodeInvalidMessageType, "Expected text message")
		return "", false
	}

	var req protocol.WebSocketRequest
	if err := json.Unmarshal(message, &req); err != nil {
		log.Printf("[device] Failed to parse registration message: %v", err)
		m.sendError(conn, "", protocol.ErrCodeParse, "Invalid message format")
		return "", false
	}

	switch req.Type {
	case WSTypeHello:
		err = m.handleHello(conn, req)
	case WSTypeRegisterDevice:
		err = m.handleRegisterDevice(conn, req)
	default:
		log.Printf("[device] Expected '%s' or '%s', got '%s'", WSTypeHello, WSTypeRegisterDevice, req.Type)
		m.sendError(conn, req.ID, protocol.ErrCodeInvalidMessageType, fmt.Sprintf("Expected '%s' or '%s' message", WSTypeHello, WSTypeRegisterDevice))
		return "", false
	}
	if err != nil {
		log.Printf("[device] Registration failed: %v", err)
		return "", false
	}

	deviceID := m.deviceIDForConn(conn)
	if deviceID == "" {
		log.Printf("[device] Failed to get deviceID after registration")
		m.sendError(conn, req.ID, protocol.ErrCodeRegistrationFailed, "Failed to get device ID")
		return "", false
	}
	return deviceID, true
}

// handleHello processes a v1 hello, which carries the protocol version
// alongside the registration payload.
func (m *Manager) handleHello(conn *wsconn.SafeConn, req protocol.WebSocketRequest) error {
	var hello HelloRequest
	if err := decodePayload(req.Payload, &hello); err != nil {
		m.sendError(conn, req.ID, protocol.ErrCodeInvalidPayload, "Invalid hello request format")
		return fmt.Errorf("failed to parse hello request: %w", err)
	}

	version := NegotiateDeviceVersion(hello.ProtocolVersion)

	device, regResp, err := m.registerSession(conn, req.ID, hello.DeviceRegistrationRequest, version)
	if err != nil {
		return err
	}

	return m.sendRegistration(conn, device, protocol.WebSocketResponse{
		ID:      req.ID,
		Type:    WSTypeHelloResponse,
		Success: true,
		Payload: HelloResponse{
			ProtocolVersion:            version,
			DeviceRegistrationResponse: regResp,
		},
	})
}

// handleRegisterDevice processes a legacy v0 registration.
func (m *Manager) handleRegisterDevice(conn *wsconn.SafeConn, req protocol.WebSocketRequest) error {
	var regReq DeviceRegistrationRequest
	if err := decodePayload(req.Payload, &regReq); err != nil {
		m.sendError(conn, req.ID, protocol.ErrCodeInvalidPayload, "Invalid registration request format")
		return fmt.Errorf("failed to parse registration request: %w", err)
	}

	device, regResp, err := m.registerSession(conn, req.ID, regReq, DeviceProtocolV0)
	if err != nil {
		return err
	}

	return m.sendRegistration(conn, device, protocol.WebSocketResponse{
		ID:      req.ID,
		Type:    WSTypeRegisterDeviceResponse,
		Success: true,
		Payload: regResp,
	})
}

// registerSession registers the device and binds it to its connection, leaving
// the caller to answer in whichever dialect it is speaking.
func (m *Manager) registerSession(conn *wsconn.SafeConn, reqID string, regReq DeviceRegistrationRequest, version int) (*Device, DeviceRegistrationResponse, error) {
	if regReq.DeviceName == "" {
		m.sendError(conn, reqID, protocol.ErrCodeInvalidRequest, "Device name is required")
		return nil, DeviceRegistrationResponse{}, fmt.Errorf("device name is required")
	}

	device, err := m.RegisterDevice(DeviceRegistrationRequest{
		DeviceName:      regReq.DeviceName,
		Platform:        regReq.Platform,
		AppVersion:      regReq.AppVersion,
		ProtocolVersion: version,
		Capabilities:    regReq.Capabilities,
		Metadata:        regReq.Metadata,
	})
	if err != nil {
		m.sendError(conn, reqID, protocol.ErrCodeRegistrationFailed, err.Error())
		return nil, DeviceRegistrationResponse{}, fmt.Errorf("failed to register device: %w", err)
	}

	m.addSession(device.DeviceID(), conn)

	m.mu.RLock()
	publicKeyPin := m.publicKeyPin
	m.mu.RUnlock()

	// Called outside the lock: it reaches back into whoever supplied it.
	var pin string
	if publicKeyPin != nil {
		pin = publicKeyPin()
	}

	return device, DeviceRegistrationResponse{
		DeviceID: device.DeviceID(),
		ServerInfo: ServerInfo{
			Version:      "1.0.0",
			SupportedNFC: []string{"mifare", "desfire", "type4", "ultralight"},
			PublicKeyPin: pin,
		},
	}, nil
}

func (m *Manager) sendRegistration(conn *wsconn.SafeConn, device *Device, resp protocol.WebSocketResponse) error {
	deviceID := device.DeviceID()

	if err := conn.WriteJSON(resp); err != nil {
		m.removeSession(deviceID)
		if unregErr := m.UnregisterDevice(deviceID); unregErr != nil {
			log.Printf("[device] Failed to unregister %s after registration send error: %v", deviceID, unregErr)
		}
		return fmt.Errorf("failed to send registration response: %w", err)
	}

	log.Printf("[device] Device registered: %s (%s, protocol v%d)", device.String(), deviceID, device.ProtocolVersion())
	return nil
}

func (m *Manager) handleTagScanned(conn *wsconn.SafeConn, deviceID string, req protocol.WebSocketRequest) error {
	var tagData TagData
	if err := decodePayload(req.Payload, &tagData); err != nil {
		log.Printf("[device] Failed to parse tag data: %v", err)
		m.sendError(conn, req.ID, protocol.ErrCodeInvalidPayload, "Invalid tag data format")
		return err
	}

	if tagData.DeviceID != "" && tagData.DeviceID != deviceID {
		m.sendError(conn, req.ID, protocol.ErrCodeInvalidDevice, "Device ID mismatch")
		return fmt.Errorf("device ID mismatch: expected %s, got %s", deviceID, tagData.DeviceID)
	}
	tagData.DeviceID = deviceID

	// SendTagData records the tag before publishing it, so a client reacting to
	// the broadcast finds the device already holding it.
	if err := m.SendTagData(deviceID, tagData); err != nil {
		log.Printf("[device] Failed to send tag data: %v", err)
		m.sendTagError(conn, req.ID, err)
		return err
	}

	log.Printf("[device] Tag scanned: device=%s, UID=%s, Type=%s", deviceID, tagData.UID, tagData.Type)
	return nil
}

func (m *Manager) handleTagRemoved(conn *wsconn.SafeConn, deviceID string, req protocol.WebSocketRequest) error {
	var removed TagRemovedData
	if err := decodePayload(req.Payload, &removed); err != nil {
		log.Printf("[device] Failed to parse tag removed data: %v", err)
		m.sendError(conn, req.ID, protocol.ErrCodeInvalidPayload, "Invalid tag removed data format")
		return err
	}

	if removed.DeviceID != "" && removed.DeviceID != deviceID {
		m.sendError(conn, req.ID, protocol.ErrCodeInvalidDevice, "Device ID mismatch")
		return fmt.Errorf("device ID mismatch")
	}
	removed.DeviceID = deviceID

	m.clearActiveTag(deviceID, removed.UID)

	if err := m.SendTagRemoved(deviceID, removed); err != nil {
		log.Printf("[device] Failed to send tag removed: %v", err)
		m.sendTagError(conn, req.ID, err)
		return err
	}
	return nil
}

func (m *Manager) handleDeviceHeartbeat(deviceID string, req protocol.WebSocketRequest) error {
	var heartbeat DeviceHeartbeat
	if err := decodePayload(req.Payload, &heartbeat); err != nil {
		return err
	}

	if heartbeat.DeviceID != "" && heartbeat.DeviceID != deviceID {
		return fmt.Errorf("device ID mismatch")
	}

	if err := m.UpdateHeartbeat(deviceID); err != nil {
		return fmt.Errorf("failed to record heartbeat: %w", err)
	}
	return nil
}

// handleGoodbye acknowledges an announced departure with a close handshake, so
// the device knows the agent heard it.
func (m *Manager) handleGoodbye(conn *wsconn.SafeConn, deviceID string, req protocol.WebSocketRequest) {
	var goodbye GoodbyeRequest
	if err := decodePayload(req.Payload, &goodbye); err != nil {
		log.Printf("[device] Malformed goodbye from %s: %v", deviceID, err)
	}

	if goodbye.Reason != "" {
		log.Printf("[device] Device %s said goodbye: %s", deviceID, goodbye.Reason)
	}

	closeMsg := websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
	if err := conn.WriteMessage(websocket.CloseMessage, closeMsg); err != nil {
		log.Printf("[device] Failed to acknowledge goodbye from %s: %v", deviceID, err)
	}
}

// endSession tears down everything a departed device owned. It is the only path
// that unregisters a connected device, so a session and a registration cannot
// outlive one another.
func (m *Manager) endSession(deviceID string, reason DisconnectReason) {
	m.removeSession(deviceID)
	m.failPendingRequests(deviceID)
	m.clearActiveTag(deviceID, "")

	if err := m.UnregisterDevice(deviceID); err != nil {
		log.Printf("[device] Failed to unregister %s on disconnect: %v", deviceID, err)
	}

	if reason.Expected() {
		log.Printf("[device] Device disconnected: %s (%s)", deviceID, reason)
	} else {
		log.Printf("[device] Device lost: %s (no close handshake)", deviceID)
	}
}

// decodePayload re-marshals a decoded payload map into a concrete type.
func decodePayload(payload map[string]any, target any) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}
	return json.Unmarshal(payloadBytes, target)
}

func (m *Manager) sendError(conn *wsconn.SafeConn, requestID string, code protocol.ErrorCode, message string) {
	m.sendErrorPayload(conn, requestID, protocol.NewErrorPayload(code), message)
}

// sendTagError reports a failure carrying tag data. A typed NFCError keeps its
// code, operation and tag; anything else is a transient delivery failure.
func (m *Manager) sendTagError(conn *wsconn.SafeConn, requestID string, err error) {
	payload := protocol.ErrorPayloadFor(err)
	if payload.Code == protocol.ErrCodeUnknownError {
		payload = protocol.NewErrorPayload(protocol.ErrCodeTagSendFailed)
	}
	m.sendErrorPayload(conn, requestID, payload, err.Error())
}

func (m *Manager) sendErrorPayload(conn *wsconn.SafeConn, requestID string, payload protocol.ErrorPayload, message string) {
	response := protocol.WebSocketResponse{
		ID:      requestID,
		Type:    protocol.WSTypeError,
		Success: false,
		Error:   message,
		Payload: payload,
	}

	if err := conn.WriteJSON(response); err != nil {
		log.Printf("[device] Failed to send error response: %v", err)
	}
}
