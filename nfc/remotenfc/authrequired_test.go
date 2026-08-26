package remotenfc_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
)

func dialDevice(t *testing.T, url string) (*websocket.Conn, int) {
	t.Helper()

	conn, resp, err := (&websocket.Dialer{
		HandshakeTimeout: 3 * time.Second,
		Subprotocols:     []string{"davi-nfc-device.v1"},
	}).Dial("ws"+strings.TrimPrefix(url, "http"), nil)
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	if err != nil {
		return nil, status
	}
	return conn, status
}

// Mounting the endpoint without an authenticator used to serve an open device
// endpoint: the upgrade succeeded, the device registered, and nothing said so.
// Forgetting the check is now refused instead of silently permitted.
func TestHandlerWithoutAuthenticatorRefuses(t *testing.T) {
	m := remotenfc.NewManager(remotenfc.DeviceTimeout)
	defer m.Close()

	ts := httptest.NewServer(m.Handler(remotenfc.ServerOptions{}))
	defer ts.Close()

	conn, status := dialDevice(t, ts.URL)
	if conn != nil {
		_ = conn.Close()
		t.Fatal("an unauthenticated device connection was accepted")
	}
	if status != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", status, http.StatusServiceUnavailable)
	}
}

// The opt-out is explicit, for a driver reached only over a trusted transport.
func TestAllowUnauthenticatedIsHonoured(t *testing.T) {
	m := remotenfc.NewManager(remotenfc.DeviceTimeout)
	defer m.Close()

	ts := httptest.NewServer(m.Handler(remotenfc.ServerOptions{AllowUnauthenticated: true}))
	defer ts.Close()

	conn, status := dialDevice(t, ts.URL)
	if conn == nil {
		t.Fatalf("AllowUnauthenticated did not admit the device (status %d)", status)
	}
	_ = conn.Close()
}

// A supplied authenticator decides, and its rejection is what the device sees.
func TestAuthenticatorDecides(t *testing.T) {
	m := remotenfc.NewManager(remotenfc.DeviceTimeout)
	defer m.Close()

	ts := httptest.NewServer(m.Handler(remotenfc.ServerOptions{
		Authenticate: func(w http.ResponseWriter, r *http.Request) (string, bool) {
			if r.URL.Query().Get("token") != "good" {
				http.Error(w, "nope", http.StatusUnauthorized)
				return "", false
			}
			return "", true
		},
	}))
	defer ts.Close()

	if conn, status := dialDevice(t, ts.URL); conn != nil {
		_ = conn.Close()
		t.Errorf("a device with no token was admitted (status %d)", status)
	} else if status != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", status, http.StatusUnauthorized)
	}

	conn, status := dialDevice(t, ts.URL+"?token=good")
	if conn == nil {
		t.Fatalf("a device with the token was refused (status %d)", status)
	}
	_ = conn.Close()
}
