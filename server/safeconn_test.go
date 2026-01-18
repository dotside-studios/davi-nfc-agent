package server

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// TestSafeConnConcurrentWrites tests that concurrent writes don't panic.
func TestSafeConnConcurrentWrites(t *testing.T) {
	// Create a test websocket server
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	var serverConn *websocket.Conn
	serverReady := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Failed to upgrade: %v", err)
			return
		}
		serverConn = conn
		close(serverReady)

		// Keep reading messages to prevent blocking
		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				return
			}
		}
	}))
	defer server.Close()

	// Connect client
	wsURL := "ws" + server.URL[4:] // http -> ws
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer clientConn.Close()

	// Wait for server to be ready
	<-serverReady
	defer serverConn.Close()

	// Wrap the server connection in SafeConn
	safeConn := NewSafeConn(serverConn)

	// Spawn many goroutines that write concurrently
	// This would panic without synchronization
	numGoroutines := 50
	numWrites := 20
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Use a channel to track panics
	panicCh := make(chan any, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					panicCh <- r
				}
			}()

			for j := 0; j < numWrites; j++ {
				msg := map[string]any{
					"goroutine": id,
					"message":   j,
					"timestamp": time.Now().UnixNano(),
				}
				// This should not panic with SafeConn
				if err := safeConn.WriteJSON(msg); err != nil {
					// Connection closed is expected at test end
					return
				}
			}
		}(i)
	}

	// Wait for all goroutines
	wg.Wait()
	close(panicCh)

	// Check for panics
	for p := range panicCh {
		t.Errorf("Goroutine panicked: %v", p)
	}
}

// TestSafeConnWriteMessage tests WriteMessage method.
func TestSafeConnWriteMessage(t *testing.T) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	receivedMessages := make(chan []byte, 100)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Failed to upgrade: %v", err)
			return
		}
		defer conn.Close()

		// Keep reading messages from client
		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			receivedMessages <- msg
		}
	}))
	defer server.Close()

	wsURL := "ws" + server.URL[4:]
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer clientConn.Close()

	// Wrap the CLIENT connection in SafeConn (client writes to server)
	safeConn := NewSafeConn(clientConn)

	// Test WriteMessage from client to server
	testMsg := []byte("test message")
	if err := safeConn.WriteMessage(websocket.TextMessage, testMsg); err != nil {
		t.Errorf("WriteMessage failed: %v", err)
	}

	// Verify message was received by server
	select {
	case received := <-receivedMessages:
		if string(received) != string(testMsg) {
			t.Errorf("Expected %q, got %q", testMsg, received)
		}
	case <-time.After(time.Second):
		t.Error("Timeout waiting for message")
	}
}

// TestSafeConnReadMessage tests ReadMessage method.
func TestSafeConnReadMessage(t *testing.T) {
	upgrader := websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool { return true },
	}

	serverReady := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("Failed to upgrade: %v", err)
			return
		}
		defer conn.Close()
		close(serverReady)

		// Send a message to the client
		if err := conn.WriteMessage(websocket.TextMessage, []byte("hello from server")); err != nil {
			t.Errorf("Server write failed: %v", err)
		}
	}))
	defer server.Close()

	wsURL := "ws" + server.URL[4:]
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("Failed to connect: %v", err)
	}
	defer clientConn.Close()

	<-serverReady

	safeConn := NewSafeConn(clientConn)

	// Read the message
	msgType, data, err := safeConn.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage failed: %v", err)
	}

	if msgType != websocket.TextMessage {
		t.Errorf("Expected TextMessage, got %d", msgType)
	}

	if string(data) != "hello from server" {
		t.Errorf("Expected 'hello from server', got %q", data)
	}
}
