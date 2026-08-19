// Package deviceserver provides the WebSocket server for NFC readers and devices.
package deviceserver

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"sync/atomic"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/gorilla/websocket"
)

// Server handles device connections and tag data input.
type Server struct {
	config Config
	bridge *server.ServerBridge

	ctx    context.Context
	cancel context.CancelFunc

	// Handler registry for device message types
	handlerRegistry *server.HandlerRegistry

	// WebSocket upgrader
	upgrader websocket.Upgrader

	// Device connections (phones, etc.)
	devices    map[*websocket.Conn]string // conn -> deviceID
	devicesMux sync.RWMutex

	// deviceHandler serves remote devices; nil when none are configured.
	deviceHandler *DeviceHandler

	// requirePaired is read on every upgrade and settable at runtime, so the
	// policy can be tried without restarting the agent.
	requirePaired atomic.Bool
}

// SetRequirePairedDevice turns the paired-device requirement on or off while
// the agent runs.
func (s *Server) SetRequirePairedDevice(on bool) {
	s.requirePaired.Store(on)
}

// RequirePairedDevice reports whether only paired devices are admitted.
func (s *Server) RequirePairedDevice() bool {
	return s.requirePaired.Load()
}

// New creates a new device server instance.
func New(config Config, bridge *server.ServerBridge) *Server {
	s := &Server{
		config:  config,
		bridge:  bridge,
		devices: make(map[*websocket.Conn]string),
		upgrader: websocket.Upgrader{
			CheckOrigin: originChecker(config),
		},
		handlerRegistry: server.NewHandlerRegistry(),
	}

	s.requirePaired.Store(config.RequirePairedDevice)

	// Register NFC reader handlers (hardware NFC)
	if config.Reader != nil {
		nfcHandler := NewNFCHandler(config.Reader, config.AllowedCardTypes, bridge)
		nfcHandler.Register(s)
	}

	// Register device handler (external devices like phones)
	if config.DeviceManager != nil {
		s.deviceHandler = NewDeviceHandler(config.DeviceManager, bridge, config)
		s.deviceHandler.Register(s)

		// Give tags produced by the manager a route back to their device, so
		// they can report and perform writes rather than looking read-only.
		config.DeviceManager.SetTagWriter(s.deviceHandler)
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

// StartBackground starts the device-side background work — the NFC reader,
// lifecycle handlers, and the write/lock/capabilities bridge consumers — under
// the given parent context, without binding an HTTP listener. It returns
// immediately once the goroutines are running.
//
// The unified server owns the HTTP listener and routes device WebSocket
// connections here via ServeWS.
func (s *Server) StartBackground(ctx context.Context) {
	// Derive a cancelable context so Stop can tear the goroutines down even
	// when the parent context outlives this server.
	s.ctx, s.cancel = context.WithCancel(ctx)

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

	// Start reader
	if reader != nil {
		reader.Start()
	}

	// Start lifecycle handlers
	s.handlerRegistry.StartLifecycleHandlers(s.ctx)

	// Start bridge request consumers
	go s.handleWriteRequests()
	go s.handleLockRequests()
	go s.handleTransceiveRequests()
	go s.handleCapabilitiesRequests()
}

// ServeWS handles a WebSocket connection request for a device. It performs its
// own API-secret check and origin validation, so it is safe to call directly
// from a shared listener (unified single-port mode).
func (s *Server) ServeWS(w http.ResponseWriter, r *http.Request) {
	s.handleWebSocket(w, r)
}

// Stop stops the device server's background work. The unified server owns the
// HTTP listener; this only cancels the context the background goroutines run
// under.
func (s *Server) Stop() {
	if s.cancel != nil {
		s.cancel()
	}
}

// handleWebSocket handles WebSocket connections from devices.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if s.requirePaired.Load() {
		if !server.CheckPairedDevice(w, r, s.config.TokenVerifier) {
			log.Printf("[device] Connection rejected from %s: no paired-device credential", r.RemoteAddr)
			return
		}
	} else if !server.CheckAuth(w, r, s.config.APISecret, s.config.TokenVerifier) {
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
	defer func() { _ = conn.Close() }()

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
//
// The hardware reader keeps priority while it actually holds a card, so mixed
// setups behave as they always have. Otherwise the write goes to whichever
// remote device is currently holding a tag.
func (s *Server) executeWriteRequest(msg server.WriteRequestMessage) {
	reader := s.config.Reader

	if reader == nil || !reader.GetDeviceStatus().CardPresent {
		if s.writeViaDevice(msg) {
			return
		}
	}

	if reader == nil {
		msg.ResponseCh <- server.WriteResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     "No NFC reader or device holding a tag",
			ErrorCode: protocol.ErrCodeNoCard,
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
			ErrorCode: protocol.ErrCodeInvalidRequest,
		}
		return
	}

	// Write to card with overwrite option. WriteMessageWithResult performs a
	// capacity check, retries on transient failures, and verifies the write by
	// reading it back, returning the verified outcome.
	result, err := reader.WriteMessageWithResult(ndefMsg, nfc.WriteOptions{
		Overwrite: true,
		Index:     -1,
		Lock:      msg.Request.Lock,
	})
	if err != nil {
		msg.ResponseCh <- server.WriteResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     err.Error(),
			ErrorCode: operationErrorCode(err, protocol.ErrCodeWriteFailed),
		}
		return
	}

	msg.ResponseCh <- server.WriteResponseMessage{
		RequestID: msg.RequestID,
		Success:   true,
		Payload:   result,
	}
}

// writeViaDevice routes a write to the remote device holding a tag. It reports
// whether it handled the request; false means no device was available and the
// caller should fall back to the hardware reader.
func (s *Server) writeViaDevice(msg server.WriteRequestMessage) bool {
	if s.deviceHandler == nil {
		return false
	}

	deviceID, uid, ok := s.deviceHandler.ActiveTagDevice()
	if !ok {
		return false
	}

	// Encode here so the device receives exactly the message the hardware path
	// would have written, rather than re-deriving it from records.
	ndefMsg, err := server.BuildNDEFMessage(msg.Request)
	if err != nil {
		msg.ResponseCh <- server.WriteResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     err.Error(),
			ErrorCode: operationErrorCode(err, protocol.ErrCodeDeviceGone),
		}
		return true
	}

	ndefBytes, err := ndefMsg.Encode()
	if err != nil {
		msg.ResponseCh <- server.WriteResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     err.Error(),
			ErrorCode: operationErrorCode(err, protocol.ErrCodeDeviceGone),
		}
		return true
	}

	resp, err := s.deviceHandler.WriteToDevice(deviceID, protocol.DeviceWriteRequest{
		RequestID:      msg.RequestID,
		TagUID:         uid,
		NDEFMessage:    server.BuildNDEFInput(msg.Request),
		NDEFBytes:      ndefBytes,
		Lock:           msg.Request.Lock,
		IdempotencyKey: msg.IdempotencyKey,
	})
	if err != nil {
		msg.ResponseCh <- server.WriteResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     err.Error(),
			ErrorCode: operationErrorCode(err, protocol.ErrCodeDeviceGone),
		}
		return true
	}

	msg.ResponseCh <- server.WriteResponseMessage{
		RequestID: msg.RequestID,
		Success:   resp.Success,
		Error:     resp.Error,
		ErrorCode: resp.ErrorCode,
		Payload:   map[string]any{"uid": uid, "deviceID": deviceID},
	}
	return true
}

// handleLockRequests listens for make-read-only requests from the client server.
func (s *Server) handleLockRequests() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case msg, ok := <-s.bridge.LockRequest:
			if !ok {
				return
			}
			s.executeLockRequest(msg)
		}
	}
}

// executeLockRequest executes a lock request from the client server.
func (s *Server) executeLockRequest(msg server.LockRequestMessage) {
	reader := s.config.Reader
	if reader == nil {
		msg.ResponseCh <- server.LockResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     "No NFC reader available",
			ErrorCode: protocol.ErrCodeNoCard,
		}
		return
	}

	result, err := reader.LockCard()
	if err != nil {
		msg.ResponseCh <- server.LockResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     err.Error(),
			ErrorCode: operationErrorCode(err, protocol.ErrCodeLockFailed),
		}
		return
	}

	msg.ResponseCh <- server.LockResponseMessage{
		RequestID: msg.RequestID,
		Success:   true,
		Payload:   result,
	}
}

// handleCapabilitiesRequests listens for capabilities queries from the client server.
func (s *Server) handleTransceiveRequests() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case msg, ok := <-s.bridge.Transceive:
			if !ok {
				return
			}
			s.executeTransceiveRequest(msg)
		}
	}
}

// executeTransceiveRequest exchanges raw bytes with whichever holds the tag,
// preferring a remote device when the hardware reader has no card — the same
// routing a write uses, so an APDU reaches the tag the operator is looking at.
func (s *Server) executeTransceiveRequest(msg server.TransceiveRequestMessage) {
	reader := s.config.Reader

	// Refused in read-only mode. A raw exchange cannot be assumed harmless —
	// the same call carries a SELECT and a write to a configuration page — so
	// it is treated as a write for the purposes of the mode.
	if reader != nil && reader.GetMode() == nfc.ModeReadOnly {
		msg.ResponseCh <- server.TransceiveResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     "Reader is in read-only mode; raw exchanges are refused because they can write",
			ErrorCode: protocol.ErrCodeReadOnly,
		}
		return
	}

	if reader == nil || !reader.GetDeviceStatus().CardPresent {
		if s.transceiveViaDevice(msg) {
			return
		}
	}

	if reader == nil {
		msg.ResponseCh <- server.TransceiveResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     "No NFC reader or device holding a tag",
			ErrorCode: protocol.ErrCodeNoCard,
		}
		return
	}

	resp, err := reader.Transceive(msg.Data)
	if err != nil {
		msg.ResponseCh <- server.TransceiveResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     err.Error(),
			ErrorCode: operationErrorCode(err, protocol.ErrCodeTransceiveFailed),
		}
		return
	}

	msg.ResponseCh <- server.TransceiveResponseMessage{
		RequestID: msg.RequestID,
		Success:   true,
		Data:      resp,
	}
}

// transceiveViaDevice routes an exchange to the remote device holding a tag. It
// reports whether it handled the request; false means no device was available
// and the caller should fall back to the hardware reader.
func (s *Server) transceiveViaDevice(msg server.TransceiveRequestMessage) bool {
	if s.deviceHandler == nil {
		return false
	}

	deviceID, uid, ok := s.deviceHandler.ActiveTagDevice()
	if !ok {
		return false
	}

	resp, err := s.deviceHandler.TransceiveWithDevice(deviceID, protocol.DeviceTransceiveRequest{
		RequestID: msg.RequestID,
		DeviceID:  deviceID,
		TagUID:    uid,
		Data:      msg.Data,
		Raw:       msg.Raw,
	})
	if err != nil {
		msg.ResponseCh <- server.TransceiveResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     err.Error(),
			ErrorCode: protocol.ErrCodeTransceiveFailed,
		}
		return true
	}
	if !resp.Success {
		code := resp.ErrorCode
		if code == "" {
			code = protocol.ErrCodeTransceiveFailed
		}
		msg.ResponseCh <- server.TransceiveResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     resp.Error,
			ErrorCode: code,
		}
		return true
	}

	msg.ResponseCh <- server.TransceiveResponseMessage{
		RequestID: msg.RequestID,
		Success:   true,
		Data:      resp.Data,
	}
	return true
}

func (s *Server) handleCapabilitiesRequests() {
	for {
		select {
		case <-s.ctx.Done():
			return
		case msg, ok := <-s.bridge.CapabilitiesRequest:
			if !ok {
				return
			}
			s.executeCapabilitiesRequest(msg)
		}
	}
}

// executeCapabilitiesRequest queries the present tag's capabilities.
func (s *Server) executeCapabilitiesRequest(msg server.CapabilitiesRequestMessage) {
	reader := s.config.Reader
	if reader == nil {
		msg.ResponseCh <- server.CapabilitiesResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     "No NFC reader available",
			ErrorCode: protocol.ErrCodeNoCard,
		}
		return
	}

	caps, err := reader.GetCapabilities()
	if err != nil {
		msg.ResponseCh <- server.CapabilitiesResponseMessage{
			RequestID: msg.RequestID,
			Success:   false,
			Error:     err.Error(),
			ErrorCode: operationErrorCode(err, protocol.ErrCodeCapabilitiesFailed),
		}
		return
	}

	msg.ResponseCh <- server.CapabilitiesResponseMessage{
		RequestID: msg.RequestID,
		Success:   true,
		Payload:   caps,
	}
}

// operationErrorCode classifies a reader failure, falling back to the
// operation's own label when the error carries no code of its own.
func operationErrorCode(err error, fallback protocol.ErrorCode) protocol.ErrorCode {
	if payload := protocol.ErrorPayloadFor(err); payload.Code != protocol.ErrCodeUnknownError {
		return payload.Code
	}
	return fallback
}

// originChecker prefers an explicit policy over the static allowlist, so the
// tray can admit an origin without restarting the listener.
func originChecker(config Config) func(r *http.Request) bool {
	if config.OriginPolicy != nil {
		return server.CheckOriginPolicy(config.OriginPolicy)
	}
	return server.CheckOrigin(config.AllowedOrigins)
}
