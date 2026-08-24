package tray

import (
	"testing"

	nfcagent "github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// pluginFunc is a plugin written as one function.
type pluginFunc func(nfcagent.AgentContext) error

func (f pluginFunc) Activate(ctx nfcagent.AgentContext) error { return f(ctx) }

// The section the plugins add to is declared with the rest of the menu, so
// their entries land where this build wants them rather than after Quit. It
// stays hidden until something is in it: an empty submenu says nothing.
func TestExtensionsSectionAppearsOnlyWhenAPluginFillsIt(t *testing.T) {
	quiet := newTestAgent()
	app, fake := newTestTray(t, quiet)
	app.activatePlugins()

	if item := fake.Find("Extensions"); item == nil {
		t.Fatal("the Extensions submenu was not declared")
	} else if item.Visible() {
		t.Error("an empty Extensions submenu is shown")
	}

	filled := newTestAgent()
	if err := filled.Plugins.Add(pluginFunc(func(ctx nfcagent.AgentContext) error {
		ctx.Systray.Add("Back Up Now", traymenu.Tooltip("runs a backup"))
		return nil
	})); err != nil {
		t.Fatalf("Add: %v", err)
	}

	app, fake = newTestTray(t, filled)
	app.activatePlugins()

	entry := fake.Find("Extensions", "Back Up Now")
	if entry == nil {
		t.Fatalf("the plugin's entry is not on the menu:\n%s", fake.Render())
	}
	if section := fake.Find("Extensions"); !section.Visible() {
		t.Error("the Extensions submenu is hidden with an entry in it")
	}
}

// The pairing entries belong to the pairing plugin now, so they appear in the
// section the tray hands the plugins rather than in its own URLs submenu.
func TestPairingEntriesComeFromThePlugin(t *testing.T) {
	a := newTestAgent()
	pairing := nfcagent.NewPairingPlugin(a, 9498)
	if err := a.Plugins.Add(pairing); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}

	app, fake := newTestTray(t, a)
	app.activatePlugins()

	pin := fake.Find("Extensions", "Pairing", "Pairing PIN: "+pairing.PIN())
	if pin == nil {
		t.Fatalf("the plugin's PIN entry is not on the menu:\n%s", fake.Render())
	}
	if fake.Find("Server URLs", "Pairing PIN: --") != nil {
		t.Error("the tray still declares a pairing entry of its own")
	}
	if section := fake.Find("Extensions"); !section.Visible() {
		t.Error("the Extensions submenu is hidden with the plugin's entries in it")
	}

	// Rotating relabels the entry, wherever the rotation came from: the menu
	// item, or the control center through the same method.
	before := pairing.PIN()
	fresh := pairing.RotatePIN()
	if fresh == before {
		t.Fatal("RotatePIN returned the PIN it replaced")
	}
	if got := pin.Title(); got != "Pairing PIN: "+fresh {
		t.Errorf("PIN entry reads %q, want the fresh PIN", got)
	}
}

// A build that registers no pairing plugin has no pairing entries at all.
func TestNoPairingPluginNoPairingEntries(t *testing.T) {
	app, fake := newTestTray(t, newTestAgent())
	app.activatePlugins()

	if item := fake.Find("Extensions", "Pairing"); item != nil {
		t.Errorf("a pairing submenu appeared with no plugin behind it:\n%s", fake.Render())
	}
}
