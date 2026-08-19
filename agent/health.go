package agent

import (
	"encoding/json"
	"net/http"
	"time"
)

// healthHandler reports that the agent is up and how many clients it is
// serving. Mounted at both /health and /api/v1/health: the two spellings
// predate each other and clients in the wild use both.
func (a *Agent) healthHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodOptions {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		clients := 0
		if a.ClientServer != nil {
			clients = a.ClientServer.ClientCount()
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    "ok",
			"type":      "agent",
			"timestamp": time.Now().Format("2006-01-02T15:04:05Z07:00"),
			"clients":   clients,
		})
	})
}
