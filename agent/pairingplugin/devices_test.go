package pairingplugin

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/secure/pairing"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// activated builds a plugin over gate and activates it against a menu that
// draws nothing, which is what a headless test has.
func activated(t *testing.T, gate *pairing.Gate) *Plugin {
	t.Helper()

	p := New(gate, 9494)
	menu := traymenu.New(traymenu.Discard())
	if err := quietAgent(t, p).Activate(menu); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	return p
}

// A device paired from the console or over the pairing server shows up without
// the operator reopening the menu. The entries belong to this plugin now, so the
// redraw is subscribed here rather than relayed through the agent.
func TestADevicePairedElsewhereRedrawsTheMenu(t *testing.T) {
	gate := pairing.New(nil, pairing.Options{ConfigDir: t.TempDir()})
	p := activated(t, gate)

	registry, ok := gate.PairedDevices().(*pairing.Registry)
	if !ok {
		t.Fatalf("the store is %T, not a registry", gate.PairedDevices())
	}
	if _, _, err := registry.Pair("phone", "android"); err != nil {
		t.Fatalf("Pair: %v", err)
	}

	rows := p.pairedDevices.Rows()
	if len(rows) != 1 || rows[0].Title != "phone (android)" {
		t.Errorf("the menu shows %v, want the paired device", rows)
	}
}

// Revoking from the menu drops the device and redraws.
func TestRevokingFromTheMenu(t *testing.T) {
	gate := pairing.New(nil, pairing.Options{ConfigDir: t.TempDir()})
	p := activated(t, gate)

	registry := gate.PairedDevices().(*pairing.Registry)
	device, _, err := registry.Pair("phone", "android")
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}

	p.revokeDevice(device.ID)

	if got := gate.PairedDevices().Count(); got != 0 {
		t.Errorf("Count() = %d, want the device revoked", got)
	}
	if rows := p.pairedDevices.Rows(); len(rows) != 0 {
		t.Errorf("the menu still shows %v", rows)
	}
}
