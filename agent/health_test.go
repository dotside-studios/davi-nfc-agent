package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// The health check is what a client library polls to find the agent, so its
// shape is part of the wire contract. Both spellings serve the same handler.
func TestHealthReportsTheAgent(t *testing.T) {
	rt, err := Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	for _, path := range []string{"/health", "/api/v1/health"} {
		rec := httptest.NewRecorder()
		rt.Server.Handler().ServeHTTP(rec, httptest.NewRequest("GET", path, nil))

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
	rt, err := Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	rec := httptest.NewRecorder()
	rt.Server.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/health", nil))
	if rec.Code != http.StatusOK {
		t.Errorf("health while stopped = %d, want 200", rec.Code)
	}
}

// The WebSocket endpoint has nothing to dispatch to before the agent starts,
// and says so rather than accepting a connection nothing serves.
func TestWSRefusedWhileStopped(t *testing.T) {
	rt, err := Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	rec := httptest.NewRecorder()
	rt.Server.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/ws", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("/ws while stopped = %d, want 503", rec.Code)
	}
}
