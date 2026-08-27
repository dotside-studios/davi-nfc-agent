package remotenfc_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dotside-studios/davi-nfc-agent/deviceid"
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

// This driver decides nothing about who may connect. Mounted on its own it
// serves every device that reaches it, which is what the other tests here rely
// on.
func TestBareHandlerAdmitsEveryDevice(t *testing.T) {
	m := remotenfc.NewManager(remotenfc.DeviceTimeout)
	defer m.Close()

	ts := httptest.NewServer(m.Handler(remotenfc.ServerOptions{}))
	defer ts.Close()

	conn, status := dialDevice(t, ts.URL)
	if conn == nil {
		t.Fatalf("a device was refused by a handler that checks nothing (status %d)", status)
	}
	_ = conn.Close()
}

// Whatever stands in front decides, and its refusal is what the device sees.
func TestAWrapperCanRefuse(t *testing.T) {
	m := remotenfc.NewManager(remotenfc.DeviceTimeout)
	defer m.Close()

	refuseWithoutToken := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Query().Get("token") != "good" {
				http.Error(w, "nope", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, deviceid.With(r, "device-7"))
		})
	}

	ts := httptest.NewServer(refuseWithoutToken(m.Handler(remotenfc.ServerOptions{})))
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
