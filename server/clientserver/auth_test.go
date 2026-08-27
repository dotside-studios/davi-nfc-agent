package clientserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
)

// dialClient upgrades against a server with the given policy and reports the
// status. httptest binds 127.0.0.1, so every dial here is a loopback request.
func dialClient(t *testing.T, query string, allowLoopback bool) int {
	t.Helper()

	s := New(Config{
		APISecret:           func() string { return "shared-secret" },
		AllowLoopbackBypass: func() bool { return allowLoopback },
	})
	t.Cleanup(s.Close)

	ts := httptest.NewServer(s)
	t.Cleanup(ts.Close)

	conn, resp, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+query, nil)
	if err == nil {
		_ = conn.Close()
		return http.StatusSwitchingProtocols
	}
	if resp == nil {
		t.Fatalf("dial failed with no response: %v", err)
	}
	return resp.StatusCode
}

// Without the bypass, a client on this host presents the secret like any other.
func TestClientLoopbackNeedsTheSecret(t *testing.T) {
	if code := dialClient(t, "/ws", false); code != http.StatusUnauthorized {
		t.Errorf("loopback with no credential got %d, want 401", code)
	}
	if code := dialClient(t, "/ws?secret=shared-secret", false); code != http.StatusSwitchingProtocols {
		t.Errorf("loopback with the secret got %d, want an upgrade", code)
	}
}

// With the bypass on, the config's policy reaches the client endpoint.
func TestClientLoopbackBypassWhenAllowed(t *testing.T) {
	if code := dialClient(t, "/ws", true); code != http.StatusSwitchingProtocols {
		t.Errorf("loopback with the bypass on got %d, want an upgrade", code)
	}
}
