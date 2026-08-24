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

// The agent holds no pairing server, so the tray is handed one by whoever built
// it. Without one the PIN entries stay off the menu.
func TestPairingEntriesFollowTheServerTheTrayWasGiven(t *testing.T) {
	if _, fake := newTestTray(t, newTestAgent()); fake.Find("Server URLs", "Pairing PIN: --").Visible() {
		t.Error("the PIN entry is shown with no pairing server behind it")
	}

	a := newTestAgent()
	pairing := nfcagent.PairingFor(a, 9498)
	app, fake := newTestTrayWithPairing(t, a, pairing)

	pin := fake.Find("Server URLs", "Pairing PIN: --")
	if pin == nil {
		t.Fatalf("the pairing PIN entry was not declared:\n%s", fake.Render())
	}
	if !pin.Visible() {
		t.Error("the PIN entry is hidden with a pairing server behind it")
	}

	app.updateURLs()
	if got := pin.Title(); got != "Pairing PIN: "+pairing.PIN() {
		t.Errorf("PIN entry reads %q, want the server's PIN", got)
	}
}
