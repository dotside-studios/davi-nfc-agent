package pairingplugin

import (
	"github.com/dotside-studios/davi-nfc-agent/secure/pairing"
	"strings"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// The plugin brings both halves: the listener as a component, and the entries
// that hand out its address and PIN.
func TestPairingPluginRegistersItsServerAndItsEntries(t *testing.T) {
	rt, err := agent.Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("agent.Setup: %v", err)
	}

	pairing := New(pairing.NewServer(pairing.ServerOptions{}), 9499)
	if err := rt.Agent.Plugins.Add(pairing); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}

	fake := traymenu.NewFake()
	menu := traymenu.New(fake)
	t.Cleanup(menu.Close)

	if err := rt.Agent.Activate(menu); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	comps := rt.Agent.Components()
	if len(comps) != 1 || comps[0].Name() != "pairing" {
		t.Fatalf("Components() = %v, want the pairing server registered", comps)
	}

	if item := fake.Find("Pairing", "Pairing PIN: "+pairing.PIN()); item == nil {
		t.Errorf("the PIN entry is missing:\n%s", fake.Render())
	}
	address := fake.Find("Pairing", "Pair Phone: "+pairing.URL())
	if address == nil {
		t.Fatalf("the address entry is missing:\n%s", fake.Render())
	}
	if !strings.Contains(address.Title(), ":9499/?pin=") {
		t.Errorf("address entry reads %q, want the pairing page carrying the PIN", address.Title())
	}
	for _, title := range []string{"Copy Pairing URL", "Copy Pairing PIN", "Regenerate Pairing PIN"} {
		if fake.Find("Pairing", title) == nil {
			t.Errorf("%q is missing from the section", title)
		}
	}
}

// Rotating relabels the entries that show the PIN, wherever the rotation came
// from: the menu item, or the control center through the same method.
func TestRotatingThePINRelabelsTheEntries(t *testing.T) {
	rt, err := agent.Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("agent.Setup: %v", err)
	}

	pairing := New(pairing.NewServer(pairing.ServerOptions{}), 9501)
	if err := rt.Agent.Plugins.Add(pairing); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}

	fake := traymenu.NewFake()
	menu := traymenu.New(fake)
	t.Cleanup(menu.Close)
	if err := rt.Agent.Activate(menu); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	pin := fake.Find("Pairing", "Pairing PIN: "+pairing.PIN())
	if pin == nil {
		t.Fatalf("the PIN entry is missing:\n%s", fake.Render())
	}

	fresh := pairing.RotatePIN()
	if got := pin.Title(); got != "Pairing PIN: "+fresh {
		t.Errorf("PIN entry reads %q, want the fresh PIN", got)
	}
	if got := fake.Find("Pairing", "Pair Phone: "+pairing.URL()); got == nil {
		t.Errorf("the address entry still carries the PIN it replaced:\n%s", fake.Render())
	}
}

// The plugin adds its entries without asking whether anyone is looking, so a
// headless agent activates it like any other.
func TestPairingPluginActivatesWithNoTray(t *testing.T) {
	rt, err := agent.Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("agent.Setup: %v", err)
	}
	if err := rt.Agent.Plugins.Add(New(pairing.NewServer(pairing.ServerOptions{}), 9503)); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}

	if err := rt.Agent.Activate(nil); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	rt.Agent.Shutdown()
}

// A plugin with no server to run says so rather than activating into nothing.
func TestPairingPluginWithNoServer(t *testing.T) {
	a := quietAgent(t, &Plugin{})

	err := a.Activate(nil)
	if err == nil {
		t.Fatal("a pairing plugin with no server was accepted")
	}
	if !strings.Contains(err.Error(), "pairing") {
		t.Errorf("error = %q, want it to name the plugin", err)
	}
}

// A nil plugin answers for a build with no pairing, which is what the console
// holds when none was registered.
func TestANilPairingPluginReportsItsAbsence(t *testing.T) {
	var pairing *Plugin

	if got := pairing.Port(); got != 0 {
		t.Errorf("Port() = %d, want 0", got)
	}
	if got := pairing.PIN(); got != "" {
		t.Errorf("PIN() = %q, want empty", got)
	}
	if got := pairing.RotatePIN(); got != "" {
		t.Errorf("RotatePIN() = %q, want empty", got)
	}
}
