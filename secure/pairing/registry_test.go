package pairing

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestPairIssuesUsableToken(t *testing.T) {
	registry, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
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
	registry, err := NewRegistry(dir)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
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

	var stored []Device
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("parse devices file: %v", err)
	}
	if len(stored) != 1 || stored[0].TokenHash == "" {
		t.Fatalf("expected one device with a hash, got %+v", stored)
	}
}

// Revoking one device must not disturb any other, which is the whole reason the
// registry exists, since rotating the shared secret logs out everything.
func TestRevokeIsPerDevice(t *testing.T) {
	registry, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
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

	registry, err := NewRegistry(dir)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	device, token, err := registry.Pair("Operator iPhone", "ios")
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}

	reopened, err := NewRegistry(dir)
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
	registry, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
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
	registry, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
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
	registry, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	var changes int
	registry.OnChange(func() { changes++ })

	device, _, _ := registry.Pair("First", "android")
	_ = registry.Revoke(device.ID)

	if changes != 2 {
		t.Errorf("OnChange fired %d times, want 2", changes)
	}
}

func TestEveryDeviceSubscriberIsNotified(t *testing.T) {
	registry, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	var first, second int
	registry.OnChange(func() { first++ })
	conn := registry.OnChange(func() { second++ })

	if _, _, err := registry.Pair("First", "android"); err != nil {
		t.Fatalf("Pair: %v", err)
	}

	if first != 1 || second != 1 {
		t.Fatalf("subscribers fired %d and %d times, want 1 and 1", first, second)
	}

	conn.Disconnect()
	if _, _, err := registry.Pair("Second", "ios"); err != nil {
		t.Fatalf("Pair: %v", err)
	}

	if first != 2 || second != 1 {
		t.Errorf("after Disconnect subscribers fired %d and %d times, want 2 and 1", first, second)
	}
}

// A token is only checked when a device connects, so a subscriber needs to know
// which device was revoked in order to end the session it already holds.
func TestRevokeNamesTheDevice(t *testing.T) {
	registry, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	var revoked [][]string
	defer registry.OnRevoke(func(ids []string) { revoked = append(revoked, ids) }).Disconnect()

	device, _, err := registry.Pair("First", "android")
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if _, _, err := registry.Pair("Second", "ios"); err != nil {
		t.Fatalf("Pair second: %v", err)
	}
	if len(revoked) != 0 {
		t.Fatalf("pairing reported a revocation: %v", revoked)
	}

	if err := registry.Revoke(device.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if len(revoked) != 1 || len(revoked[0]) != 1 || revoked[0][0] != device.ID {
		t.Fatalf("Revoke reported %v, want one emission naming %s", revoked, device.ID)
	}

	// Revoking a device that is not there changes nothing, so it announces
	// nothing: a subscriber would otherwise tear down a session on a typo.
	_ = registry.Revoke("no-such-device")
	if len(revoked) != 1 {
		t.Fatalf("revoking an unknown device reported %v", revoked)
	}
}

// RevokeAll names every device it revoked, so every live session goes with it.
func TestRevokeAllNamesEveryDevice(t *testing.T) {
	registry, err := NewRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	var revoked []string
	defer registry.OnRevoke(func(ids []string) { revoked = append(revoked, ids...) }).Disconnect()

	first, _, _ := registry.Pair("First", "android")
	second, _, _ := registry.Pair("Second", "ios")

	if err := registry.RevokeAll(); err != nil {
		t.Fatalf("RevokeAll: %v", err)
	}

	sort.Strings(revoked)
	want := []string{first.ID, second.ID}
	sort.Strings(want)
	if len(revoked) != 2 || revoked[0] != want[0] || revoked[1] != want[1] {
		t.Fatalf("RevokeAll reported %v, want %v", revoked, want)
	}

	// An empty registry has nothing to announce.
	revoked = nil
	if err := registry.RevokeAll(); err != nil {
		t.Fatalf("RevokeAll on an empty registry: %v", err)
	}
	if len(revoked) != 0 {
		t.Fatalf("RevokeAll on an empty registry reported %v", revoked)
	}
}
