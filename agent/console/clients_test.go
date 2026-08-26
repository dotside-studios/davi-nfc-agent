//go:build !nowebui

package console

import (
	"io"
	"log"
	"net"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
	"github.com/gorilla/websocket"
)

// freePort reserves a port and hands it back, so a started agent binds
// somewhere nothing else in the build is listening.
func freePort(t *testing.T) int {
	t.Helper()

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	defer func() { _ = l.Close() }()

	_, port, err := net.SplitHostPort(l.Addr().String())
	if err != nil {
		t.Fatalf("read the reserved port: %v", err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("parse the reserved port: %v", err)
	}
	return n
}

// served is a running agent, the console reporting on it, and the address a
// client connects to. The listener is not bound: its mux is served through
// httptest, so nothing races for a port.
type served struct {
	agent   *agent.Agent
	servers *agent.ServerPlugin
	console *Server
	url     string
}

func servedConsole(t *testing.T) served {
	t.Helper()

	a := agent.New(agent.Config{
		Manager: nfc.NewMockManager(),
		Logger:  log.New(io.Discard, "", 0),

		// A started agent binds for real, and this binary runs beside the
		// others, so the default port is not this test's to take.
		DevicePort: freePort(t),
	})
	servers := &agent.ServerPlugin{}
	c := New(Config{Agent: a, Servers: servers})

	if err := a.Plugins.Add(servers); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}
	menu := traymenu.New(traymenu.Discard())
	t.Cleanup(menu.Close)
	if err := a.Activate(menu); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(a.Stop)

	http := httptest.NewServer(servers.Listener().Handler())
	t.Cleanup(http.Close)

	return served{agent: a, servers: servers, console: c, url: "ws" + strings.TrimPrefix(http.URL, "http") + "/ws"}
}

// dial connects a client and waits for the server to have registered it: the
// handshake is answered before the connection is on the roster.
func (s served) dial(t *testing.T) *websocket.Conn {
	t.Helper()

	conn, _, err := websocket.DefaultDialer.Dial(s.url, nil)
	if err != nil {
		t.Fatalf("client dial: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	deadline := time.Now().Add(3 * time.Second)
	for s.servers.ClientCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the server never registered the client")
		}
		time.Sleep(time.Millisecond)
	}
	return conn
}

// The console reports on the clients the server holds, and disconnects one
// through it. The agent holds no server, so asking it would report nothing.
func TestTheConsoleReportsTheServersClients(t *testing.T) {
	s := servedConsole(t)
	s.dial(t)

	if got := s.console.host.ClientCount(); got != 1 {
		t.Fatalf("ClientCount() = %d with one client connected, want 1", got)
	}

	live := s.console.host.Clients()
	if len(live) != 1 || live[0].ID == "" {
		t.Fatalf("Clients() = %v, want the one connected client, named", live)
	}

	if err := s.console.host.DisconnectClient(live[0].ID); err != nil {
		t.Fatalf("DisconnectClient: %v", err)
	}

	// The roster clears when the closed connection's read loop notices, not
	// when Disconnect returns.
	deadline := time.Now().Add(3 * time.Second)
	for s.console.host.ClientCount() != 0 {
		if time.Now().After(deadline) {
			t.Fatalf("ClientCount() = %d after disconnecting the only client, want 0", s.console.host.ClientCount())
		}
		time.Sleep(time.Millisecond)
	}

	if err := s.console.host.DisconnectClient(live[0].ID); err == nil {
		t.Error("disconnecting a client that has already gone reported no error")
	}
}

// A client connecting is the server's news, not the agent's, and it changes
// what an open page shows.
func TestAnOpenPageIsWokenByAClientConnecting(t *testing.T) {
	s := servedConsole(t)

	woken, done := s.console.subscribe()
	t.Cleanup(done)

	s.dial(t)

	select {
	case <-woken:
	case <-time.After(3 * time.Second):
		t.Fatal("a client connecting did not reach the open page")
	}
}
