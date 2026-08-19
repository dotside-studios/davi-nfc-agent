// Package clientserver provides the WebSocket server for client applications.
package clientserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"sync"
	"time"

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
	clients    map[*server.SafeConn]*clientSession
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
		clients: make(map[*server.SafeConn]*clientSession),
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

// clientSession is what is known about one connected client. Kept so an
// operator can tell which application is driving the reader: a count alone
// cannot answer "what is writing to my tags".
type clientSession struct {
	id          string
	origin      string
	remoteAddr  string
	userAgent   string
	connectedAt time.Time

	// Counted per connection, so a client that is merely listening is
	// distinguishable from one issuing writes.
	writes int
	locks  int
}

// ClientInfo describes a connected client for display.
type ClientInfo struct {
	ID          string    `json:"id"`
	Origin      string    `json:"origin,omitempty"`
	RemoteAddr  string    `json:"remoteAddr"`
	UserAgent   string    `json:"userAgent,omitempty"`
	ConnectedAt time.Time `json:"connectedAt"`
	Writes      int       `json:"writes"`
	Locks       int       `json:"locks"`
}

// Clients returns the connected clients, most recently connected first.
func (s *Server) Clients() []ClientInfo {
	s.clientsMux.RLock()
	defer s.clientsMux.RUnlock()

	out := make([]ClientInfo, 0, len(s.clients))
	for _, c := range s.clients {
		out = append(out, ClientInfo{
			ID:          c.id,
			Origin:      c.origin,
			RemoteAddr:  c.remoteAddr,
			UserAgent:   c.userAgent,
			ConnectedAt: c.connectedAt,
			Writes:      c.writes,
			Locks:       c.locks,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ConnectedAt.After(out[j].ConnectedAt) })
	return out
}

// Disconnect closes one client's connection by ID, reporting whether it was
// found. The read loop notices the closed socket and removes the session, so
// this does not touch the map itself.
func (s *Server) Disconnect(clientID string) bool {
	s.clientsMux.RLock()
	var target *server.SafeConn
	for conn, c := range s.clients {
		if c.id == clientID {
			target = conn
			break
		}
	}
	s.clientsMux.RUnlock()

	if target == nil {
		return false
	}

	log.Printf("[client] Disconnecting client %s at an operator's request", clientID[:8])
	_ = target.Close()
	return true
}

// notifyChange tells any observer that the client list moved.
func (s *Server) notifyChange() {
	if s.config.OnChange != nil {
		s.config.OnChange()
	}
}

// countOperation records that a client issued a write or lock.
func (s *Server) countOperation(conn *server.SafeConn, kind string) {
	s.clientsMux.Lock()
	defer s.clientsMux.Unlock()
	c, ok := s.clients[conn]
	if !ok {
		return
	}
	switch kind {
	case "write":
		c.writes++
	case "lock":
		c.locks++
	case "transceive":
		// Counted with writes: a raw exchange can write, and the point of the
		// count is to show which clients are capable of changing a tag.
		c.writes++
	}
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
	s.clients[conn] = &clientSession{
		id:          clientID,
		origin:      r.Header.Get("Origin"),
		remoteAddr:  r.RemoteAddr,
		userAgent:   r.Header.Get("User-Agent"),
		connectedAt: time.Now(),
	}
	s.clientsMux.Unlock()
	s.notifyChange()

	log.Printf("[client] Client connected: %s (total: %d)", clientID[:8], s.clientCount())

	defer func() {
		_ = conn.Close()
		s.clientsMux.Lock()
		delete(s.clients, conn)
		s.clientsMux.Unlock()
		log.Printf("[client] Client disconnected: %s (total: %d)", clientID[:8], s.clientCount())
		s.notifyChange()
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
			s.countOperation(conn, "write")
			s.handleWriteRequest(conn, clientID, req)
		case server.WSMessageTypeLockRequest:
			s.countOperation(conn, "lock")
			s.handleLockRequest(conn, clientID, req)
		case server.WSMessageTypeCapabilitiesRequest:
			s.handleCapabilitiesRequest(conn, clientID, req)
		case server.WSMessageTypeTransceiveRequest:
			s.countOperation(conn, "transceive")
			s.handleTransceiveRequest(conn, clientID, req)
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
		RequestID:       requestID,
		ClientID:        clientID,
		Request:         writeReq,
		TargetDevice:    writeReq.DeviceID,
		TagUID:          writeReq.UID,
		AllowUntargeted: writeReq.AllowUntargeted,
		// A client that wants a retry deduplicated supplies a stable key. The
		// request ID stands in, which dedupes when the client reuses that too.
		IdempotencyKey: firstNonEmpty(writeReq.IdempotencyKey, requestID),
		ResponseCh:     make(chan server.WriteResponseMessage, 1),
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

	target := tagTarget(req.Payload)

	msg := server.LockRequestMessage{
		RequestID:       requestID,
		ClientID:        clientID,
		TargetDevice:    target.DeviceID,
		TagUID:          target.UID,
		AllowUntargeted: target.AllowUntargeted,
		IdempotencyKey:  firstNonEmpty(target.IdempotencyKey, requestID),
		ResponseCh:      make(chan server.LockResponseMessage, 1),
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

// handleTransceiveRequest exchanges raw bytes with the present tag.
//
// The command arrives base64-encoded, matching how the device protocol carries
// byte slices, so a client builds it the same way at both ends.
func (s *Server) handleTransceiveRequest(conn *server.SafeConn, clientID string, req protocol.WebSocketRequest) {
	requestID := req.ID
	if requestID == "" {
		requestID = uuid.New().String()
	}

	var payload struct {
		Data            string `json:"data"`
		Raw             bool   `json:"raw"`
		DeviceID        string `json:"deviceID"`
		UID             string `json:"uid"`
		AllowUntargeted bool   `json:"allowUntargeted"`
	}
	payloadBytes, err := json.Marshal(req.Payload)
	if err == nil {
		err = json.Unmarshal(payloadBytes, &payload)
	}
	if err != nil {
		s.sendErrorResponse(conn, req.ID, protocol.ErrCodeInvalidRequest, "Invalid transceive request")
		return
	}

	data, err := base64.StdEncoding.DecodeString(payload.Data)
	if err != nil {
		s.sendErrorResponse(conn, req.ID, protocol.ErrCodeInvalidRequest, "data must be base64")
		return
	}
	if len(data) == 0 {
		s.sendErrorResponse(conn, req.ID, protocol.ErrCodeInvalidRequest, "data is empty")
		return
	}

	response, err := s.bridge.SendTransceiveRequest(server.TransceiveRequestMessage{
		RequestID:       requestID,
		ClientID:        clientID,
		Data:            data,
		Raw:             payload.Raw,
		TargetDevice:    payload.DeviceID,
		TagUID:          payload.UID,
		AllowUntargeted: payload.AllowUntargeted,
		ResponseCh:      make(chan server.TransceiveResponseMessage, 1),
	})
	if err != nil {
		log.Printf("[client] Transceive request failed: %v", err)
		s.sendOperationError(conn, req.ID, protocol.ErrCodeTransceiveFailed, err)
		return
	}

	wsResponse := protocol.WebSocketResponse{
		ID:      req.ID,
		Type:    server.WSMessageTypeTransceiveResponse,
		Success: response.Success,
	}
	if response.Success {
		wsResponse.Payload = map[string]interface{}{
			"data": base64.StdEncoding.EncodeToString(response.Data),
		}
	} else {
		wsResponse.Error = response.Error
		wsResponse.Payload = errorPayloadOrDefault(response.ErrorCode, protocol.ErrCodeTransceiveFailed)
	}

	if err := conn.WriteJSON(wsResponse); err != nil {
		log.Printf("[client] Failed to send transceive response: %v", err)
	}
}

// handleCapabilitiesRequest handles capabilities queries for the present tag.
func (s *Server) handleCapabilitiesRequest(conn *server.SafeConn, clientID string, req protocol.WebSocketRequest) {
	requestID := req.ID
	if requestID == "" {
		requestID = uuid.New().String()
	}

	msg := server.CapabilitiesRequestMessage{
		RequestID:       requestID,
		ClientID:        clientID,
		TargetDevice:    tagTarget(req.Payload).DeviceID,
		TagUID:          tagTarget(req.Payload).UID,
		AllowUntargeted: tagTarget(req.Payload).AllowUntargeted,
		ResponseCh:      make(chan server.CapabilitiesResponseMessage, 1),
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

// requestTarget is what a request that acts on a tag may carry alongside its
// own fields. A tagData broadcast reports deviceID, so a client watching two
// phones can name the one it means.
type requestTarget struct {
	DeviceID        string `json:"deviceID"`
	UID             string `json:"uid"`
	AllowUntargeted bool   `json:"allowUntargeted"`
	IdempotencyKey  string `json:"idempotencyKey"`
}

// tagTarget reads the target out of a request payload, tolerating its absence.
func tagTarget(payload map[string]any) requestTarget {
	var target requestTarget

	raw, err := json.Marshal(payload)
	if err != nil {
		return target
	}
	_ = json.Unmarshal(raw, &target)

	return target
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
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
			// Hand the scan to an in-process observer before the clients see
			// it. This is the supported way to read tags from Go: the bridge
			// channel has exactly one consumer -- this loop -- so a second
			// reader would take scans away from the browsers rather than
			// copying them.
			if s.config.OnTag != nil {
				s.config.OnTag(data)
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

		// Which reader this came from. Absent means the agent's own hardware,
		// which is the only source deviceStatus describes — so a client showing
		// a tag can tell whether that status has anything to say about it.
		if source, ok := data.Card.GetUnderlyingTag().(interface{ SourceDevice() string }); ok {
			if id := source.SourceDevice(); id != "" {
				payload["deviceID"] = id
			}
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
	payload := protocol.ErrorPayloadFor(err)
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
