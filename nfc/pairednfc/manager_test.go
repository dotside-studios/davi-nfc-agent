package pairednfc_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/pairednfc"
	"github.com/dotside-studios/davi-nfc-agent/pairing"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

// The machinery is the component: bolting this on gets a credential store,
// rather than assembling one beside it and remembering to hand it over.
func TestTheManagerBuildsItsOwnRegistry(t *testing.T) {
	dir := t.TempDir()

	m, err := pairednfc.New(nfc.NewMockManager(), pairednfc.Options{ConfigDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if m.PairedDevices() == nil {
		t.Fatal("the manager holds no credential store")
	}
	if m.PairingServer() == nil {
		t.Fatal("the manager holds no pairing server")
	}
	if got := m.PairingServer().PIN(); got == "" {
		t.Error("the pairing server has no PIN; a phone would have nothing to present")
	}
}

// A device paired before a restart is still paired after one.
func TestAPairingSurvivesRebuildingTheManager(t *testing.T) {
	dir := t.TempDir()

	before, err := pairednfc.New(nfc.NewMockManager(), pairednfc.Options{ConfigDir: dir})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	registry, ok := before.PairedDevices().(*pairing.Registry)
	if !ok {
		t.Fatalf("the store is %T, not a registry", before.PairedDevices())
	}
	device, token, err := registry.Pair("phone", "android")
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}

	after, err := pairednfc.New(nfc.NewMockManager(), pairednfc.Options{ConfigDir: dir})
	if err != nil {
		t.Fatalf("New after restart: %v", err)
	}

	if got := after.PairedDevices().Count(); got != 1 {
		t.Fatalf("the rebuilt manager holds %d devices, want 1", got)
	}
	if id, ok := after.TokenVerifier().VerifyToken(token); !ok || id != device.ID {
		t.Errorf("VerifyToken after a restart = (%q, %v), want the paired device", id, ok)
	}
}

// A config directory that cannot be read is not a reason to refuse to start:
// the agent serves and the devices are kept in memory.
func TestAnUnreadableConfigDirStillBuilds(t *testing.T) {
	dir := t.TempDir()
	// A file where the registry expects a directory entry it can parse.
	if err := os.WriteFile(filepath.Join(dir, "paired-devices.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	m, err := pairednfc.New(nfc.NewMockManager(), pairednfc.Options{ConfigDir: dir})
	if err != nil {
		t.Fatalf("New reported an error for an unreadable store: %v", err)
	}
	if m.PairedDevices() == nil {
		t.Fatal("no store at all after an unreadable one")
	}
	if got := m.PairedDevices().Count(); got != 0 {
		t.Errorf("Count() = %d, want an empty in-memory store", got)
	}
}

// A build that already holds a store passes it, which agent.New is documented
// to allow.
func TestASuppliedRegistryIsUsed(t *testing.T) {
	registry, err := pairing.NewRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if _, _, err := registry.Pair("phone", "android"); err != nil {
		t.Fatalf("Pair: %v", err)
	}

	m, err := pairednfc.New(nfc.NewMockManager(), pairednfc.Options{Registry: registry})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if got := m.PairedDevices().Count(); got != 1 {
		t.Errorf("Count() = %d, want the supplied registry's one device", got)
	}
}

// The manager hands out two narrow capabilities rather than the registry: a
// store to read and revoke, and a verifier to check. Minting stays inside with
// the pairing server.
//
// The narrowing is the compiler's, since a caller holding a pairing.Store
// cannot reach Pair without asserting its way back to the registry, so this
// pins the types rather than probing the value.
func TestTheManagerHandsOutNarrowCapabilities(t *testing.T) {
	m := over(t, reader{})

	var store pairing.Store = m.PairedDevices()
	var verifier server.TokenVerifier = m.TokenVerifier()

	if store == nil || verifier == nil {
		t.Fatal("the manager hands out no store or no verifier")
	}

	// Both views answer for the same credentials.
	registry, ok := store.(*pairing.Registry)
	if !ok {
		t.Fatalf("the store is %T, not a registry", store)
	}
	_, token, err := registry.Pair("phone", "android")
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if _, ok := verifier.VerifyToken(token); !ok {
		t.Error("the verifier does not recognise what the store issued")
	}
}

func TestNewRefusesNoChild(t *testing.T) {
	if _, err := pairednfc.New(nil, pairednfc.Options{}); err == nil {
		t.Error("New over no manager reported no error")
	}
}
