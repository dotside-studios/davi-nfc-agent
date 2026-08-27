package serverplugin

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/server/clientserver"
	"github.com/gorilla/websocket"
)

// clientOf connects to a client server the way an application does, and returns
// the message types it receives.
func clientOf(t *testing.T, srv *clientserver.Server) chan string {
	t.Helper()

	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// A scan broadcast before the server has registered the connection reaches
	// nobody, and the handshake returns first.
	deadline := time.Now().Add(3 * time.Second)
	for srv.ClientCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the client server never registered the connection")
		}
		time.Sleep(5 * time.Millisecond)
	}

	seen := make(chan string, 16)
	go func() {
		for {
			var msg struct {
				Type string `json:"type"`
			}
			if err := conn.ReadJSON(&msg); err != nil {
				return
			}
			select {
			case seen <- msg.Type:
			default:
			}
		}
	}()
	return seen
}

// await reports whether one of the messages arrives.
func await(t *testing.T, seen chan string, want string) bool {
	t.Helper()

	deadline := time.After(3 * time.Second)
	for {
		select {
		case got := <-seen:
			if got == want {
				return true
			}
		case <-deadline:
			return false
		}
	}
}

// What the agent reports reaches the clients, scans and reader status alike,
// and keeps reaching them across a stop and start: the server is the plugin's
// rather than the run's, so a client is not silently cut off by a restart it
// cannot see.
func TestWhatTheAgentReportsReachesTheClients(t *testing.T) {
	p := &Plugin{}
	a := serverAgent(t, p)

	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	serving := p.serving()
	client := clientOf(t, serving)

	a.Events().Tag.Emit(nfc.NFCData{Device: "mock:usb:001", Card: nfc.NewCard(nfc.NewMockTag("04A1B2C3"))})
	if !await(t, client, "tagData") {
		t.Fatal("a scan the agent reported never reached the clients")
	}
	a.Events().Reader.Emit(nfc.DeviceStatus{Device: "mock:usb:001", Connected: true})
	if !await(t, client, "deviceStatus") {
		t.Fatal("the readers' status never reached the clients")
	}

	a.Stop()
	if err := a.Start(""); err != nil {
		t.Fatalf("Start again: %v", err)
	}
	defer a.Stop()

	if p.serving() != serving {
		t.Fatal("the restart replaced the server the clients are connected to")
	}

	a.Events().Tag.Emit(nfc.NFCData{Device: "mock:usb:001", Card: nfc.NewCard(nfc.NewMockTag("04FFFFFF"))})
	if !await(t, client, "tagData") {
		t.Error("a scan after the restart never reached the client that stayed connected")
	}
}
