// Package wsconn provides a write-safe wrapper around a WebSocket connection.
package wsconn

import (
	"sync"

	"github.com/gorilla/websocket"
)

// SafeConn serializes writes to a websocket.Conn. gorilla/websocket does not
// allow concurrent writes to one connection.
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

// Conn returns the underlying connection. Direct access bypasses the write
// lock, so use it only for reads and connection state.
func (sc *SafeConn) Conn() *websocket.Conn {
	return sc.conn
}
