package deviceserver_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/deviceserver"
)

// With no device driver there is no device protocol to speak. The connection
// used to be accepted anyway: the device registered, got "no handler for
// message type" logged at it, and waited for a reply that could never come.
func TestDeviceConnectionRefusedWithoutADriver(t *testing.T) {
	ds := deviceserver.New(deviceserver.Config{AllowedOrigins: []string{"*"}}, server.NewServerBridge())

	ts := httptest.NewServer(http.HandlerFunc(ds.ServeWS))
	defer ts.Close()

	url := "ws" + strings.TrimPrefix(ts.URL, "http") + "?mode=device"
	conn, resp, err := (&websocket.Dialer{HandshakeTimeout: 3 * time.Second}).Dial(url, nil)
	if err == nil {
		conn.Close()
		t.Fatal("a device connection was accepted with no driver to serve it")
	}
	if resp == nil {
		t.Fatalf("dial failed without a response: %v", err)
	}
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}
}
