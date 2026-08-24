//go:build !nowebui

package console

import (
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

func quietAgent(t *testing.T) *agent.Agent {
	t.Helper()

	return agent.New(agent.Config{
		Manager: nfc.NewMockManager(),
		Logger:  log.New(io.Discard, "", 0),
	})
}

// Listing the console's endpoints is all there is to serving it: the routes go
// on the listener, and the page's address is listed with the others.
func TestTheEndpointsServeTheConsoleAndListIt(t *testing.T) {
	a := quietAgent(t)

	servers := &agent.ServerPlugin{}
	c := New(Config{Agent: a, Servers: servers})
	servers.Add(c.Endpoints()...)
	if err := a.Plugins.Add(servers); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}

	fake := traymenu.NewFake()
	menu := traymenu.New(fake)
	t.Cleanup(menu.Close)

	if err := a.Activate(menu); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	hostPort := ""
	if ips := agent.LocalIPs(); len(ips) > 0 {
		hostPort = ips[0]
	} else {
		hostPort = "localhost"
	}
	hostPort = net.JoinHostPort(hostPort, strconv.Itoa(servers.Listener().Port()))

	handler := servers.Listener().Handler()

	// The control API answers on its own paths, and the page is served from
	// the root: neither is there unless the plugin mounted it.
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/control/state", nil))
	if rec.Code == http.StatusNotFound {
		t.Error("GET /control/state = 404; the control API is not mounted")
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if body := rec.Body.String(); body == "NFC Agent" {
		t.Error("the root still serves the listener's banner; the console page is not mounted")
	}

	for _, title := range []string{"  Copy Control Center URL", "  Open Control Center"} {
		if item := fake.Find("Server URLs", title); item == nil {
			t.Errorf("%q is missing from the addresses:\n%s", title, fake.Render())
		}
	}
	if item := fake.Find("Server URLs", "Control Center: http://"+strings.TrimSuffix(hostPort, "/")+"/"); item == nil {
		t.Errorf("the console's address is not listed:\n%s", fake.Render())
	}
}

// A build with no console compiled in lists no endpoints, so a program needs no
// build tag of its own to leave one out.
func TestAnAbsentConsoleListsNoEndpoints(t *testing.T) {
	var absent *Server
	if got := absent.Endpoints(); got != nil {
		t.Errorf("Endpoints() = %v, want none", got)
	}
}
