package agent

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

func TestLoadOrCreateAPISecret_FirstRun(t *testing.T) {
	dir := t.TempDir()
	secret, fresh, err := loadOrCreateAPISecret(dir)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	if !fresh {
		t.Errorf("first run should report fresh=true")
	}
	if len(secret) < 32 {
		t.Errorf("secret too short: %d chars", len(secret))
	}

	// File must exist with the secret on disk.
	data, err := os.ReadFile(filepath.Join(dir, secretFileName))
	if err != nil {
		t.Fatalf("read persisted secret: %v", err)
	}
	if got := strings.TrimSpace(string(data)); got != secret {
		t.Errorf("on-disk secret %q != returned %q", got, secret)
	}
}

func TestLoadOrCreateAPISecret_Persists(t *testing.T) {
	dir := t.TempDir()

	first, _, err := loadOrCreateAPISecret(dir)
	if err != nil {
		t.Fatalf("first run: %v", err)
	}

	second, fresh, err := loadOrCreateAPISecret(dir)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if fresh {
		t.Errorf("second run should report fresh=false")
	}
	if first != second {
		t.Errorf("secret changed across calls: %q vs %q", first, second)
	}
}

func TestLoadOrCreateAPISecret_RegenerateOnEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, secretFileName)

	if err := os.WriteFile(path, []byte("   \n"), 0600); err != nil {
		t.Fatalf("seed: %v", err)
	}

	secret, fresh, err := loadOrCreateAPISecret(dir)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !fresh {
		t.Errorf("empty file should trigger fresh=true")
	}
	if len(secret) < 32 {
		t.Errorf("secret too short")
	}
}

func TestRotateAPISecret(t *testing.T) {
	dir := t.TempDir()
	first, _, err := loadOrCreateAPISecret(dir)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	rotated, err := rotateAPISecret(dir)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if rotated == first {
		t.Errorf("rotateAPISecret returned the same value")
	}

	// Subsequent load should return the rotated value.
	loaded, _, err := loadOrCreateAPISecret(dir)
	if err != nil {
		t.Fatalf("post-rotate load: %v", err)
	}
	if loaded != rotated {
		t.Errorf("loaded %q != rotated %q", loaded, rotated)
	}
}

func TestGenerateAPISecret(t *testing.T) {
	seen := make(map[string]struct{})
	for range 50 {
		s, err := generateAPISecret()
		if err != nil {
			t.Fatalf("gen: %v", err)
		}
		if len(s) < 32 {
			t.Errorf("too short: %d", len(s))
		}
		if _, dup := seen[s]; dup {
			t.Errorf("collision: %q", s)
		}
		seen[s] = struct{}{}
	}
}

// admitsSecret asks the device endpoint's gate whether it would admit a
// connection presenting secret, from an address the loopback bypass misses.
func admitsSecret(t *testing.T, a *Agent, secret string) bool {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/ws?mode=device&secret="+secret, nil)
	req.RemoteAddr = "192.0.2.10:4444"

	_, ok := a.DeviceAuth.Check(httptest.NewRecorder(), req)
	return ok
}

// Rotating the secret revokes the old one everywhere it is checked. The device
// endpoint's gate held the secret it was built with, so a rotation left it
// admitting the old secret and refusing the one the console had just handed the
// operator: the control that exists to revoke access revoked nothing.
func TestRotatingTheSecretReachesTheDeviceEndpoint(t *testing.T) {
	a := New(Config{
		Manager:   nfc.NewMockManager(),
		Logger:    log.New(io.Discard, "", 0),
		APISecret: "old-secret",
		ConfigDir: t.TempDir(),
	})

	if !admitsSecret(t, a, "old-secret") {
		t.Fatal("the secret the agent was built with was refused")
	}

	fresh, err := a.RotateAPISecret()
	if err != nil {
		t.Fatalf("RotateAPISecret: %v", err)
	}

	if admitsSecret(t, a, "old-secret") {
		t.Error("the rotated-away secret is still admitted")
	}
	if !admitsSecret(t, a, fresh) {
		t.Error("the fresh secret is refused")
	}
	if got := a.APISecret(); got != fresh {
		t.Errorf("APISecret() = %q, want the fresh secret", got)
	}
}

// The client server reads the secret per connection too, so a rotation needs
// nothing rebuilt for clients either.
func TestRotatingTheSecretNeedsNoRestart(t *testing.T) {
	p := &ServerPlugin{}
	a := New(Config{
		Manager:   nfc.NewMockManager(),
		Logger:    log.New(io.Discard, "", 0),
		APISecret: "old-secret",
		ConfigDir: t.TempDir(),
		Plugins:   []Plugin{p},
	})
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
func clientUpgrade(t *testing.T, p *ServerPlugin, secret string) int {
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
