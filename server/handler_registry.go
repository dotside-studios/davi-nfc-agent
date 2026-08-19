package server

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/gorilla/websocket"
)

// HandlerFunc is a function type for handling websocket messages.
// It processes a websocket request and returns an error if processing fails.
type HandlerFunc func(ctx context.Context, conn *websocket.Conn, req protocol.WebSocketRequest) error

// WebSocketHandlerFunc is a function type for custom WebSocket connection handling.
// It takes over the entire WebSocket connection lifecycle when matched.
// Returns true if the connection was handled, false to continue with default handling.
type WebSocketHandlerFunc func(w http.ResponseWriter, r *http.Request) bool

// HandlerServer provides methods for handlers to register routes and start lifecycle processes.
// It also provides broadcast methods for sending data to connected clients.
type HandlerServer interface {
	// Handle registers a handler function for a specific message type
	Handle(messageType string, handler HandlerFunc) error

	// HandleWebSocket registers a custom WebSocket handler that intercepts connections
	// before normal message routing. The matcher function determines if this handler
	// should process the connection.
	HandleWebSocket(matcher func(r *http.Request) bool, handler WebSocketHandlerFunc)

	// StartLifecycle registers a function to be called when the server starts
	StartLifecycle(start func(ctx context.Context))

	// Broadcast methods for NFC data
	BroadcastTagData(data nfc.NFCData)
	BroadcastDeviceStatus(status nfc.DeviceStatus)
}

// wsHandlerEntry represents a custom WebSocket handler with its matcher.
type wsHandlerEntry struct {
	matcher func(r *http.Request) bool
	handler WebSocketHandlerFunc
}

// HandlerRegistry manages websocket message handlers using a router-style approach.
// It provides thread-safe registration and retrieval of handler functions by message type.
type HandlerRegistry struct {
	handlers          map[string]HandlerFunc
	wsHandlers        []wsHandlerEntry
	lifecycleStarters []func(ctx context.Context)
	mu                sync.RWMutex
}

// NewHandlerRegistry creates a new handler registry.
func NewHandlerRegistry() *HandlerRegistry {
	return &HandlerRegistry{
		handlers: make(map[string]HandlerFunc),
	}
}

// Handle registers a handler function for a specific message type.
// This is the router-style registration method.
// Returns an error if a handler for the same message type is already registered.
func (r *HandlerRegistry) Handle(messageType string, handler HandlerFunc) error {
	if handler == nil {
		return fmt.Errorf("handler cannot be nil")
	}

	if messageType == "" {
		return fmt.Errorf("message type cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.handlers[messageType]; exists {
		return fmt.Errorf("handler for message type '%s' already registered", messageType)
	}

	r.handlers[messageType] = handler
	return nil
}

// RegisterLifecycle registers a lifecycle function to be called when the server starts.
func (r *HandlerRegistry) RegisterLifecycle(start func(ctx context.Context)) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.lifecycleStarters = append(r.lifecycleStarters, start)
}

// HandleWebSocket registers a custom WebSocket handler with a matcher function.
func (r *HandlerRegistry) HandleWebSocket(matcher func(r *http.Request) bool, handler WebSocketHandlerFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.wsHandlers = append(r.wsHandlers, wsHandlerEntry{
		matcher: matcher,
		handler: handler,
	})
}

// TryCustomWebSocketHandler attempts to handle the request with registered custom handlers.
// Returns true if a handler processed the connection, false otherwise.
func (r *HandlerRegistry) TryCustomWebSocketHandler(w http.ResponseWriter, req *http.Request) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, entry := range r.wsHandlers {
		if entry.matcher(req) {
			return entry.handler(w, req)
		}
	}
	return false
}

// Get retrieves a handler function by message type.
// Returns the handler and true if found, nil and false otherwise.
func (r *HandlerRegistry) Get(messageType string) (HandlerFunc, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	handler, ok := r.handlers[messageType]
	return handler, ok
}

// Has checks if a handler exists for the given message type.
func (r *HandlerRegistry) Has(messageType string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, ok := r.handlers[messageType]
	return ok
}

// MessageTypes returns all registered message types.
func (r *HandlerRegistry) MessageTypes() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	types := make([]string, 0, len(r.handlers))
	for t := range r.handlers {
		types = append(types, t)
	}
	return types
}

// StartLifecycleHandlers starts all registered lifecycle functions.
func (r *HandlerRegistry) StartLifecycleHandlers(ctx context.Context) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, starter := range r.lifecycleStarters {
		starter(ctx)
	}
}
