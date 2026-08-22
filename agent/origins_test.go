package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestOriginStoreSeedsFirstPartyDefaults(t *testing.T) {
	dir := t.TempDir()

	store, err := NewOriginStore(dir)
	if err != nil {
		t.Fatalf("NewOriginStore: %v", err)
	}

	// The shipped console must work on a fresh install without configuration.
	if !store.Allowed("shop.davi.social") {
		t.Error("shop.davi.social is not allowed by default")
	}
	if !store.Allowed("davi.social") {
		t.Error("davi.social is not allowed by default")
	}
	// The guard still has to stop everything else.
	if store.Allowed("evil.example") {
		t.Error("an unknown origin was allowed by default")
	}

	if _, err := os.Stat(filepath.Join(dir, originsFileName)); err != nil {
		t.Errorf("defaults were not persisted: %v", err)
	}
}

func TestOriginStorePersistsAcrossRestart(t *testing.T) {
	dir := t.TempDir()

	store, err := NewOriginStore(dir)
	if err != nil {
		t.Fatalf("NewOriginStore: %v", err)
	}
	if err := store.Allow("https://console.example:8443/admin"); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if err := store.Revoke("davi.social"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	reopened, err := NewOriginStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}

	// A pasted URL is stored as the host:port the origin check matches on.
	if !reopened.Allowed("console.example:8443") {
		t.Error("allowed origin did not survive a restart")
	}
	if reopened.Allowed("davi.social") {
		t.Error("revoked origin came back after a restart")
	}
}

func TestOriginStoreNormalizes(t *testing.T) {
	store, _ := NewOriginStore("")

	if err := store.Allow("HTTPS://Console.Example/path?q=1"); err != nil {
		t.Fatalf("Allow: %v", err)
	}

	for _, form := range []string{
		"console.example",
		"https://console.example",
		"Console.Example",
		"console.example/other",
	} {
		if !store.Allowed(form) {
			t.Errorf("Allowed(%q) = false, want true", form)
		}
	}
}

// A refused origin has to be recoverable, so it is recorded for the tray to
// offer. Repeats collapse — a console retrying in a loop must not flood it.
func TestOriginStoreRecordsBlockedOnce(t *testing.T) {
	store, _ := NewOriginStore("")

	var notified int
	store.OnBlocked(func(string) { notified++ })

	store.RecordBlocked("https://console.example")
	store.RecordBlocked("console.example")
	store.RecordBlocked("console.example")

	if notified != 1 {
		t.Errorf("notified %d times, want 1", notified)
	}
	if blocked := store.Blocked(); len(blocked) != 1 || blocked[0] != "console.example" {
		t.Errorf("Blocked() = %v, want [console.example]", blocked)
	}
}

// Once allowed, an origin stops being offered as blocked.
func TestOriginStoreBlockedClearsOnAllow(t *testing.T) {
	store, _ := NewOriginStore("")

	store.RecordBlocked("console.example")
	if err := store.Allow("console.example"); err != nil {
		t.Fatalf("Allow: %v", err)
	}

	if blocked := store.Blocked(); len(blocked) != 0 {
		t.Errorf("Blocked() = %v, want empty once allowed", blocked)
	}
}

// The escape hatch must never reach disk: left on by accident it would let any
// site the operator visits drive the reader.
func TestSessionAllowAnyIsNotPersisted(t *testing.T) {
	dir := t.TempDir()

	store, err := NewOriginStore(dir)
	if err != nil {
		t.Fatalf("NewOriginStore: %v", err)
	}
	store.SessionAllowAny(true)

	if !store.Allowed("evil.example") {
		t.Error("session allow-any did not take effect")
	}

	data, err := os.ReadFile(filepath.Join(dir, originsFileName))
	if err != nil {
		t.Fatalf("read origins file: %v", err)
	}
	var stored []string
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("parse origins file: %v", err)
	}
	for _, origin := range stored {
		if origin == "*" {
			t.Fatal("session allow-any was written to disk")
		}
	}

	reopened, err := NewOriginStore(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	if reopened.Allowed("evil.example") {
		t.Error("allow-any survived a restart; the guard must come back")
	}
}

func TestOriginStoreChangeNotification(t *testing.T) {
	store, _ := NewOriginStore("")

	var changes int
	store.OnChange(func() { changes++ })

	_ = store.Allow("a.example")
	_ = store.Revoke("a.example")
	store.SessionAllowAny(true)

	if changes != 3 {
		t.Errorf("OnChange fired %d times, want 3", changes)
	}
}
