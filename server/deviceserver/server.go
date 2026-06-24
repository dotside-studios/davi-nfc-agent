// Package deviceserver provides the WebSocket server for NFC readers and devices.
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
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/gorilla/websocket"
	"github.com/grandcat/zeroconf"
)

// Server handles device connections and tag data input.
type Server struct {
	config Config
	bridge *server.ServerBridge

	httpServer *http.Server
	ctx        context.Context
	cancel     context.CancelFunc

	// Handler registry for device message types
	handlerRegistry *server.HandlerRegistry

	// WebSocket upgrader
	upgrader websocket.Upgrader

	// mDNS service for auto-discovery
	mdnsServer *zeroconf.Server

	// Device connections (phones, etc.)
	devices    map[*websocket.Conn]string // conn -> deviceID
	devicesMux sync.RWMutex
}

// New creates a new device server instance.
func New(config Config, bridge *server.ServerBridge) *Server {
	s := &Server{
		config:  config,
		bridge:  bridge,
		devices: make(map[*websocket.Conn]string),
		upgrader: websocket.Upgrader{
			CheckOrigin: server.CheckOrigin(config.AllowedOrigins),
		},
		handlerRegistry: server.NewHandlerRegistry(),
	}

	// Register NFC reader handlers (hardware NFC)
	if config.Reader != nil {
		nfcHandler := NewNFCHandler(config.Reader, config.AllowedCardTypes, bridge)
		nfcHandler.Register(s)
	}

	// Register device handler (external devices like phones)
	if config.DeviceManager != nil {
		deviceHandler := NewDeviceHandler(config.DeviceManager, bridge, config.AllowedOrigins)
		deviceHandler.Register(s)
	}

	return s
}

// Handle implements server.HandlerServer interface.
func (s *Server) Handle(messageType string, handler server.HandlerFunc) error {
	return s.handlerRegistry.Handle(messageType, handler)
}

// StartLifecycle implements server.HandlerServer interface.
func (s *Server) StartLifecycle(start func(ctx context.Context)) {
	s.handlerRegistry.RegisterLifecycle(start)
}

// HandleWebSocket implements server.HandlerServer interface.
func (s *Server) HandleWebSocket(matcher func(r *http.Request) bool, handler server.WebSocketHandlerFunc) {
	s.handlerRegistry.HandleWebSocket(matcher, handler)
}

// BroadcastTagData sends tag data through the bridge to the client server.
func (s *Server) BroadcastTagData(data nfc.NFCData) {
	if !s.bridge.SendTagData(data) {
		log.Printf("[device] Warning: failed to send tag data to bridge (channel full or closed)")
	}
}

// BroadcastDeviceStatus sends device status through the bridge to the client server.
func (s *Server) BroadcastDeviceStatus(status nfc.DeviceStatus) {
	if !s.bridge.SendDeviceStatus(status) {
		log.Printf("[device] Warning: failed to send device status to bridge (channel full or closed)")
	}
}

// Start starts the device server.
func (s *Server) Start() error {
	log.Printf("[device] Starting Device Server on port %d...", s.config.Port)

	reader := s.config.Reader

	// Check device status
	if reader != nil {
		deviceStatus := reader.GetDeviceStatus()
		if deviceStatus.Connected {
			reader.LogDeviceInfo()
		} else {
			log.Printf("[device] No NFC device connected, waiting for device...")
		}
	}

	// Create context
	s.ctx, s.cancel = context.WithCancel(context.Background())

	// Set up HTTP routes
	mux := http.NewServeMux()

	// WebSocket endpoint for devices (bidirectional)
	mux.HandleFunc("/ws", s.enableCORS(func(w http.ResponseWriter, r *http.Request) {
		s.handleWebSocket(w, r)
	}))

	// Health check
	mux.HandleFunc("/health", s.enableCORS(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status": "ok",
			"type":   "device",
		})
	}))

	// Root
	mux.HandleFunc("/", s.enableCORS(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("NFC Device Server"))
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
			log.Printf("[device] Listening on :%d (TLS)", s.config.Port)
			err = s.httpServer.ListenAndServeTLS(s.config.CertFile, s.config.KeyFile)
		} else {
			log.Printf("[device] Listening on :%d", s.config.Port)
			err = s.httpServer.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			log.Printf("[device] HTTP server error: %v", err)
		}
	}()

	// Start mDNS service
	if err := s.startMDNS(); err != nil {
		log.Printf("[device] Warning: Failed to start mDNS: %v", err)
	}

	// Start reader
	if reader != nil {
		reader.Start()
	}

	// Start lifecycle handlers
	s.handlerRegistry.StartLifecycleHandlers(s.ctx)

	// Start write request handler
	go s.handleWriteRequests()

	// Block until shutdown
	<-s.ctx.Done()
	log.Printf("[device] Server context cancelled, shutting down...")

	return nil
}

// Stop stops the device server.
func (s *Server) Stop() {
	if s.mdnsServer != nil {
		s.mdnsServer.Shutdown()
		s.mdnsServer = nil
	}

	if s.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.httpServer.Shutdown(ctx)
	}

	if s.cancel != nil {
		s.cancel()
	}
}

// handleWebSocket handles WebSocket connections from devices.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if !server.CheckAPISecret(w, r, s.config.APISecret) {
		log.Printf("[device] WebSocket connection rejected from %s: bad/missing API secret", r.RemoteAddr)
		return
	}

	// Try custom handlers first (e.g., remotenfc)
	if s.handlerRegistry.TryCustomWebSocketHandler(w, r) {
		return
	}

	// Default device handling (if no custom handler matched)
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("[device] WebSocket upgrade error: %v", err)
		return
	}
	defer conn.Close()

	// Add to devices map
	s.devicesMux.Lock()
	s.devices[conn] = "" // Unknown device ID initially
	s.devicesMux.Unlock()

	defer func() {
		s.devicesMux.Lock()
		delete(s.devices, conn)
		s.devicesMux.Unlock()
	}()

	log.Printf("[device] Device connected")

	// Handle incoming messages
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				log.Printf("[device] WebSocket read error: %v", err)
			}
			break
		}

		var req protocol.WebSocketRequest
		if err := json.Unmarshal(message, &req); err != nil {
			log.Printf("[device] Failed to parse message: %v", err)
			continue
		}

		// Route to handler
		if handler, ok := s.handlerRegistry.Get(req.Type); ok {
			if err := handler(s.ctx, conn, req); err != nil {
				log.Printf("[device] Handler error for %s: %v", req.Type, err)
			}
		} else {
			log.Printf("[device] No handler for message type: %s", req.Type)
		}
	}
}

// handleWriteRequests listens for write requests from the client server.
func (s *Server) handleWriteRequests() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case msg, ok := <-s.bridge.WriteRequest:
			if !ok {
				return
			}
			s.executeWriteRequest(msg)
		}
	}
}

// executeWriteRequest executes a write request from the client server.
func (s *Server) executeWriteRequest(msg server.WriteRequestMessage) {
	reader := s.config.Reader
	if reader == nil {
		msg.ResponseCh <- server.WriteResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     "No NFC reader available",
		}
		return
	}

	// Build NDEF message
	ndefMsg, err := server.BuildNDEFMessage(msg.Request)
	if err != nil {
		msg.ResponseCh <- server.WriteResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     err.Error(),
		}
		return
	}

	// Write to card with overwrite option. WriteMessageWithResult performs a
	// capacity check, retries on transient failures, and verifies the write by
	// reading it back, returning the verified outcome.
	result, err := reader.WriteMessageWithResult(ndefMsg, nfc.WriteOptions{
		Overwrite: true,
		Index:     -1,
	})
	if err != nil {
		msg.ResponseCh <- server.WriteResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     err.Error(),
		}
		return
	}

	msg.ResponseCh <- server.WriteResponseMessage{
		RequestID: msg.RequestID,
		Success:   true,
		Payload:   result,
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

// startMDNS starts the mDNS service for auto-discovery.
func (s *Server) startMDNS() error {
	var err error
	s.mdnsServer, err = zeroconf.Register(
		server.MDNSDeviceServiceName,
		server.MDNSDeviceServiceType,
		server.MDNSDomain,
		s.config.Port,
		[]string{
			"version=1.0",
			"protocol=websocket",
			"path=/ws",
			"type=device",
		},
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to register mDNS service: %w", err)
	}
	log.Printf("[device] mDNS service registered: %s on port %d", server.MDNSDeviceServiceType, s.config.Port)
	return nil
}
