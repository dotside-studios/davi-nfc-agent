// Package clientserver provides the WebSocket server for client applications.
package clientserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// Server handles client connections for consuming NFC data.
type Server struct {
	config Config
	bridge *server.ServerBridge

	ctx    context.Context
	cancel context.CancelFunc

	// WebSocket upgrader
	upgrader websocket.Upgrader

	// Client connections (multiple allowed)
	clients    map[*server.SafeConn]string // conn -> clientID
	clientsMux sync.RWMutex

	// Last received data for late joiners
	lastCard *nfc.Card
	cardMu   sync.RWMutex
}

// New creates a new client server instance.
func New(config Config, bridge *server.ServerBridge) *Server {
	return &Server{
		config:  config,
		bridge:  bridge,
		clients: make(map[*server.SafeConn]string),
		upgrader: websocket.Upgrader{
			CheckOrigin: originChecker(config),
		},
	}
}

// StartBackground starts the client-side background work — the bridge listeners
// that fan tag data and device status out to connected clients — under the given
// parent context, without binding an HTTP listener. It returns immediately once
// the goroutines are running.
//
// The unified server owns the HTTP listener and routes client WebSocket
// connections here via ServeWS.
func (s *Server) StartBackground(ctx context.Context) {
	// Derive a cancelable context so Stop can tear the goroutines down even
	// when the parent context outlives this server.
	s.ctx, s.cancel = context.WithCancel(ctx)

	// Start bridge listeners
	go s.listenBridgeTagData()
	go s.listenBridgeDeviceStatus()
}

// ServeWS handles a WebSocket connection request for a client. It performs its
// own API-secret check and origin validation, so it is safe to call directly
// from a shared listener (unified single-port mode).
func (s *Server) ServeWS(w http.ResponseWriter, r *http.Request) {
	s.handleWebSocket(w, r)
}

// ClientCount returns the number of currently connected clients.
func (s *Server) ClientCount() int {
	return s.clientCount()
}

// Stop stops the client server's background work. The unified server owns the
// HTTP listener; this only cancels the context the background goroutines run
// under.
func (s *Server) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

// clientCount returns the number of connected clients.
func (s *Server) clientCount() int {
	s.clientsMux.RLock()
	defer s.clientsMux.RUnlock()
	return len(s.clients)
}

// GetLastCard returns the last received card data.
func (s *Server) GetLastCard() *nfc.Card {
	s.cardMu.RLock()
	defer s.cardMu.RUnlock()
	return s.lastCard
}

// handleWebSocket handles WebSocket connections from clients.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if !server.CheckAuth(w, r, s.config.APISecret, s.config.TokenVerifier) {
		log.Printf("[client] WebSocket connection rejected from %s: bad/missing API secret", r.RemoteAddr)
		return
	}

	wsConn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[client] WebSocket upgrade error: %v", err)
		return
	}

	conn := server.NewSafeConn(wsConn)
	clientID := uuid.New().String()

	// Add to clients map
	s.clientsMux.Lock()
	s.clients[conn] = clientID
	s.clientsMux.Unlock()

	log.Printf("[client] Client connected: %s (total: %d)", clientID[:8], s.clientCount())

	defer func() {
		conn.Close()
		s.clientsMux.Lock()
		delete(s.clients, conn)
		s.clientsMux.Unlock()
		log.Printf("[client] Client disconnected: %s (total: %d)", clientID[:8], s.clientCount())
	}()

	// Handle incoming messages
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("[client] WebSocket read error: %v", err)
			}
			break
		}

		var req protocol.WebSocketRequest
		if err := json.Unmarshal(message, &req); err != nil {
			log.Printf("[client] Failed to parse message: %v", err)
			s.sendErrorResponse(conn, "", protocol.ErrCodeParse, "Invalid message format")
			continue
		}

		// Handle message types
		switch req.Type {
		case server.WSMessageTypeWriteRequest:
			s.handleWriteRequest(conn, clientID, req)
		case server.WSMessageTypeLockRequest:
			s.handleLockRequest(conn, clientID, req)
		case server.WSMessageTypeCapabilitiesRequest:
			s.handleCapabilitiesRequest(conn, clientID, req)
		default:
			log.Printf("[client] Unknown message type: %s", req.Type)
			s.sendErrorResponse(conn, req.ID, protocol.ErrCodeUnknownType, fmt.Sprintf("Unknown message type: %s", req.Type))
		}
	}
}

// handleWriteRequest handles write requests from clients.
func (s *Server) handleWriteRequest(conn *server.SafeConn, clientID string, req protocol.WebSocketRequest) {
	// Parse write request from payload
	payloadBytes, err := json.Marshal(req.Payload)
	if err != nil {
		log.Printf("[client] Failed to marshal write request payload: %v", err)
		s.sendErrorResponse(conn, req.ID, protocol.ErrCodeInvalidPayload, "Invalid write request payload")
		return
	}

	var writeReq server.WriteRequest
	if err := json.Unmarshal(payloadBytes, &writeReq); err != nil {
		log.Printf("[client] Failed to parse write request: %v", err)
		s.sendErrorResponse(conn, req.ID, protocol.ErrCodeInvalidRequest, "Failed to parse write request")
		return
	}

	// Create request message
	requestID := req.ID
	if requestID == "" {
		requestID = uuid.New().String()
	}

	msg := server.WriteRequestMessage{
		RequestID:  requestID,
		ClientID:   clientID,
		Request:    writeReq,
		ResponseCh: make(chan server.WriteResponseMessage, 1),
	}

	// Send through bridge and wait for response
	response, err := s.bridge.SendWriteRequest(msg)
	if err != nil {
		log.Printf("[client] Write request failed: %v", err)
		s.sendOperationError(conn, req.ID, protocol.ErrCodeWriteFailed, err)
		return
	}

	// Send response to client
	wsResponse := protocol.WebSocketResponse{
		ID:      req.ID,
		Type:    server.WSMessageTypeWriteResponse,
		Success: response.Success,
	}
	if response.Success {
		payload := map[string]interface{}{
			"message": "Write operation completed successfully",
		}
		// Surface the verified write outcome so clients can confirm the data
		// actually landed (verified), how many attempts it took, and the size.
		if wr, ok := response.Payload.(*nfc.WriteResult); ok && wr != nil {
			payload["uid"] = wr.UID
			payload["tagType"] = wr.TagType
			payload["bytesWritten"] = wr.BytesWritten
			payload["verified"] = wr.Verified
			payload["attempts"] = wr.Attempts
			payload["locked"] = wr.Locked
		}
		wsResponse.Payload = payload
	} else {
		wsResponse.Error = response.Error
		wsResponse.Payload = errorPayloadOrDefault(response.ErrorCode, protocol.ErrCodeWriteFailed)
	}

	if err := conn.WriteJSON(wsResponse); err != nil {
		log.Printf("[client] Failed to send write response: %v", err)
	}
}

// handleLockRequest handles make-read-only (lock) requests from clients.
func (s *Server) handleLockRequest(conn *server.SafeConn, clientID string, req protocol.WebSocketRequest) {
	requestID := req.ID
	if requestID == "" {
		requestID = uuid.New().String()
	}

	msg := server.LockRequestMessage{
		RequestID:  requestID,
		ClientID:   clientID,
		ResponseCh: make(chan server.LockResponseMessage, 1),
	}

	// Send through bridge and wait for response
	response, err := s.bridge.SendLockRequest(msg)
	if err != nil {
		log.Printf("[client] Lock request failed: %v", err)
		s.sendOperationError(conn, req.ID, protocol.ErrCodeLockFailed, err)
		return
	}

	wsResponse := protocol.WebSocketResponse{
		ID:      req.ID,
		Type:    server.WSMessageTypeLockResponse,
		Success: response.Success,
	}
	if response.Success {
		payload := map[string]interface{}{
			"message": "Lock operation completed successfully",
		}
		if lr, ok := response.Payload.(*nfc.LockResult); ok && lr != nil {
			payload["uid"] = lr.UID
			payload["tagType"] = lr.TagType
			payload["locked"] = lr.Locked
		}
		wsResponse.Payload = payload
	} else {
		wsResponse.Error = response.Error
		wsResponse.Payload = errorPayloadOrDefault(response.ErrorCode, protocol.ErrCodeLockFailed)
	}

	if err := conn.WriteJSON(wsResponse); err != nil {
		log.Printf("[client] Failed to send lock response: %v", err)
	}
}

// handleCapabilitiesRequest handles capabilities queries for the present tag.
func (s *Server) handleCapabilitiesRequest(conn *server.SafeConn, clientID string, req protocol.WebSocketRequest) {
	requestID := req.ID
	if requestID == "" {
		requestID = uuid.New().String()
	}

	msg := server.CapabilitiesRequestMessage{
		RequestID:  requestID,
		ClientID:   clientID,
		ResponseCh: make(chan server.CapabilitiesResponseMessage, 1),
	}

	// Send through bridge and wait for response
	response, err := s.bridge.SendCapabilitiesRequest(msg)
	if err != nil {
		log.Printf("[client] Capabilities request failed: %v", err)
		s.sendOperationError(conn, req.ID, protocol.ErrCodeCapabilitiesFailed, err)
		return
	}

	wsResponse := protocol.WebSocketResponse{
		ID:      req.ID,
		Type:    server.WSMessageTypeCapabilitiesResponse,
		Success: response.Success,
	}
	if response.Success {
		wsResponse.Payload = map[string]interface{}{
			"capabilities": response.Payload,
		}
	} else {
		wsResponse.Error = response.Error
		wsResponse.Payload = errorPayloadOrDefault(response.ErrorCode, protocol.ErrCodeCapabilitiesFailed)
	}

	if err := conn.WriteJSON(wsResponse); err != nil {
		log.Printf("[client] Failed to send capabilities response: %v", err)
	}
}

// listenBridgeTagData listens for tag data from the bridge and broadcasts to clients.
func (s *Server) listenBridgeTagData() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case data, ok := <-s.bridge.TagData:
			if !ok {
				return
			}
			// Store last card
			if data.Card != nil {
				s.cardMu.Lock()
				s.lastCard = data.Card
				s.cardMu.Unlock()
			}
			// Broadcast to all clients
			s.broadcastTagData(data)
		}
	}
}

// listenBridgeDeviceStatus listens for device status from the bridge and broadcasts to clients.
func (s *Server) listenBridgeDeviceStatus() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case status, ok := <-s.bridge.DeviceStatus:
			if !ok {
				return
			}
			s.broadcastDeviceStatus(status)
		}
	}
}

// broadcastTagData sends tag data to all connected clients.
func (s *Server) broadcastTagData(data nfc.NFCData) {
	s.clientsMux.RLock()
	defer s.clientsMux.RUnlock()

	for conn := range s.clients {
		s.sendTagDataToClient(conn, data)
	}
}

// sendTagDataToClient sends tag data to a specific client.
func (s *Server) sendTagDataToClient(conn *server.SafeConn, data nfc.NFCData) {
	var errStr *string
	if data.Err != nil {
		e := data.Err.Error()
		errStr = &e
	}

	var payload map[string]interface{}

	if data.Card != nil {
		payload = map[string]interface{}{
			"uid":          data.Card.UID,
			"type":         data.Card.Type,
			"technology":   data.Card.Technology,
			"scannedAt":    data.Card.ScannedAt.Format("2006-01-02T15:04:05Z07:00"),
			"capabilities": data.Card.Capabilities(),
			"err":          errStr,
		}

		// Try to read and parse message from card
		if msg, err := data.Card.ReadMessage(); err == nil {
			var text string
			var messageInfo map[string]interface{}

			if ndefMsg, ok := msg.(*nfc.NDEFMessage); ok {
				text, _ = ndefMsg.GetText()
				messageInfo = ndefMsg.ToJSONMap()
			} else if textMsg, ok := msg.(*nfc.TextMessage); ok {
				text = textMsg.Text
				messageInfo = map[string]interface{}{
					"type": "raw",
					"data": textMsg.Bytes(),
				}
			}

			payload["message"] = messageInfo
			payload["text"] = text
		} else {
			payload["text"] = ""
		}
	} else {
		payload = map[string]interface{}{
			"uid":  "",
			"text": "",
			"err":  errStr,
		}
	}

	message := protocol.WebSocketMessage{
		Type:    server.WSMessageTypeTagData,
		Payload: payload,
	}

	if err := conn.WriteJSON(message); err != nil {
		log.Printf("[client] Failed to send tag data: %v", err)
	}
}

// broadcastDeviceStatus sends device status to all connected clients.
func (s *Server) broadcastDeviceStatus(status nfc.DeviceStatus) {
	s.clientsMux.RLock()
	defer s.clientsMux.RUnlock()

	message := protocol.WebSocketMessage{
		Type:    server.WSMessageTypeDeviceStatus,
		Payload: status,
	}

	for conn := range s.clients {
		if err := conn.WriteJSON(message); err != nil {
			log.Printf("[client] Failed to send device status: %v", err)
		}
	}
}

// sendErrorResponse sends an error response to a WebSocket client.
func (s *Server) sendErrorResponse(conn *server.SafeConn, requestID string, errorCode protocol.ErrorCode, message string) {
	response := protocol.WebSocketResponse{
		ID:      requestID,
		Type:    server.WSMessageTypeError,
		Success: false,
		Error:   message,
		Payload: protocol.NewErrorPayload(errorCode),
	}

	if err := conn.WriteJSON(response); err != nil {
		log.Printf("[client] Failed to send error response: %v", err)
	}
}

// sendOperationError reports a failed tag operation, keeping the code the
// underlying NFCError carried rather than flattening every failure to one
// per-operation label. A client can then tell "present the tag again" from
// "this tag is locked".
func (s *Server) sendOperationError(conn *server.SafeConn, requestID string, fallback protocol.ErrorCode, err error) {
	payload := nfc.WireError(err)
	if payload.Code == protocol.ErrCodeUnknownError {
		payload = protocol.NewErrorPayload(fallback)
	}

	response := protocol.WebSocketResponse{
		ID:      requestID,
		Type:    server.WSMessageTypeError,
		Success: false,
		Error:   err.Error(),
		Payload: payload,
	}

	if writeErr := conn.WriteJSON(response); writeErr != nil {
		log.Printf("[client] Failed to send error response: %v", writeErr)
	}
}

// errorPayloadOrDefault builds the error payload for an operation the reader or
// device refused, preferring the code it reported over the generic one.
func errorPayloadOrDefault(code, fallback protocol.ErrorCode) protocol.ErrorPayload {
	if code == "" {
		code = fallback
	}
	return protocol.NewErrorPayload(code)
}

// originChecker prefers an explicit policy over the static allowlist, so the
// tray can admit an origin without restarting the listener.
func originChecker(config Config) func(r *http.Request) bool {
	if config.OriginPolicy != nil {
		return server.CheckOriginPolicy(config.OriginPolicy)
	}
	return server.CheckOrigin(config.AllowedOrigins)
}
