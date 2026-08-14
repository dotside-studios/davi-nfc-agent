package deviceserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// DeviceWriteTimeout bounds how long the agent waits for a device to report a
// write outcome. A tag is only in the field while the user holds it there, so
// waiting much longer than this reports a stale result.
const DeviceWriteTimeout = 20 * time.Second

// pendingWrite is an in-flight write awaiting a device's response.
type pendingWrite struct {
	deviceID string
	respCh   chan protocol.DeviceWriteResponse
}

// activeTag records which device currently holds a tag in its field, so a write
// request from a client knows where to go.
type activeTag struct {
	deviceID string
	uid      string
}

// DeviceHandler handles all device WebSocket connections and management.
type DeviceHandler struct {
	manager           *remotenfc.Manager
	bridge            *server.ServerBridge
	deviceSessions    map[string]*server.SafeConn // deviceID -> websocket conn
	deviceSessionsMux sync.RWMutex
	connToDeviceID    map[*server.SafeConn]string // reverse lookup: conn -> deviceID
	upgrader          websocket.Upgrader

	pendingWrites    map[string]pendingWrite // requestID -> waiter
	pendingWritesMux sync.Mutex

	active    activeTag
	activeMux sync.RWMutex
}

// NewDeviceHandler creates a new device handler. allowedOrigins extends
// the default same-origin policy on the device WebSocket upgrade.
func NewDeviceHandler(manager *remotenfc.Manager, bridge *server.ServerBridge, allowedOrigins []string) *DeviceHandler {
	return &DeviceHandler{
		manager:        manager,
		bridge:         bridge,
		deviceSessions: make(map[string]*server.SafeConn),
		connToDeviceID: make(map[*server.SafeConn]string),
		pendingWrites:  make(map[string]pendingWrite),
		upgrader: websocket.Upgrader{
			CheckOrigin:  server.CheckOrigin(allowedOrigins),
			Subprotocols: protocol.DeviceSubprotocols,
		},
	}
}

// Register registers the handler with the server.
func (h *DeviceHandler) Register(s *Server) {
	s.HandleWebSocket(IsDeviceConnection, func(w http.ResponseWriter, r *http.Request) bool {
		h.HandleWebSocket(w, r)
		return true
	})

	// Register lifecycle to broadcast device tag data
	s.StartLifecycle(func(ctx context.Context) {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case data := <-h.manager.Data():
					s.BroadcastTagData(data)
				}
			}
		}()
	})
}

// HandleWebSocket handles WebSocket connections from devices.
func (h *DeviceHandler) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	wsConn, err := h.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[device] WebSocket upgrade error: %v", err)
		return
	}

	// Wrap in SafeConn to prevent concurrent write panics
	conn := server.NewSafeConn(wsConn)

	log.Printf("[device] WebSocket connected from %s (subprotocol=%q)", r.RemoteAddr, wsConn.Subprotocol())

	var deviceID string
	reason := protocol.DisconnectDropped
	defer func() {
		conn.Close()
		if deviceID != "" {
			h.handleDeviceDisconnect(deviceID, reason)
		}
		log.Printf("[device] WebSocket disconnected: %s (%s)", deviceID, reason)
	}()

	// Wait for registerDevice message
	messageType, message, err := conn.ReadMessage()
	if err != nil {
		log.Printf("[device] Failed to read registration message: %v", err)
		h.sendError(conn, "", protocol.ErrCodeReadError, "Failed to read message")
		return
	}

	if messageType != websocket.TextMessage {
		log.Printf("[device] Expected text message, got type %d", messageType)
		h.sendError(conn, "", protocol.ErrCodeInvalidMessageType, "Expected text message")
		return
	}

	var wsRequest protocol.WebSocketRequest
	if err := json.Unmarshal(message, &wsRequest); err != nil {
		log.Printf("[device] Failed to parse registration message: %v", err)
		h.sendError(conn, "", protocol.ErrCodeParse, "Invalid message format")
		return
	}

	// The first frame's type selects the dialect: hello is v1, registerDevice is
	// the legacy v0 exchange. The negotiated subprotocol is advisory — a device
	// that never offered one still gets served whichever it actually speaks.
	switch wsRequest.Type {
	case protocol.WSTypeHello:
		err = h.handleHello(r.Context(), conn, wsRequest)
	case protocol.WSTypeRegisterDevice:
		err = h.handleRegisterDevice(r.Context(), conn, wsRequest)
	default:
		log.Printf("[device] Expected '%s' or '%s', got '%s'", protocol.WSTypeHello, protocol.WSTypeRegisterDevice, wsRequest.Type)
		h.sendError(conn, wsRequest.ID, protocol.ErrCodeInvalidMessageType, fmt.Sprintf("Expected '%s' or '%s' message", protocol.WSTypeHello, protocol.WSTypeRegisterDevice))
		return
	}
	if err != nil {
		log.Printf("[device] Registration failed: %v", err)
		return
	}

	// Get deviceID from connection context
	deviceID = h.getDeviceIDFromConn(conn)
	if deviceID == "" {
		log.Printf("[device] Failed to get deviceID after registration")
		h.sendError(conn, wsRequest.ID, protocol.ErrCodeRegistrationFailed, "Failed to get device ID")
		return
	}

	// Handle device messages in loop
	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			// A close handshake means the device meant to leave. Anything else
			// — an abrupt TCP reset, a dead radio — is a drop.
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				reason = protocol.DisconnectClosed
			}
			break
		}

		if messageType == websocket.TextMessage {
			var wsRequest protocol.WebSocketRequest
			if err := json.Unmarshal(message, &wsRequest); err != nil {
				log.Printf("[device] Failed to parse message: %v", err)
				h.sendError(conn, "", protocol.ErrCodeParse, "Invalid message format")
				continue
			}

			// Route message to appropriate handler
			var handlerErr error

			switch wsRequest.Type {
			case protocol.WSTypeTagScanned:
				handlerErr = h.handleTagScanned(conn, deviceID, wsRequest)
			case protocol.WSTypeTagRemoved:
				handlerErr = h.handleTagRemoved(conn, deviceID, wsRequest)
			case protocol.WSTypeDeviceHeartbeat:
				handlerErr = h.handleDeviceHeartbeat(conn, deviceID, wsRequest)
			case protocol.WSTypeGoodbye:
				reason = protocol.DisconnectGoodbye
				h.handleGoodbye(conn, deviceID, wsRequest)
				return
			case protocol.WSTypeDeviceWriteResponse:
				handlerErr = h.handleWriteResponse(deviceID, wsRequest)
			default:
				log.Printf("[device] Unknown message type: %s", wsRequest.Type)
				h.sendError(conn, wsRequest.ID, protocol.ErrCodeUnknownType, fmt.Sprintf("Unknown message type: %s", wsRequest.Type))
				continue
			}

			if handlerErr != nil {
				log.Printf("[device] Handler error for message type '%s': %v", wsRequest.Type, handlerErr)
			}
		}
	}
}

// handleHello processes a v1 hello frame, which carries the protocol version
// alongside the registration payload.
func (h *DeviceHandler) handleHello(_ context.Context, conn *server.SafeConn, req protocol.WebSocketRequest) error {
	var hello protocol.HelloRequest
	if err := decodePayload(req.Payload, &hello); err != nil {
		h.sendError(conn, req.ID, protocol.ErrCodeInvalidPayload, "Invalid hello request format")
		return fmt.Errorf("failed to parse hello request: %w", err)
	}

	version := protocol.NegotiateDeviceVersion(hello.ProtocolVersion)

	device, regResp, err := h.register(conn, req.ID, hello.DeviceRegistrationRequest, version)
	if err != nil {
		return err
	}

	return h.sendRegistration(conn, device, protocol.WebSocketResponse{
		ID:      req.ID,
		Type:    protocol.WSTypeHelloResponse,
		Success: true,
		Payload: protocol.HelloResponse{
			ProtocolVersion:            version,
			DeviceRegistrationResponse: regResp,
		},
	})
}

// handleRegisterDevice processes a legacy v0 registration request.
func (h *DeviceHandler) handleRegisterDevice(_ context.Context, conn *server.SafeConn, req protocol.WebSocketRequest) error {
	var regReq protocol.DeviceRegistrationRequest
	if err := decodePayload(req.Payload, &regReq); err != nil {
		h.sendError(conn, req.ID, protocol.ErrCodeInvalidPayload, "Invalid registration request format")
		return fmt.Errorf("failed to parse registration request: %w", err)
	}

	device, regResp, err := h.register(conn, req.ID, regReq, protocol.DeviceProtocolV0)
	if err != nil {
		return err
	}

	return h.sendRegistration(conn, device, protocol.WebSocketResponse{
		ID:      req.ID,
		Type:    protocol.WSTypeRegisterDeviceResponse,
		Success: true,
		Payload: regResp,
	})
}

// register validates the registration payload and registers the device, leaving
// the caller to send the response in whichever dialect it is speaking.
func (h *DeviceHandler) register(conn *server.SafeConn, reqID string, regReq protocol.DeviceRegistrationRequest, version int) (*remotenfc.Device, protocol.DeviceRegistrationResponse, error) {
	if regReq.DeviceName == "" {
		h.sendError(conn, reqID, protocol.ErrCodeInvalidRequest, "Device name is required")
		return nil, protocol.DeviceRegistrationResponse{}, fmt.Errorf("device name is required")
	}

	device, err := h.manager.RegisterDevice(remotenfc.DeviceRegistrationRequest{
		DeviceName:      regReq.DeviceName,
		Platform:        regReq.Platform,
		AppVersion:      regReq.AppVersion,
		ProtocolVersion: version,
		Capabilities:    regReq.Capabilities,
		Metadata:        regReq.Metadata,
	})
	if err != nil {
		h.sendError(conn, reqID, protocol.ErrCodeRegistrationFailed, err.Error())
		return nil, protocol.DeviceRegistrationResponse{}, fmt.Errorf("failed to register device: %w", err)
	}

	h.addDeviceSession(device.DeviceID(), conn)

	return device, protocol.DeviceRegistrationResponse{
		DeviceID:     device.DeviceID(),
		SessionToken: "",
		ServerInfo: protocol.ServerInfo{
			Version:      "1.0.0",
			SupportedNFC: []string{"mifare", "desfire", "type4", "ultralight"},
		},
	}, nil
}

func (h *DeviceHandler) sendRegistration(conn *server.SafeConn, device *remotenfc.Device, resp protocol.WebSocketResponse) error {
	deviceID := device.DeviceID()

	if err := conn.WriteJSON(resp); err != nil {
		h.removeDeviceSession(deviceID)
		h.manager.UnregisterDevice(deviceID)
		return fmt.Errorf("failed to send registration response: %w", err)
	}

	log.Printf("[device] Device registered: %s (%s, protocol v%d)", device.String(), deviceID, device.ProtocolVersion())

	return nil
}

// decodePayload re-marshals a decoded payload map into a concrete type.
func decodePayload(payload map[string]any, target any) error {
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}
	return json.Unmarshal(payloadBytes, target)
}

// handleTagScanned processes a tag scan event from a device.
func (h *DeviceHandler) handleTagScanned(conn *server.SafeConn, deviceID string, req protocol.WebSocketRequest) error {
	payloadBytes, err := json.Marshal(req.Payload)
	if err != nil {
		log.Printf("[device] Failed to marshal tag data: %v", err)
		h.sendError(conn, req.ID, protocol.ErrCodeInvalidPayload, "Failed to process payload")
		return err
	}

	var tagData protocol.DeviceTagData
	if err := json.Unmarshal(payloadBytes, &tagData); err != nil {
		log.Printf("[device] Failed to parse tag data: %v", err)
		h.sendError(conn, req.ID, protocol.ErrCodeInvalidPayload, "Invalid tag data format")
		return err
	}

	// Validate deviceID matches
	if tagData.DeviceID != "" && tagData.DeviceID != deviceID {
		h.sendError(conn, req.ID, protocol.ErrCodeInvalidDevice, "Device ID mismatch")
		return fmt.Errorf("device ID mismatch: expected %s, got %s", deviceID, tagData.DeviceID)
	}
	tagData.DeviceID = deviceID

	// Convert to remotenfc.TagData and send
	phoneTagData := remotenfc.TagData{
		DeviceID:     tagData.DeviceID,
		UID:          tagData.UID,
		Technology:   tagData.Technology,
		Type:         tagData.Type,
		ATR:          tagData.ATR,
		ScannedAt:    tagData.ScannedAt,
		NDEFMessage:  tagData.NDEFMessage,
		RawData:      tagData.RawData,
		Capabilities: tagData.Capabilities,
	}

	if err := h.manager.SendTagData(deviceID, phoneTagData); err != nil {
		log.Printf("[device] Failed to send tag data: %v", err)
		h.sendTagError(conn, req.ID, err)
		return err
	}

	h.setActiveTag(deviceID, tagData.UID)

	log.Printf("[device] Tag scanned: device=%s, UID=%s, Type=%s", deviceID, tagData.UID, tagData.Type)
	return nil
}

// handleTagRemoved processes a tag removal event from a device.
func (h *DeviceHandler) handleTagRemoved(conn *server.SafeConn, deviceID string, req protocol.WebSocketRequest) error {
	payloadBytes, err := json.Marshal(req.Payload)
	if err != nil {
		log.Printf("[device] Failed to marshal tag removed data: %v", err)
		h.sendError(conn, req.ID, protocol.ErrCodeInvalidPayload, "Failed to process payload")
		return err
	}

	var removedData protocol.DeviceTagRemovedData
	if err := json.Unmarshal(payloadBytes, &removedData); err != nil {
		log.Printf("[device] Failed to parse tag removed data: %v", err)
		h.sendError(conn, req.ID, protocol.ErrCodeInvalidPayload, "Invalid tag removed data format")
		return err
	}

	// Validate deviceID matches
	if removedData.DeviceID != "" && removedData.DeviceID != deviceID {
		h.sendError(conn, req.ID, protocol.ErrCodeInvalidDevice, "Device ID mismatch")
		return fmt.Errorf("device ID mismatch")
	}
	removedData.DeviceID = deviceID

	// Convert to remotenfc type
	phoneRemovedData := remotenfc.TagRemovedData{
		DeviceID:  removedData.DeviceID,
		UID:       removedData.UID,
		RemovedAt: removedData.RemovedAt,
	}

	h.clearActiveTag(deviceID, removedData.UID)

	if err := h.manager.SendTagRemoved(deviceID, phoneRemovedData); err != nil {
		log.Printf("[device] Failed to send tag removed: %v", err)
		h.sendTagError(conn, req.ID, err)
		return err
	}

	return nil
}

// handleDeviceHeartbeat processes a heartbeat from a device.
func (h *DeviceHandler) handleDeviceHeartbeat(_ *server.SafeConn, deviceID string, req protocol.WebSocketRequest) error {
	payloadBytes, err := json.Marshal(req.Payload)
	if err != nil {
		return err
	}

	var heartbeat protocol.DeviceHeartbeat
	if err := json.Unmarshal(payloadBytes, &heartbeat); err != nil {
		return err
	}

	// Validate deviceID matches
	if heartbeat.DeviceID != "" && heartbeat.DeviceID != deviceID {
		return fmt.Errorf("device ID mismatch")
	}

	h.manager.UpdateHeartbeat(deviceID)
	return nil
}

// WriteTag implements remotenfc.TagWriter, letting a tag write through the
// device holding it.
func (h *DeviceHandler) WriteTag(deviceID, tagUID string, ndef []byte, opts nfc.WriteOptions) error {
	id := uuid.NewString()

	resp, err := h.WriteToDevice(deviceID, protocol.DeviceWriteRequest{
		RequestID:      id,
		TagUID:         tagUID,
		NDEFBytes:      ndef,
		Lock:           opts.Lock,
		IdempotencyKey: id,
	})
	if err != nil {
		return err
	}
	if !resp.Success {
		return writeResponseError("WriteData", tagUID, resp)
	}
	return nil
}

// LockTag implements remotenfc.TagWriter.
func (h *DeviceHandler) LockTag(deviceID, tagUID string) error {
	id := uuid.NewString()

	resp, err := h.WriteToDevice(deviceID, protocol.DeviceWriteRequest{
		RequestID:      id,
		TagUID:         tagUID,
		Lock:           true,
		IdempotencyKey: id,
	})
	if err != nil {
		return err
	}
	if !resp.Success {
		return writeResponseError("MakeReadOnly", tagUID, resp)
	}
	return nil
}

// DeviceCanWrite implements remotenfc.TagWriter.
func (h *DeviceHandler) DeviceCanWrite(deviceID string) bool {
	return h.deviceDeclared(deviceID, func(c protocol.DeviceCapabilities) bool { return c.CanWrite })
}

// DeviceCanLock implements remotenfc.TagWriter.
func (h *DeviceHandler) DeviceCanLock(deviceID string) bool {
	return h.deviceDeclared(deviceID, func(c protocol.DeviceCapabilities) bool { return c.CanLock })
}

// deviceDeclared reports whether a still-connected device declared a capability.
func (h *DeviceHandler) deviceDeclared(deviceID string, want func(protocol.DeviceCapabilities) bool) bool {
	if h.manager == nil {
		return false
	}

	device, ok := h.manager.GetDevice(deviceID)
	if !ok || !device.IsActive() {
		return false
	}

	h.deviceSessionsMux.RLock()
	_, connected := h.deviceSessions[deviceID]
	h.deviceSessionsMux.RUnlock()

	return connected && want(device.PhoneCapabilities())
}

// writeResponseError turns a device's refusal into a typed error, so the code
// it reported survives instead of collapsing into a string.
func writeResponseError(op, tagUID string, resp protocol.DeviceWriteResponse) error {
	message := resp.Error
	if message == "" {
		message = "device reported the operation failed"
	}

	fallback := nfc.ErrCodeWriteFailed
	if op == "MakeReadOnly" {
		fallback = nfc.ErrCodeReadOnly
	}

	return &nfc.NFCError{
		Code:    nfc.InternalErrorCode(resp.ErrorCode, fallback),
		Op:      op,
		TagUID:  tagUID,
		Message: message,
	}
}

// ActiveTagDevice returns the device currently holding a tag in its field.
func (h *DeviceHandler) ActiveTagDevice() (deviceID string, uid string, ok bool) {
	h.activeMux.RLock()
	defer h.activeMux.RUnlock()

	if h.active.deviceID == "" {
		return "", "", false
	}
	return h.active.deviceID, h.active.uid, true
}

func (h *DeviceHandler) setActiveTag(deviceID, uid string) {
	h.activeMux.Lock()
	defer h.activeMux.Unlock()
	h.active = activeTag{deviceID: deviceID, uid: uid}
}

// clearActiveTag forgets the active tag, but only if it is still the one being
// cleared — a second device may have presented a tag in the meantime.
func (h *DeviceHandler) clearActiveTag(deviceID, uid string) {
	h.activeMux.Lock()
	defer h.activeMux.Unlock()

	if h.active.deviceID != deviceID {
		return
	}
	if uid != "" && h.active.uid != "" && h.active.uid != uid {
		return
	}
	h.active = activeTag{}
}

// WriteToDevice asks a device to write a tag and waits for its outcome.
func (h *DeviceHandler) WriteToDevice(deviceID string, req protocol.DeviceWriteRequest) (protocol.DeviceWriteResponse, error) {
	respCh := make(chan protocol.DeviceWriteResponse, 1)

	h.pendingWritesMux.Lock()
	h.pendingWrites[req.RequestID] = pendingWrite{deviceID: deviceID, respCh: respCh}
	h.pendingWritesMux.Unlock()

	defer func() {
		h.pendingWritesMux.Lock()
		delete(h.pendingWrites, req.RequestID)
		h.pendingWritesMux.Unlock()
	}()

	req.DeviceID = deviceID
	if err := h.SendToDevice(deviceID, protocol.WebSocketMessage{
		ID:      req.RequestID,
		Type:    protocol.WSTypeDeviceWriteRequest,
		Payload: req,
	}); err != nil {
		return protocol.DeviceWriteResponse{}, err
	}

	select {
	case resp := <-respCh:
		return resp, nil
	case <-time.After(DeviceWriteTimeout):
		return protocol.DeviceWriteResponse{}, fmt.Errorf("device %s did not respond within %s", deviceID, DeviceWriteTimeout)
	}
}

// handleWriteResponse delivers a device's write outcome to whoever is waiting.
func (h *DeviceHandler) handleWriteResponse(deviceID string, req protocol.WebSocketRequest) error {
	var resp protocol.DeviceWriteResponse
	if err := decodePayload(req.Payload, &resp); err != nil {
		return fmt.Errorf("failed to parse write response: %w", err)
	}

	if resp.RequestID == "" {
		resp.RequestID = req.ID
	}

	h.pendingWritesMux.Lock()
	pending, ok := h.pendingWrites[resp.RequestID]
	if ok {
		delete(h.pendingWrites, resp.RequestID)
	}
	h.pendingWritesMux.Unlock()

	if !ok {
		// Already timed out, or a response to a request we never sent.
		log.Printf("[device] Unmatched write response from %s: %s", deviceID, resp.RequestID)
		return nil
	}

	// A device may only answer for its own requests.
	if pending.deviceID != deviceID {
		return fmt.Errorf("write response for %s came from %s", pending.deviceID, deviceID)
	}

	pending.respCh <- resp
	return nil
}

// failPendingWrites releases waiters blocked on a device that has gone away,
// rather than making each of them wait out the full timeout.
func (h *DeviceHandler) failPendingWrites(deviceID string) {
	h.pendingWritesMux.Lock()
	defer h.pendingWritesMux.Unlock()

	for requestID, pending := range h.pendingWrites {
		if pending.deviceID != deviceID {
			continue
		}
		pending.respCh <- protocol.DeviceWriteResponse{
			RequestID: requestID,
			Success:   false,
			Error:     "device disconnected before reporting the write outcome",
			ErrorCode: protocol.ErrCodeDeviceGone,
		}
		delete(h.pendingWrites, requestID)
	}
}

// handleGoodbye acknowledges a device's announced departure with a close
// handshake, so the device knows the agent heard it rather than timing out.
func (h *DeviceHandler) handleGoodbye(conn *server.SafeConn, deviceID string, req protocol.WebSocketRequest) {
	var goodbye protocol.GoodbyeRequest
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

// handleDeviceDisconnect cleans up when a device WebSocket closes.
func (h *DeviceHandler) handleDeviceDisconnect(deviceID string, reason protocol.DisconnectReason) {
	h.removeDeviceSession(deviceID)
	h.failPendingWrites(deviceID)
	h.clearActiveTag(deviceID, "")

	if h.manager != nil {
		h.manager.UnregisterDevice(deviceID)
	}

	if reason.Expected() {
		log.Printf("[device] Device disconnected: %s (%s)", deviceID, reason)
	} else {
		log.Printf("[device] Device lost: %s (no close handshake)", deviceID)
	}
}

// addDeviceSession stores a WebSocket connection for a device.
func (h *DeviceHandler) addDeviceSession(deviceID string, conn *server.SafeConn) {
	h.deviceSessionsMux.Lock()
	defer h.deviceSessionsMux.Unlock()

	h.deviceSessions[deviceID] = conn
	h.connToDeviceID[conn] = deviceID
}

// getDeviceIDFromConn retrieves the device ID for a connection.
func (h *DeviceHandler) getDeviceIDFromConn(conn *server.SafeConn) string {
	h.deviceSessionsMux.RLock()
	defer h.deviceSessionsMux.RUnlock()

	return h.connToDeviceID[conn]
}

// removeDeviceSession removes a WebSocket connection for a device.
func (h *DeviceHandler) removeDeviceSession(deviceID string) {
	h.deviceSessionsMux.Lock()
	defer h.deviceSessionsMux.Unlock()

	if conn, ok := h.deviceSessions[deviceID]; ok {
		delete(h.connToDeviceID, conn)
		delete(h.deviceSessions, deviceID)
	}
}

// SendToDevice sends a message to a specific device.
func (h *DeviceHandler) SendToDevice(deviceID string, message any) error {
	h.deviceSessionsMux.RLock()
	conn, ok := h.deviceSessions[deviceID]
	h.deviceSessionsMux.RUnlock()

	if !ok {
		return fmt.Errorf("device not connected: %s", deviceID)
	}

	return conn.WriteJSON(message)
}

// sendError sends an error response to a device.
func (h *DeviceHandler) sendError(conn *server.SafeConn, requestID string, errorCode protocol.ErrorCode, message string) {
	h.sendErrorPayload(conn, requestID, protocol.NewErrorPayload(errorCode), message)
}

// sendTagError reports a failure carrying tag data to the manager. A typed
// NFCError keeps its code, operation, and tag; anything else is a transient
// delivery failure, which is what TAG_SEND_FAILED has always meant.
func (h *DeviceHandler) sendTagError(conn *server.SafeConn, requestID string, err error) {
	payload := nfc.WireError(err)
	if payload.Code == protocol.ErrCodeUnknownError {
		payload = protocol.NewErrorPayload(protocol.ErrCodeTagSendFailed)
	}

	h.sendErrorPayload(conn, requestID, payload, err.Error())
}

func (h *DeviceHandler) sendErrorPayload(conn *server.SafeConn, requestID string, payload protocol.ErrorPayload, message string) {
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

// IsDeviceConnection determines if a request is from a device.
func IsDeviceConnection(r *http.Request) bool {
	// Check X-Device-Mode header
	if r.Header.Get("X-Device-Mode") == "true" {
		return true
	}

	// Check query parameter
	if r.URL.Query().Get("mode") == "device" {
		return true
	}

	return false
}

// Ensure DeviceHandler implements server.HandlerServer (partially)
var _ nfc.DeviceEventEmitter = (*remotenfc.Device)(nil)
