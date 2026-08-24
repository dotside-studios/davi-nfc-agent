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
