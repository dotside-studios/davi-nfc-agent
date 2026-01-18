package deviceserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/gorilla/websocket"
)

// DeviceHandler handles all device WebSocket connections and management.
type DeviceHandler struct {
	manager           *remotenfc.Manager
	bridge            *server.ServerBridge
	deviceSessions    map[string]*server.SafeConn // deviceID -> websocket conn
	deviceSessionsMux sync.RWMutex
	connToDeviceID    map[*server.SafeConn]string // reverse lookup: conn -> deviceID
	upgrader          websocket.Upgrader
}

// NewDeviceHandler creates a new device handler.
func NewDeviceHandler(manager *remotenfc.Manager, bridge *server.ServerBridge) *DeviceHandler {
	return &DeviceHandler{
		manager:        manager,
		bridge:         bridge,
		deviceSessions: make(map[string]*server.SafeConn),
		connToDeviceID: make(map[*server.SafeConn]string),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins
			},
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

	log.Printf("[device] WebSocket connected from %s", r.RemoteAddr)

	var deviceID string
	defer func() {
		conn.Close()
		if deviceID != "" {
			h.handleDeviceDisconnect(deviceID)
		}
		log.Printf("[device] WebSocket disconnected: %s", deviceID)
	}()

	// Wait for registerDevice message
	messageType, message, err := conn.ReadMessage()
	if err != nil {
		log.Printf("[device] Failed to read registration message: %v", err)
		h.sendError(conn, "", "READ_ERROR", "Failed to read message")
		return
	}

	if messageType != websocket.TextMessage {
		log.Printf("[device] Expected text message, got type %d", messageType)
		h.sendError(conn, "", "INVALID_MESSAGE_TYPE", "Expected text message")
		return
	}

	var wsRequest protocol.WebSocketRequest
	if err := json.Unmarshal(message, &wsRequest); err != nil {
		log.Printf("[device] Failed to parse registration message: %v", err)
		h.sendError(conn, "", "PARSE_ERROR", "Invalid message format")
		return
	}

	if wsRequest.Type != protocol.WSTypeRegisterDevice {
		log.Printf("[device] Expected '%s', got '%s'", protocol.WSTypeRegisterDevice, wsRequest.Type)
		h.sendError(conn, wsRequest.ID, "INVALID_MESSAGE_TYPE", fmt.Sprintf("Expected '%s' message", protocol.WSTypeRegisterDevice))
		return
	}

	// Handle device registration
	if err = h.handleRegisterDevice(r.Context(), conn, wsRequest); err != nil {
		log.Printf("[device] Registration failed: %v", err)
		return
	}

	// Get deviceID from connection context
	deviceID = h.getDeviceIDFromConn(conn)
	if deviceID == "" {
		log.Printf("[device] Failed to get deviceID after registration")
		h.sendError(conn, wsRequest.ID, "REGISTRATION_FAILED", "Failed to get device ID")
		return
	}

	// Handle device messages in loop
	for {
		messageType, message, err := conn.ReadMessage()
		if err != nil {
			break
		}

		if messageType == websocket.TextMessage {
			var wsRequest protocol.WebSocketRequest
			if err := json.Unmarshal(message, &wsRequest); err != nil {
				log.Printf("[device] Failed to parse message: %v", err)
				h.sendError(conn, "", "PARSE_ERROR", "Invalid message format")
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
			case protocol.WSTypeDeviceWriteResponse:
				log.Printf("[device] Write response received (not yet implemented)")
				continue
			default:
				log.Printf("[device] Unknown message type: %s", wsRequest.Type)
				h.sendError(conn, wsRequest.ID, "UNKNOWN_TYPE", fmt.Sprintf("Unknown message type: %s", wsRequest.Type))
				continue
			}

			if handlerErr != nil {
				log.Printf("[device] Handler error for message type '%s': %v", wsRequest.Type, handlerErr)
			}
		}
	}
}

// handleRegisterDevice processes a device registration request.
func (h *DeviceHandler) handleRegisterDevice(_ context.Context, conn *server.SafeConn, req protocol.WebSocketRequest) error {
	// Parse DeviceRegistrationRequest from payload
	payloadBytes, err := json.Marshal(req.Payload)
	if err != nil {
		h.sendError(conn, req.ID, "INVALID_PAYLOAD", "Failed to process payload")
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	var regReq protocol.DeviceRegistrationRequest
	if err := json.Unmarshal(payloadBytes, &regReq); err != nil {
		h.sendError(conn, req.ID, "INVALID_PAYLOAD", "Invalid registration request format")
		return fmt.Errorf("failed to parse registration request: %w", err)
	}

	// Validate request
	if regReq.DeviceName == "" {
		h.sendError(conn, req.ID, "INVALID_REQUEST", "Device name is required")
		return fmt.Errorf("device name is required")
	}

	// Convert protocol type to remotenfc type for registration
	phoneReq := remotenfc.DeviceRegistrationRequest{
		DeviceName: regReq.DeviceName,
		Platform:   regReq.Platform,
		AppVersion: regReq.AppVersion,
		Capabilities: remotenfc.DeviceCapabilities{
			CanRead:  regReq.Capabilities.CanRead,
			CanWrite: regReq.Capabilities.CanWrite,
			NFCType:  regReq.Capabilities.NFCType,
		},
		Metadata: regReq.Metadata,
	}

	// Register device
	device, err := h.manager.RegisterDevice(phoneReq)
	if err != nil {
		h.sendError(conn, req.ID, "REGISTRATION_FAILED", err.Error())
		return fmt.Errorf("failed to register device: %w", err)
	}

	deviceID := device.DeviceID()

	// Store WebSocket connection
	h.addDeviceSession(deviceID, conn)

	// Send registration response
	response := protocol.WebSocketResponse{
		ID:      req.ID,
		Type:    protocol.WSTypeRegisterDeviceResponse,
		Success: true,
		Payload: protocol.DeviceRegistrationResponse{
			DeviceID:     deviceID,
			SessionToken: "",
			ServerInfo: protocol.ServerInfo{
				Version:      "1.0.0",
				SupportedNFC: []string{"mifare", "desfire", "type4", "ultralight"},
			},
		},
	}

	if err := conn.WriteJSON(response); err != nil {
		h.removeDeviceSession(deviceID)
		h.manager.UnregisterDevice(deviceID)
		return fmt.Errorf("failed to send registration response: %w", err)
	}

	log.Printf("[device] Device registered: %s (%s)", device.String(), deviceID)

	return nil
}

// handleTagScanned processes a tag scan event from a device.
func (h *DeviceHandler) handleTagScanned(conn *server.SafeConn, deviceID string, req protocol.WebSocketRequest) error {
	payloadBytes, err := json.Marshal(req.Payload)
	if err != nil {
		log.Printf("[device] Failed to marshal tag data: %v", err)
		h.sendError(conn, req.ID, "INVALID_PAYLOAD", "Failed to process payload")
		return err
	}

	var tagData protocol.DeviceTagData
	if err := json.Unmarshal(payloadBytes, &tagData); err != nil {
		log.Printf("[device] Failed to parse tag data: %v", err)
		h.sendError(conn, req.ID, "INVALID_PAYLOAD", "Invalid tag data format")
		return err
	}

	// Validate deviceID matches
	if tagData.DeviceID != "" && tagData.DeviceID != deviceID {
		h.sendError(conn, req.ID, "INVALID_DEVICE", "Device ID mismatch")
		return fmt.Errorf("device ID mismatch: expected %s, got %s", deviceID, tagData.DeviceID)
	}
	tagData.DeviceID = deviceID

	// Convert to remotenfc.TagData and send
	phoneTagData := remotenfc.TagData{
		DeviceID:    tagData.DeviceID,
		UID:         tagData.UID,
		Technology:  tagData.Technology,
		Type:        tagData.Type,
		ATR:         tagData.ATR,
		ScannedAt:   tagData.ScannedAt,
		NDEFMessage: tagData.NDEFMessage,
		RawData:     tagData.RawData,
	}

	if err := h.manager.SendTagData(deviceID, phoneTagData); err != nil {
		log.Printf("[device] Failed to send tag data: %v", err)
		h.sendError(conn, req.ID, "TAG_SEND_FAILED", err.Error())
		return err
	}

	log.Printf("[device] Tag scanned: device=%s, UID=%s, Type=%s", deviceID, tagData.UID, tagData.Type)
	return nil
}

// handleTagRemoved processes a tag removal event from a device.
func (h *DeviceHandler) handleTagRemoved(conn *server.SafeConn, deviceID string, req protocol.WebSocketRequest) error {
	payloadBytes, err := json.Marshal(req.Payload)
	if err != nil {
		log.Printf("[device] Failed to marshal tag removed data: %v", err)
		h.sendError(conn, req.ID, "INVALID_PAYLOAD", "Failed to process payload")
		return err
	}

	var removedData protocol.DeviceTagRemovedData
	if err := json.Unmarshal(payloadBytes, &removedData); err != nil {
		log.Printf("[device] Failed to parse tag removed data: %v", err)
		h.sendError(conn, req.ID, "INVALID_PAYLOAD", "Invalid tag removed data format")
		return err
	}

	// Validate deviceID matches
	if removedData.DeviceID != "" && removedData.DeviceID != deviceID {
		h.sendError(conn, req.ID, "INVALID_DEVICE", "Device ID mismatch")
		return fmt.Errorf("device ID mismatch")
	}
	removedData.DeviceID = deviceID

	// Convert to remotenfc type
	phoneRemovedData := remotenfc.TagRemovedData{
		DeviceID:  removedData.DeviceID,
		UID:       removedData.UID,
		RemovedAt: removedData.RemovedAt,
	}

	if err := h.manager.SendTagRemoved(deviceID, phoneRemovedData); err != nil {
		log.Printf("[device] Failed to send tag removed: %v", err)
		h.sendError(conn, req.ID, "TAG_SEND_FAILED", err.Error())
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

// handleDeviceDisconnect cleans up when device WebSocket closes.
func (h *DeviceHandler) handleDeviceDisconnect(deviceID string) {
	h.removeDeviceSession(deviceID)

	if h.manager != nil {
		h.manager.UnregisterDevice(deviceID)
	}

	log.Printf("[device] Device disconnected: %s", deviceID)
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
func (h *DeviceHandler) sendError(conn *server.SafeConn, requestID string, errorCode string, message string) {
	response := protocol.WebSocketResponse{
		ID:      requestID,
		Type:    protocol.WSTypeError,
		Success: false,
		Error:   message,
		Payload: map[string]any{
			"code": errorCode,
		},
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
