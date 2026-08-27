package pairing_test

import (
	"github.com/dotside-studios/davi-nfc-agent/secure/pairing"
	"os"
	"path/filepath"
	"testing"
)

// The machinery is the component: bolting this on gets a credential store,
// rather than assembling one beside it and remembering to hand it over.
func TestTheManagerBuildsItsOwnRegistry(t *testing.T) {
	dir := t.TempDir()

	m := pairing.New(nil, pairing.Options{ConfigDir: dir})
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

	before := pairing.New(nil, pairing.Options{ConfigDir: dir})
	registry, ok := before.PairedDevices().(*pairing.Registry)
	if !ok {
		t.Fatalf("the store is %T, not a registry", before.PairedDevices())
	}
	device, token, err := registry.Pair("phone", "android")
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}

	after := pairing.New(nil, pairing.Options{ConfigDir: dir})

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

	m := pairing.New(nil, pairing.Options{ConfigDir: dir})
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

	m := pairing.New(nil, pairing.Options{Registry: registry})

	if got := m.PairedDevices().Count(); got != 1 {
		t.Errorf("Count() = %d, want the supplied registry's one device", got)
	}
}

// The gate hands out two narrow capabilities rather than the registry: a store
// to read and revoke, and a verifier to check. Minting stays inside with the
// pairing server, and the return types are what keeps it there.
//
// What this covers is that the two answer for the same credentials, so a
// console listing devices and an endpoint admitting one cannot drift apart.
func TestTheGateHandsOutNarrowCapabilities(t *testing.T) {
	g := pairing.New(nil, pairing.Options{ConfigDir: t.TempDir()})

	store := g.PairedDevices()
	verifier := g.TokenVerifier()
	if store == nil || verifier == nil {
		t.Fatal("the gate hands out no store or no verifier")
	}

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

// A build whose backends hold no sessions to end passes nil. Revoking still
// works; there is simply nothing to disconnect.
func TestAGateWithNoSessionsToEnd(t *testing.T) {
	g := pairing.New(nil, pairing.Options{ConfigDir: t.TempDir()})

	registry, ok := g.PairedDevices().(*pairing.Registry)
	if !ok {
		t.Fatalf("the store is %T, not a registry", g.PairedDevices())
	}
	device, _, err := registry.Pair("phone", "android")
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}

	if err := registry.Revoke(device.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if g.PairedDevices().Count() != 0 {
		t.Error("the revoked device is still in the store")
	}
}

// Revocation reaches the backend that holds the session.
func TestRevokingEndsTheSession(t *testing.T) {
	ended := []string{}
	g := pairing.New(sessionsFunc(func(id, _ string) bool {
		ended = append(ended, id)
		return true
	}), pairing.Options{ConfigDir: t.TempDir()})

	registry := g.PairedDevices().(*pairing.Registry)
	device, _, err := registry.Pair("phone", "android")
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}

	if err := registry.Revoke(device.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	if len(ended) != 1 || ended[0] != device.ID {
		t.Errorf("sessions ended for %v, want just %q", ended, device.ID)
	}
}

// sessionsFunc adapts a function to pairing.Sessions.
type sessionsFunc func(deviceID, reason string) bool

func (f sessionsFunc) DisconnectDevice(deviceID, reason string) bool {
	return f(deviceID, reason)
}
