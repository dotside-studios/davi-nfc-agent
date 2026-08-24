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

// The pairing entries used to be decided when the menu was declared, before the
// plugins had been activated, so a pairing server brought by one would never
// have shown its PIN.
func TestPairingEntriesFollowThePluginThatBringsThePairingServer(t *testing.T) {
	withPairing := newTestAgent()
	if err := withPairing.Plugins.Add(pluginFunc(func(ctx nfcagent.AgentContext) error {
		return ctx.Use(nfcagent.NewPairingServer(nfcagent.PairingConfig{Port: 9498}))
	})); err != nil {
		t.Fatalf("Add: %v", err)
	}

	app, fake := newTestTray(t, withPairing)

	pin := fake.Find("Server URLs", "Pairing PIN: --")
	if pin == nil {
		t.Fatal("the pairing PIN entry was not declared")
	}
	if pin.Visible() {
		t.Error("the PIN entry is shown before the plugins have been activated")
	}

	app.activatePlugins()

	if !pin.Visible() {
		t.Error("the PIN entry is still hidden with a pairing server registered")
	}
	if app.agent.BootstrapPort() != 9498 {
		t.Errorf("BootstrapPort() = %d, want the plugin's pairing server", app.agent.BootstrapPort())
	}
}

// A build with no pairing server keeps the entries off the menu entirely.
func TestPairingEntriesStayHiddenWithNoPairingServer(t *testing.T) {
	app, fake := newTestTray(t, newTestAgent())
	app.activatePlugins()

	if pin := fake.Find("Server URLs", "Pairing PIN: --"); pin.Visible() {
		t.Error("the PIN entry is shown with no pairing server behind it")
	}
}
