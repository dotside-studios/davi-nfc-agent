//go:build !nowebui

package console

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/secure/pairing"
)

// gate builds a pairing gate over a registry of its own, which is what a build
// hands the console.
func gate(t *testing.T) *pairing.Gate {
	t.Helper()

	registry, err := pairing.NewRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return pairing.New(nil, pairing.Options{Registry: registry})
}

// Pairing is the gate's rather than the agent's, so the console follows it
// separately. A phone completing pairing, or a device revoked from the tray,
// left an open page listing what it loaded: the agent used to report device
// changes, and the console followed that, so moving the registry out of the
// agent took the subscription with it.
func TestAnOpenPageIsWokenByAPairing(t *testing.T) {
	g := gate(t)
	c := New(Config{Agent: quietAgent(t), Pairing: g})

	woken, done := c.subscribe()
	t.Cleanup(done)

	if _, _, err := g.PairedDevices().(*pairing.Registry).Pair("phone", "android"); err != nil {
		t.Fatalf("Pair: %v", err)
	}

	select {
	case <-woken:
	default:
		t.Fatal("a device pairing did not reach the open page")
	}
}

func TestAnOpenPageIsWokenByARevocation(t *testing.T) {
	g := gate(t)
	device, _, err := g.PairedDevices().(*pairing.Registry).Pair("phone", "android")
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}

	c := New(Config{Agent: quietAgent(t), Pairing: g})
	woken, done := c.subscribe()
	t.Cleanup(done)

	if err := g.PairedDevices().Revoke(device.ID); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	select {
	case <-woken:
	default:
		t.Fatal("a revocation did not reach the open page")
	}
}

// The PIN is displayed in two places, so whichever control rotates it, the
// other has to relabel. This is the console's half; the tray's is covered in
// agent/pairingplugin.
func TestAnOpenPageIsWokenByAPINRotatedElsewhere(t *testing.T) {
	g := gate(t)
	c := New(Config{Agent: quietAgent(t), Pairing: g})

	woken, done := c.subscribe()
	t.Cleanup(done)

	g.PairingServer().RotatePIN()

	select {
	case <-woken:
	default:
		t.Fatal("a PIN rotated outside the console did not reach the open page")
	}
}

// A build that pairs no devices holds no gate, and the console still answers
// for what it can rather than dereferencing one.
func TestTheConsoleToleratesNoPairing(t *testing.T) {
	c := New(Config{Agent: quietAgent(t)})
	h := c.host

	if got := h.PairingPIN(); got != "" {
		t.Errorf("PairingPIN() = %q with no gate, want empty", got)
	}
	if _, err := h.RotatePairingPIN(); err == nil {
		t.Error("RotatePairingPIN reported no error with no gate")
	}
	if got := h.PairedDevices(); got != nil {
		t.Errorf("PairedDevices() = %v with no gate, want nil", got)
	}
	if err := h.RevokeDevice("whatever"); err == nil {
		t.Error("RevokeDevice reported no error with no gate")
	}
}
