package serverplugin

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// admitsSecret asks the device endpoint's gate whether it would admit a
// connection presenting secret, from an address the loopback bypass misses.
func admitsSecret(t *testing.T, gate func(http.ResponseWriter, *http.Request) (string, bool), secret string) bool {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/ws?mode=device&secret="+secret, nil)
	req.RemoteAddr = "192.0.2.10:4444"

	_, ok := gate(httptest.NewRecorder(), req)
	return ok
}

// Rotating the secret revokes the old one everywhere it is checked. The device
// endpoint's gate held the secret it was built with, so a rotation left it
// admitting the old secret and refusing the one the console had just handed the
// operator: the control that exists to revoke access revoked nothing.
func TestRotatingTheSecretReachesTheDeviceEndpoint(t *testing.T) {
	p := &Plugin{}
	a := agent.New(agent.Config{
		Manager:    nfc.NewMockManager(),
		Logger:     log.New(io.Discard, "", 0),
		APISecret:  "old-secret",
		ConfigDir:  t.TempDir(),
		DevicePort: freePort(t),
	})
	if err := a.Plugins.Add(p); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}
	gate := p.Authenticate()
	if err := a.Activate(nil); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	if !admitsSecret(t, gate, "old-secret") {
		t.Fatal("the secret the agent was built with was refused")
	}

	fresh, err := a.RotateAPISecret()
	if err != nil {
		t.Fatalf("RotateAPISecret: %v", err)
	}

	if admitsSecret(t, gate, "old-secret") {
		t.Error("the rotated-away secret is still admitted")
	}
	if !admitsSecret(t, gate, fresh) {
		t.Error("the fresh secret is refused")
	}
	if got := a.APISecret(); got != fresh {
		t.Errorf("APISecret() = %q, want the fresh secret", got)
	}
}

// The client server reads the secret per connection too, so a rotation needs
// nothing rebuilt for clients either.
func TestRotatingTheSecretNeedsNoRestart(t *testing.T) {
	p := &Plugin{}
	a := agent.New(agent.Config{
		Manager:    nfc.NewMockManager(),
		Logger:     log.New(io.Discard, "", 0),
		APISecret:  "old-secret",
		ConfigDir:  t.TempDir(),
		DevicePort: freePort(t),
	})
	serveClients(p, a)
	if err := a.Plugins.Add(p); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}
	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	serving := p.serving()
	fresh, err := a.RotateAPISecret()
	if err != nil {
		t.Fatalf("RotateAPISecret: %v", err)
	}

	if p.serving() != serving {
		t.Error("rotating the secret rebuilt the client server")
	}

	unauthorized := clientUpgrade(t, p, "old-secret")
	if unauthorized != http.StatusUnauthorized {
		t.Errorf("the rotated-away secret got %d from the client endpoint, want 401", unauthorized)
	}
	if got := clientUpgrade(t, p, fresh); got == http.StatusUnauthorized {
		t.Error("the fresh secret was refused by the client endpoint")
	}
}

// clientUpgrade asks the listener's mux to upgrade a client connection
// presenting secret, and reports the status. A rejected credential answers
// before the handshake, which is what this reads.
func clientUpgrade(t *testing.T, p *Plugin, secret string) int {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/ws?secret="+secret, nil)
	req.RemoteAddr = "192.0.2.10:4444"
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

	rec := httptest.NewRecorder()
	p.Listener().Handler().ServeHTTP(rec, req)
	return rec.Code
}
