package serverplugin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// served returns the routes a set-up agent answers on, without binding a
// listener. The server plugin is registered as a program would register it, and
// the listener exists once the plugins have been activated.
func served(t *testing.T) http.Handler {
	t.Helper()

	rt, err := agent.Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("agent.Setup: %v", err)
	}

	servers := &Plugin{}
	if err := rt.Agent.Plugins.Add(servers); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := rt.Agent.Activate(nil); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	return servers.Listener().Handler()
}

// The health check is what a client library polls to find the agent, so its
// shape is part of the wire contract. Both spellings serve the same handler.
func TestHealthReportsTheAgent(t *testing.T) {
	handler := served(t)

	for _, path := range []string{"/health", "/api/v1/health"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))

		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", path, rec.Code)
			continue
		}

		var body map[string]any
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Errorf("GET %s: %v", path, err)
			continue
		}
		if body["type"] != "agent" {
			t.Errorf("GET %s type = %v, want agent", path, body["type"])
		}
		if body["status"] != "ok" {
			t.Errorf("GET %s status = %v, want ok", path, body["status"])
		}
	}
}

// Health answers before the agent starts, so a client polling for the agent to
// come up gets an answer rather than a refused connection.
func TestHealthAnswersWhileStopped(t *testing.T) {
	rec := httptest.NewRecorder()
	served(t).ServeHTTP(rec, httptest.NewRequest("GET", "/health", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("health while stopped = %d, want 200", rec.Code)
	}
}

// The WebSocket endpoint has nothing to dispatch to before the agent starts,
// and says so rather than accepting a connection nothing serves.
func TestWSRefusedWhileStopped(t *testing.T) {
	rec := httptest.NewRecorder()
	served(t).ServeHTTP(rec, httptest.NewRequest("GET", "/ws", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/ws while stopped = %d, want 503", rec.Code)
	}
}
