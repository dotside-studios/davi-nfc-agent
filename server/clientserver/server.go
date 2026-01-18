// Package clientserver provides the WebSocket server for client applications.
package clientserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
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

	httpServer *http.Server
	ctx        context.Context
	cancel     context.CancelFunc

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
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}
}

// Start starts the client server.
func (s *Server) Start() error {
	log.Printf("[client] Starting Client Server on port %d...", s.config.Port)

	// Create context
	s.ctx, s.cancel = context.WithCancel(context.Background())

	// Set up HTTP routes
	mux := http.NewServeMux()

	// WebSocket endpoint for clients
	mux.HandleFunc("/ws", s.enableCORS(func(w http.ResponseWriter, r *http.Request) {
		s.handleWebSocket(w, r)
	}))

	// Health check
	mux.HandleFunc("/api/v1/health", s.enableCORS(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodOptions {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "ok",
			"type":      "client",
			"timestamp": time.Now().Format("2006-01-02T15:04:05Z07:00"),
			"clients":   s.clientCount(),
		})
	}))

	// Root
	mux.HandleFunc("/", s.enableCORS(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("NFC Client Server"))
	}))

	// Create HTTP server
	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.config.Port),
		Handler: mux,
	}

	// Start HTTP server in goroutine
	go func() {
		var err error
		if s.config.TLSEnabled() {
			log.Printf("[client] Listening on :%d (TLS)", s.config.Port)
			err = s.httpServer.ListenAndServeTLS(s.config.CertFile, s.config.KeyFile)
		} else {
			log.Printf("[client] Listening on :%d", s.config.Port)
			err = s.httpServer.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			log.Printf("[client] HTTP server error: %v", err)
		}
	}()

	// Start bridge listeners
	go s.listenBridgeTagData()
	go s.listenBridgeDeviceStatus()

	// Block until shutdown
	<-s.ctx.Done()
	log.Printf("[client] Server context cancelled, shutting down...")

	return nil
}

// Stop stops the client server.
func (s *Server) Stop() {
	if s.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.httpServer.Shutdown(ctx)
	}

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
	// Validate optional API secret if configured
	if s.config.APISecret != "" {
		secret := r.URL.Query().Get("secret")
		if secret != s.config.APISecret {
			log.Printf("[client] WebSocket connection rejected: invalid API secret")
			http.Error(w, "Unauthorized: Invalid API secret", http.StatusUnauthorized)
			return
		}
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

	// Send last card data if available
	s.cardMu.RLock()
	lastCard := s.lastCard
	s.cardMu.RUnlock()
	if lastCard != nil {
		s.sendTagDataToClient(conn, nfc.NFCData{Card: lastCard})
	}

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
			s.sendErrorResponse(conn, "", "PARSE_ERROR", "Invalid message format")
			continue
		}

		// Handle message types
		switch req.Type {
		case server.WSMessageTypeWriteRequest:
			s.handleWriteRequest(conn, clientID, req)
		default:
			log.Printf("[client] Unknown message type: %s", req.Type)
			s.sendErrorResponse(conn, req.ID, "UNKNOWN_TYPE", fmt.Sprintf("Unknown message type: %s", req.Type))
		}
	}
}

// handleWriteRequest handles write requests from clients.
func (s *Server) handleWriteRequest(conn *server.SafeConn, clientID string, req protocol.WebSocketRequest) {
	// Parse write request from payload
	payloadBytes, err := json.Marshal(req.Payload)
	if err != nil {
		log.Printf("[client] Failed to marshal write request payload: %v", err)
		s.sendErrorResponse(conn, req.ID, "INVALID_PAYLOAD", "Invalid write request payload")
		return
	}

	var writeReq server.WriteRequest
	if err := json.Unmarshal(payloadBytes, &writeReq); err != nil {
		log.Printf("[client] Failed to parse write request: %v", err)
		s.sendErrorResponse(conn, req.ID, "INVALID_WRITE_REQUEST", "Failed to parse write request")
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
		s.sendErrorResponse(conn, req.ID, "WRITE_FAILED", err.Error())
		return
	}

	// Send response to client
	wsResponse := protocol.WebSocketResponse{
		ID:      req.ID,
		Type:    server.WSMessageTypeWriteResponse,
		Success: response.Success,
	}
	if response.Success {
		wsResponse.Payload = map[string]interface{}{
			"message": "Write operation completed successfully",
		}
	} else {
		wsResponse.Error = response.Error
		wsResponse.Payload = map[string]interface{}{
			"code": "WRITE_FAILED",
		}
	}

	if err := conn.WriteJSON(wsResponse); err != nil {
		log.Printf("[client] Failed to send write response: %v", err)
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
			"uid":        data.Card.UID,
			"type":       data.Card.Type,
			"technology": data.Card.Technology,
			"scannedAt":  data.Card.ScannedAt.Format("2006-01-02T15:04:05Z07:00"),
			"err":        errStr,
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
func (s *Server) sendErrorResponse(conn *server.SafeConn, requestID string, errorCode string, message string) {
	response := protocol.WebSocketResponse{
		ID:      requestID,
		Type:    server.WSMessageTypeError,
		Success: false,
		Error:   message,
		Payload: map[string]interface{}{
			"code": errorCode,
		},
	}

	if err := conn.WriteJSON(response); err != nil {
		log.Printf("[client] Failed to send error response: %v", err)
	}
}

// enableCORS adds CORS headers.
func (s *Server) enableCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next(w, r)
	}
}
