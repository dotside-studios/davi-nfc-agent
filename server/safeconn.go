// Package server provides shared server utilities.
package server

import (
	"sync"

	"github.com/gorilla/websocket"
)

// SafeConn wraps a websocket.Conn with a mutex to prevent concurrent writes.
// The gorilla/websocket library does not support concurrent writes to the same
// connection, so all writes must be serialized.
type SafeConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

// NewSafeConn creates a new SafeConn wrapping the given websocket connection.
func NewSafeConn(conn *websocket.Conn) *SafeConn {
	return &SafeConn{conn: conn}
}

// WriteJSON writes a JSON message to the connection in a thread-safe manner.
func (sc *SafeConn) WriteJSON(v any) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.conn.WriteJSON(v)
}

// WriteMessage writes a message to the connection in a thread-safe manner.
func (sc *SafeConn) WriteMessage(messageType int, data []byte) error {
	sc.mu.Lock()
	defer sc.mu.Unlock()
	return sc.conn.WriteMessage(messageType, data)
}

// ReadMessage reads a message from the connection.
// Reading does not need synchronization as only one goroutine reads per connection.
func (sc *SafeConn) ReadMessage() (int, []byte, error) {
	return sc.conn.ReadMessage()
}

// Close closes the underlying connection.
func (sc *SafeConn) Close() error {
	return sc.conn.Close()
}

// Conn returns the underlying websocket connection.
// Use with caution - direct access bypasses synchronization.
func (sc *SafeConn) Conn() *websocket.Conn {
	return sc.conn
}
