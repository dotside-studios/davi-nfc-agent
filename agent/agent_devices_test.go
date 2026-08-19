package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPairIssuesUsableToken(t *testing.T) {
	registry, err := NewDeviceRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewDeviceRegistry: %v", err)
	}

	device, token, err := registry.Pair("Operator iPhone", "ios")
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}

	if device.ID == "" {
		t.Error("paired device has no ID")
	}
	if token == "" {
		t.Fatal("pairing returned no token")
	}

	id, ok := registry.VerifyToken(token)
	if !ok {
		t.Fatal("the issued token was not accepted")
	}
	if id != device.ID {
		t.Errorf("token resolved to %q, want %q", id, device.ID)
	}
}

// The registry must not be a file full of usable credentials.
func TestTokenIsNotStoredInTheClear(t *testing.T) {
	dir := t.TempDir()
	registry, err := NewDeviceRegistry(dir)
	if err != nil {
		t.Fatalf("NewDeviceRegistry: %v", err)
	}

	_, token, err := registry.Pair("Operator iPhone", "ios")
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, devicesFileName))
	if err != nil {
		t.Fatalf("read devices file: %v", err)
	}
	if strings.Contains(string(data), token) {
		t.Fatal("the token was written to disk in the clear")
	}

	var stored []PairedDevice
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("parse devices file: %v", err)
	}
	if len(stored) != 1 || stored[0].TokenHash == "" {
		t.Fatalf("expected one device with a hash, got %+v", stored)
	}
}

// Revoking one device must not disturb any other — the whole reason the
// registry exists, since rotating the shared secret logs out everything.
func TestRevokeIsPerDevice(t *testing.T) {
	registry, err := NewDeviceRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewDeviceRegistry: %v", err)
	}

	first, firstToken, err := registry.Pair("First", "android")
	if err != nil {
		t.Fatalf("Pair first: %v", err)
	}
	_, secondToken, err := registry.Pair("Second", "ios")
	if err != nil {
		t.Fatalf("Pair second: %v", err)
	}

	if err := registry.Revoke(first.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	if _, ok := registry.VerifyToken(firstToken); ok {
		t.Error("a revoked device's token still works")
	}
	if _, ok := registry.VerifyToken(secondToken); !ok {
		t.Error("revoking one device invalidated another")
	}
	if registry.Count() != 1 {
		t.Errorf("Count = %d, want 1", registry.Count())
	}
}

func TestPairingSurvivesRestart(t *testing.T) {
	dir := t.TempDir()

	registry, err := NewDeviceRegistry(dir)
	if err != nil {
		t.Fatalf("NewDeviceRegistry: %v", err)
	}
	device, token, err := registry.Pair("Operator iPhone", "ios")
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}

	reopened, err := NewDeviceRegistry(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}

	id, ok := reopened.VerifyToken(token)
	if !ok {
		t.Fatal("a paired device had to pair again after a restart")
	}
	if id != device.ID {
		t.Errorf("token resolved to %q, want %q", id, device.ID)
	}
}

func TestVerifyRejectsUnknownTokens(t *testing.T) {
	registry, err := NewDeviceRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewDeviceRegistry: %v", err)
	}
	if _, _, err := registry.Pair("Operator iPhone", "ios"); err != nil {
		t.Fatalf("Pair: %v", err)
	}

	for _, token := range []string{"", "not-a-token", "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		if _, ok := registry.VerifyToken(token); ok {
			t.Errorf("VerifyToken(%q) accepted an unknown credential", token)
		}
	}
}

func TestRevokeAll(t *testing.T) {
	registry, err := NewDeviceRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewDeviceRegistry: %v", err)
	}

	_, firstToken, _ := registry.Pair("First", "android")
	_, secondToken, _ := registry.Pair("Second", "ios")

	if err := registry.RevokeAll(); err != nil {
		t.Fatalf("RevokeAll: %v", err)
	}

	if _, ok := registry.VerifyToken(firstToken); ok {
		t.Error("a token survived RevokeAll")
	}
	if _, ok := registry.VerifyToken(secondToken); ok {
		t.Error("a token survived RevokeAll")
	}
	if registry.Count() != 0 {
		t.Errorf("Count = %d, want 0", registry.Count())
	}
}

func TestPairNotifiesOnChange(t *testing.T) {
	registry, err := NewDeviceRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewDeviceRegistry: %v", err)
	}

	var changes int
	registry.OnChange(func() { changes++ })

	device, _, _ := registry.Pair("First", "android")
	_ = registry.Revoke(device.ID)

	if changes != 2 {
		t.Errorf("OnChange fired %d times, want 2", changes)
	}
}
