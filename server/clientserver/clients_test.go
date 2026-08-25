package clientserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// dial connects a client to a test server and waits for it to be registered.
func dial(t *testing.T, s *Server, origin string) *websocket.Conn {
	t.Helper()

	ts := httptest.NewServer(http.HandlerFunc(s.ServeWS))
	t.Cleanup(ts.Close)

	header := http.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}
	header.Set("User-Agent", "Go-http-client/1.1 test")

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), header)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	waitFor(t, func() bool { return s.ClientCount() > 0 })
	return conn
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not met within 2s")
}

func newTestServer(onChange func(clients int)) *Server {
	// No origin policy and no secret: the test dials from an ephemeral port, and
	// what is under test is the session bookkeeping, not admission.
	return New(Config{AllowedOrigins: []string{"*"}, OnChange: onChange})
}

func TestClientsReportsConnectionDetail(t *testing.T) {
	s := newTestServer(nil)
	dial(t, s, "https://app.example.com")

	clients := s.Clients()
	if len(clients) != 1 {
		t.Fatalf("got %d clients, want 1", len(clients))
	}

	c := clients[0]
	if c.Origin != "https://app.example.com" {
		t.Errorf("origin = %q", c.Origin)
	}
	if c.RemoteAddr == "" {
		t.Error("remoteAddr empty")
	}
	if !strings.Contains(c.UserAgent, "Go-http-client") {
		t.Errorf("userAgent = %q", c.UserAgent)
	}
	if c.ID == "" {
		t.Error("id empty")
	}
	if time.Since(c.ConnectedAt) > time.Minute {
		t.Errorf("connectedAt = %v", c.ConnectedAt)
	}
	if c.Writes != 0 || c.Locks != 0 {
		t.Errorf("fresh client has writes=%d locks=%d, want 0", c.Writes, c.Locks)
	}
}

// A client with no Origin is a non-browser caller, which is legitimate and must
// still be listed rather than dropped.
func TestClientWithoutOriginIsStillListed(t *testing.T) {
	s := newTestServer(nil)
	dial(t, s, "")

	clients := s.Clients()
	if len(clients) != 1 {
		t.Fatalf("got %d clients, want 1", len(clients))
	}
	if clients[0].Origin != "" {
		t.Errorf("origin = %q, want empty", clients[0].Origin)
	}
}

func TestDisconnectClosesTheConnection(t *testing.T) {
	s := newTestServer(nil)
	dial(t, s, "https://app.example.com")

	id := s.Clients()[0].ID
	if !s.Disconnect(id) {
		t.Fatal("Disconnect reported no such client")
	}

	waitFor(t, func() bool { return s.ClientCount() == 0 })
}

func TestDisconnectUnknownClient(t *testing.T) {
	s := newTestServer(nil)
	if s.Disconnect("not-a-real-id") {
		t.Error("Disconnect reported success for an unknown client")
	}
}

// Counted per connection, so a client that only listens is distinguishable from
// one changing tags.
//
// One connection per operation: a request occupies the connection's read loop
// until the bridge answers it, and nothing is reading the bridge here, so a
// second request on the same socket would never be dispatched.
func TestWriteAndLockAreCountedPerClient(t *testing.T) {
	s := newTestServer(nil)

	writer := dial(t, s, "https://writer.example.com")
	_ = writer.WriteJSON(map[string]any{
		"type":    "writeRequest",
		"payload": map[string]any{"records": []map[string]any{{"type": "text", "content": "x"}}},
	})

	locker := dial(t, s, "https://locker.example.com")
	_ = locker.WriteJSON(map[string]any{"type": "lockRequest", "payload": map[string]any{}})

	waitFor(t, func() bool {
		byOrigin := map[string]ClientInfo{}
		for _, c := range s.Clients() {
			byOrigin[c.Origin] = c
		}
		return byOrigin["https://writer.example.com"].Writes == 1 &&
			byOrigin["https://locker.example.com"].Locks == 1
	})

	// The counts belong to the connection that issued them, not to all of them.
	for _, c := range s.Clients() {
		if c.Origin == "https://writer.example.com" && c.Locks != 0 {
			t.Errorf("writer has locks=%d, want 0", c.Locks)
		}
		if c.Origin == "https://locker.example.com" && c.Writes != 0 {
			t.Errorf("locker has writes=%d, want 0", c.Writes)
		}
	}
}

func TestOnChangeReportsTheCountOnConnectAndDisconnect(t *testing.T) {
	calls := make(chan int, 8)
	s := newTestServer(func(clients int) { calls <- clients })

	dial(t, s, "https://app.example.com")
	select {
	case got := <-calls:
		if got != 1 {
			t.Errorf("OnChange reported %d clients on connect, want 1", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnChange did not fire on connect")
	}

	s.Disconnect(s.Clients()[0].ID)
	select {
	case got := <-calls:
		if got != 0 {
			t.Errorf("OnChange reported %d clients on disconnect, want 0", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("OnChange did not fire on disconnect")
	}
}

func TestClientsSortedNewestFirst(t *testing.T) {
	s := newTestServer(nil)
	dial(t, s, "https://first.example.com")
	time.Sleep(10 * time.Millisecond)
	dial(t, s, "https://second.example.com")
	waitFor(t, func() bool { return s.ClientCount() == 2 })

	clients := s.Clients()
	if clients[0].Origin != "https://second.example.com" {
		t.Errorf("first row = %q, want the most recent connection", clients[0].Origin)
	}
}
