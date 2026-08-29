// Package clientserver provides the WebSocket server for client applications.
package clientserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/event"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/wsconn"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// clientRequestQueue is how many requests one connection may have outstanding
// before the agent refuses with BUSY. Requests are served one at a time, so
// this bounds a client that asks faster than the reader can answer; blocking
// the read loop instead would hide the next disconnect.
const clientRequestQueue = 8

// clientSendQueue bounds how many broadcasts may be queued for one client before
// the agent starts dropping them for that client. Broadcasts are enqueued rather
// than written inline, so a client that stops reading its socket fills only its
// own queue — it cannot block the producer goroutine or the delivery to every
// other client. A client this far behind is not keeping up; newer scans
// supersede the dropped ones.
const clientSendQueue = 256

// Server handles client connections for consuming NFC data.
type Server struct {
	config Config

	// WebSocket upgrader
	upgrader websocket.Upgrader

	// Client connections (multiple allowed)
	clients    map[*wsconn.SafeConn]*clientSession
	clientsMux sync.RWMutex

	// What Config.Scans and Config.ReaderStatus were connected with, for Close
	// to take back.
	scans  *event.Connection
	status *event.Connection

	// changed carries the connected count after each connect and disconnect.
	changed event.Signal[int]
}

// New creates a client server, subscribed to whatever it was given to
// broadcast. Connections are served as they arrive; Close takes the
// subscriptions back.
func New(config Config) *Server {
	s := &Server{
		config:  config,
		clients: make(map[*wsconn.SafeConn]*clientSession),
		upgrader: websocket.Upgrader{
			CheckOrigin: originChecker(config),
		},
	}

	if config.Scans != nil {
		s.scans = config.Scans.Connect(s.Broadcast)
	}
	if config.ReaderStatus != nil {
		s.status = config.ReaderStatus.Connect(s.BroadcastDeviceStatus)
	}
	return s
}

// Close ends the subscriptions the server was built with. Connections already
// open are left alone: they are held by the listener, which closes them when it
// stops.
//
// Safe to call more than once, and on a server built with nothing to subscribe
// to.
func (s *Server) Close() {
	s.scans.Disconnect()
	s.status.Disconnect()
}

// apiSecret is the secret required right now, empty for a server admitting
// connections without one.
func (s *Server) apiSecret() string {
	if s.config.APISecret == nil {
		return ""
	}
	return s.config.APISecret()
}

// allowLoopbackBypass reports whether a connection from this host may skip the
// secret. False when no policy is configured.
func (s *Server) allowLoopbackBypass() bool {
	return s.config.AllowLoopbackBypass != nil && s.config.AllowLoopbackBypass()
}

// ServeHTTP upgrades a client connection. The server checks the API secret and
// the origin itself, so it is safe to mount directly on a shared listener.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handleWebSocket(w, r)
}

var _ http.Handler = (*Server)(nil)

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

	// cancel ends the operations this client asked for. Called when the
	// connection drops and by Disconnect.
	cancel context.CancelFunc

	// send carries broadcasts to this client's own writer goroutine, so a slow
	// client backs up only its own queue instead of the producer. A full queue is
	// dropped rather than blocked on.
	send chan protocol.WebSocketMessage
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
	var target *wsconn.SafeConn
	var cancel context.CancelFunc
	for conn, c := range s.clients {
		if c.id == clientID {
			target, cancel = conn, c.cancel
			break
		}
	}
	s.clientsMux.RUnlock()

	if target == nil {
		return false
	}

	clientLog.Printf("Disconnecting client %s at an operator's request", clientID[:8])
	if cancel != nil {
		cancel()
	}
	_ = target.Close()
	return true
}

// OnClientsChange calls fn with the connected count after each connect and
// disconnect, so an observer refreshes without polling. The connection it
// returns removes it.
//
// Handlers run on the connection's own goroutine, off the hot path, so one must
// not block.
func (s *Server) OnClientsChange(fn func(clients int)) *event.Connection {
	return s.changed.Connect(fn)
}

// notifyChange tells any observer that the client list moved.
func (s *Server) notifyChange() { s.changed.Emit(s.clientCount()) }

// countOperation records that a client issued a write or lock.
func (s *Server) countOperation(conn *wsconn.SafeConn, kind string) {
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

// clientCount returns the number of connected clients.
func (s *Server) clientCount() int {
	s.clientsMux.RLock()
	defer s.clientsMux.RUnlock()
	return len(s.clients)
}

// handleWebSocket handles WebSocket connections from clients.
func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	auth := server.AuthOptions{
		Secret:        s.apiSecret(),
		Verifier:      s.config.TokenVerifier,
		AllowLoopback: s.allowLoopbackBypass(),
	}
	if _, ok := server.CheckAuth(w, r, auth); !ok {
		clientWarn.Printf("WebSocket connection rejected from %s: bad/missing API secret", r.RemoteAddr)
		return
	}

	wsConn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		clientFail.Printf("WebSocket upgrade error: %v", err)
		return
	}

	conn := wsconn.NewSafeConn(wsConn)
	clientID := uuid.New().String()

	// Ends when the client goes away, so an operation still running is told
	// that whoever asked for it is gone.
	ctx, cancel := context.WithCancel(context.Background())

	// Add to clients map
	send := make(chan protocol.WebSocketMessage, clientSendQueue)
	s.clientsMux.Lock()
	s.clients[conn] = &clientSession{
		id:          clientID,
		origin:      r.Header.Get("Origin"),
		remoteAddr:  r.RemoteAddr,
		userAgent:   r.Header.Get("User-Agent"),
		connectedAt: time.Now(),
		cancel:      cancel,
		send:        send,
	}
	s.clientsMux.Unlock()
	s.notifyChange()

	// One writer goroutine per client drains its broadcast queue to the socket.
	// A stalled client blocks only this goroutine (and its own queue fills and
	// drops); it never blocks the producer or the other clients. It exits when
	// the connection is cancelled or the socket close unblocks a pending write.
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-send:
				if err := conn.WriteJSON(msg); err != nil {
					return
				}
			}
		}
	}()

	clientLog.Printf("Client connected: %s (total: %d)", clientID[:8], s.clientCount())

	defer func() {
		cancel()
		_ = conn.Close()
		s.clientsMux.Lock()
		delete(s.clients, conn)
		s.clientsMux.Unlock()
		clientLog.Printf("Client disconnected: %s (total: %d)", clientID[:8], s.clientCount())
		s.notifyChange()
	}()

	// Requests are dispatched on their own goroutine so that reading continues
	// while one is being served. Serving inline left the loop inside a handler
	// for the length of a tag operation, which is where a disconnect goes
	// unnoticed: nothing reads, so nothing sees EOF, and the operation runs to
	// completion for a client that is no longer there.
	requests := make(chan protocol.WebSocketRequest, clientRequestQueue)
	var dispatching sync.WaitGroup
	dispatching.Add(1)
	go func() {
		defer dispatching.Done()
		// One at a time, which is the order requests were served in before.
		for req := range requests {
			s.dispatch(ctx, conn, clientID, req)
		}
	}()

	// Registered after the cleanup above, so it runs first: cancelling before
	// waiting is what lets an operation still in flight return. Waiting for the
	// dispatch goroutine first would block on the very context this ends.
	defer func() {
		cancel()
		close(requests)
		dispatching.Wait()
	}()

	// Handle incoming messages
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				clientWarn.Printf("WebSocket read error: %v", err)
			}
			return
		}

		var req protocol.WebSocketRequest
		if err := json.Unmarshal(message, &req); err != nil {
			clientWarn.Printf("Failed to parse message: %v", err)
			s.sendErrorResponse(conn, "", protocol.ErrCodeParse, "Invalid message format")
			continue
		}

		select {
		case requests <- req:
		default:
			// The queue is what bounds a client that asks faster than the
			// reader can answer. Refusing is the backpressure; blocking here
			// would stop reading and hide the next disconnect.
			s.sendErrorResponse(conn, req.ID, protocol.ErrCodeBusy,
				"Too many requests outstanding on this connection; retry when the earlier ones answer")
		}
	}
}

// dispatch serves one request. Called from the connection's dispatch goroutine,
// one at a time.
func (s *Server) dispatch(ctx context.Context, conn *wsconn.SafeConn, clientID string, req protocol.WebSocketRequest) {
	switch req.Type {
	case server.WSMessageTypeWriteRequest:
		s.countOperation(conn, "write")
		s.handleWriteRequest(ctx, conn, clientID, req)
	case server.WSMessageTypeLockRequest:
		s.countOperation(conn, "lock")
		s.handleLockRequest(ctx, conn, clientID, req)
	case server.WSMessageTypeCapabilitiesRequest:
		s.handleCapabilitiesRequest(ctx, conn, clientID, req)
	case server.WSMessageTypeTransceiveRequest:
		s.countOperation(conn, "transceive")
		s.handleTransceiveRequest(ctx, conn, clientID, req)
	default:
		clientWarn.Printf("Unknown message type: %s", req.Type)
		s.sendErrorResponse(conn, req.ID, protocol.ErrCodeUnknownType, fmt.Sprintf("Unknown message type: %s", req.Type))
	}
}

// handleWriteRequest encodes a message onto the tag the client names.
func (s *Server) handleWriteRequest(ctx context.Context, conn *wsconn.SafeConn, clientID string, req protocol.WebSocketRequest) {
	var writeReq server.WriteRequest
	if !decodePayload(req.Payload, &writeReq) {
		s.sendErrorResponse(conn, req.ID, protocol.ErrCodeInvalidRequest, "Failed to parse write request")
		return
	}

	result, err := s.ops().Write(ctx, server.WriteOp{
		Target:  targetOf(writeReq.UID, writeReq.DeviceID, writeReq.AllowUntargeted),
		Request: writeReq,
		// A client that wants a retry deduplicated supplies a stable key. The
		// request ID stands in, which dedupes when the client reuses that too.
		IdempotencyKey: firstNonEmpty(writeReq.IdempotencyKey, req.ID),
	})
	if err != nil {
		s.sendOperationError(conn, req.ID, protocol.ErrCodeWriteFailed, err)
		return
	}

	// Surface the verified write outcome so clients can confirm the data
	// actually landed, how many attempts it took, and the size.
	payload := map[string]any{"message": "Write operation completed successfully"}
	if result != nil {
		payload["uid"] = result.UID
		payload["tagType"] = result.TagType
		payload["bytesWritten"] = result.BytesWritten
		payload["verified"] = result.Verified
		payload["attempts"] = result.Attempts
		payload["locked"] = result.Locked
	}

	s.reply(conn, req.ID, server.WSMessageTypeWriteResponse, payload)
}

// handleLockRequest makes the named tag permanently read-only.
func (s *Server) handleLockRequest(ctx context.Context, conn *wsconn.SafeConn, clientID string, req protocol.WebSocketRequest) {
	target := tagTarget(req.Payload)

	result, err := s.ops().Lock(ctx, server.LockOp{
		Target:         targetOf(target.UID, target.DeviceID, target.AllowUntargeted),
		IdempotencyKey: firstNonEmpty(target.IdempotencyKey, req.ID),
	})
	if err != nil {
		s.sendOperationError(conn, req.ID, protocol.ErrCodeLockFailed, err)
		return
	}

	s.reply(conn, req.ID, server.WSMessageTypeLockResponse, result)
}

// handleTransceiveRequest exchanges raw bytes with the named tag.
func (s *Server) handleTransceiveRequest(ctx context.Context, conn *wsconn.SafeConn, clientID string, req protocol.WebSocketRequest) {
	var payload struct {
		Data            string `json:"data"`
		Raw             bool   `json:"raw"`
		DeviceID        string `json:"deviceID"`
		UID             string `json:"uid"`
		AllowUntargeted bool   `json:"allowUntargeted"`
	}
	if !decodePayload(req.Payload, &payload) {
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

	resp, err := s.ops().Transceive(ctx, server.TransceiveOp{
		Target: targetOf(payload.UID, payload.DeviceID, payload.AllowUntargeted),
		Data:   data,
		Raw:    payload.Raw,
	})
	if err != nil {
		s.sendOperationError(conn, req.ID, protocol.ErrCodeTransceiveFailed, err)
		return
	}

	s.reply(conn, req.ID, server.WSMessageTypeTransceiveResponse, map[string]any{
		"data": base64.StdEncoding.EncodeToString(resp),
	})
}

// handleCapabilitiesRequest reports what the named tag supports.
func (s *Server) handleCapabilitiesRequest(ctx context.Context, conn *wsconn.SafeConn, clientID string, req protocol.WebSocketRequest) {
	target := tagTarget(req.Payload)

	caps, err := s.ops().Capabilities(ctx, server.CapabilitiesOp{
		Target: targetOf(target.UID, target.DeviceID, target.AllowUntargeted),
	})
	if err != nil {
		s.sendOperationError(conn, req.ID, protocol.ErrCodeCapabilitiesFailed, err)
		return
	}

	s.reply(conn, req.ID, server.WSMessageTypeCapabilitiesResponse, caps)
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

// Broadcast serves a scan to every connected client. Called by whatever
// produced it, which decides what is worth serving.
func (s *Server) Broadcast(data nfc.NFCData) {
	s.broadcastTagData(data)
}

// BroadcastDeviceStatus tells every client what the agent's own reader is
// doing. It describes that reader and nothing else: a tag a phone is holding is
// unaffected by it.
func (s *Server) BroadcastDeviceStatus(status nfc.DeviceStatus) {
	s.broadcastDeviceStatus(status)
}

// broadcastTagData queues tag data for every connected client. The message is
// built once and enqueued to each client's own writer goroutine, so a client
// that has stopped reading backs up only its own queue and never stalls the
// producer or the delivery to the others.
func (s *Server) broadcastTagData(data nfc.NFCData) {
	s.enqueueToAll(tagDataMessage(data))
}

// enqueueToAll offers msg to every client's send queue without blocking. A full
// queue means that client is not keeping up; the message is dropped for it
// rather than stalling everyone else.
func (s *Server) enqueueToAll(msg protocol.WebSocketMessage) {
	s.clientsMux.RLock()
	defer s.clientsMux.RUnlock()

	for _, c := range s.clients {
		select {
		case c.send <- msg:
		default:
			// This client's queue is full; drop for it. Newer scans supersede it.
		}
	}
}

// tagDataMessage builds the broadcast message for a scan. It does not depend on
// which client receives it, so it is built once per broadcast.
func tagDataMessage(data nfc.NFCData) protocol.WebSocketMessage {
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
		// which is the only source deviceStatus describes, so a client showing
		// a tag can tell whether that status has anything to say about it.
		if source, ok := data.Card.GetUnderlyingTag().(interface{ SourceDevice() string }); ok {
			if id := source.SourceDevice(); id != "" {
				payload["deviceID"] = id
			}
		}

		// The optical symbology, when this scan came from a camera rather than
		// an NFC field. Absent for an NFC tag, so a client can tell a scanned
		// QR or barcode from a chip on the same feed.
		if optical, ok := data.Card.GetUnderlyingTag().(interface{ Format() string }); ok {
			if f := optical.Format(); f != "" {
				payload["format"] = f
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

	return protocol.WebSocketMessage{
		Type:    server.WSMessageTypeTagData,
		Payload: payload,
	}
}

// broadcastDeviceStatus queues a device-status update for every connected
// client, through the same per-client queues as tag data.
func (s *Server) broadcastDeviceStatus(status nfc.DeviceStatus) {
	s.enqueueToAll(protocol.WebSocketMessage{
		Type:    server.WSMessageTypeDeviceStatus,
		Payload: status,
	})
}

// sendErrorResponse sends an error response to a WebSocket client.
func (s *Server) sendErrorResponse(conn *wsconn.SafeConn, requestID string, errorCode protocol.ErrorCode, message string) {
	response := protocol.WebSocketResponse{
		ID:      requestID,
		Type:    server.WSMessageTypeError,
		Success: false,
		Error:   message,
		Payload: protocol.NewErrorPayload(errorCode),
	}

	if err := conn.WriteJSON(response); err != nil {
		clientFail.Printf("Failed to send error response: %v", err)
	}
}

// sendOperationError reports a failed tag operation, keeping the code the
// underlying NFCError carried rather than flattening every failure to one
// per-operation label. A client can then tell "present the tag again" from
// "this tag is locked".
func (s *Server) sendOperationError(conn *wsconn.SafeConn, requestID string, fallback protocol.ErrorCode, err error) {
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
		clientFail.Printf("Failed to send error response: %v", writeErr)
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

// originChecker prefers an explicit policy over the static allowlist, so an
// origin allowed while the agent runs is admitted without anything being
// rebuilt.
func originChecker(config Config) func(r *http.Request) bool {
	if config.OriginPolicy != nil {
		return server.CheckOriginPolicy(config.OriginPolicy)
	}
	return server.CheckOrigin(config.AllowedOrigins)
}
