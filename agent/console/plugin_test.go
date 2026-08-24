//go:build !nowebui

package console

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
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

// Registering the plugin is all there is to having a control center: it mounts
// its own routes and adds the entry that opens it.
func TestThePluginServesTheConsoleAndOffersIt(t *testing.T) {
	a := quietAgent(t)

	servers := &agent.ServerPlugin{}
	if err := a.Plugins.Add(servers, NewPlugin(a, nil, nil, nil)); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}

	fake := traymenu.NewFake()
	menu := traymenu.New(fake)
	t.Cleanup(menu.Close)

	if err := a.Activate(menu); err != nil {
		t.Fatalf("Activate: %v", err)
	}

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

	if item := fake.Find("Open Control Center"); item == nil {
		t.Errorf("the entry that opens the console is missing:\n%s", fake.Render())
	}
}

// It mounts on a listener some earlier plugin published, so registering it
// without one says so rather than serving nothing.
func TestThePluginNeedsAListenerToMountOn(t *testing.T) {
	a := quietAgent(t)
	if err := a.Plugins.Add(NewPlugin(a, nil, nil, nil)); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}

	err := a.Activate(nil)
	if err == nil {
		t.Fatal("the console mounted with no listener to mount on")
	}
	if !strings.Contains(err.Error(), "control center") {
		t.Errorf("error = %q, want it to name the plugin", err)
	}
}
