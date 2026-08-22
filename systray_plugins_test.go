package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/surface"
	"github.com/dotside-studios/davi-nfc-agent/tls"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// testPlugin is a consumer's feature, as far as the tray is concerned: it takes
// a menu, publishes an address, and follows the agent.
type testPlugin struct {
	info      surface.Info
	err       error
	wantsMenu bool

	host    surface.Host
	entry   *traymenu.Item
	states  []surface.State
	detachs int
}

func (p *testPlugin) Describe() surface.Info { return p.info }

func (p *testPlugin) Attach(host surface.Host) error {
	if p.err != nil {
		return p.err
	}
	p.host = host

	if p.wantsMenu {
		p.entry = host.Menu().Add("Hold Gate Open")
	}
	host.Endpoints().Set(surface.Endpoint{ID: p.info.ID, Label: "Gate", URL: "http://localhost:8080/"})
	host.Watch(func(state surface.State) { p.states = append(p.states, state) })
	return nil
}

func (p *testPlugin) Detach() { p.detachs++ }

// newTestTrayWith builds the tray with plugins registered, as main does.
func newTestTrayWith(t *testing.T, agent *Agent, plugins ...surface.Plugin) (*SystrayApp, *traymenu.Fake) {
	t.Helper()

	registry := surface.NewRegistry()
	for _, plugin := range plugins {
		if err := registry.Add(plugin); err != nil {
			t.Fatalf("Add: %v", err)
		}
	}

	fake := traymenu.NewFake()
	app := newSystrayApp(agent, "", fake)
	t.Cleanup(app.menu.Close)
	app.AttachPlugins(registry)
	app.setupUI()
	return app, fake
}

func TestPluginMenu(t *testing.T) {
	plugin := &testPlugin{info: surface.Info{ID: "turnstile", Title: "Turnstile", Tooltip: "The gate"}, wantsMenu: true}
	_, fake := newTestTrayWith(t, newTestAgent(), plugin)

	item := fake.Find("Turnstile")
	if item == nil || !item.Visible() {
		t.Fatal("the plugin's menu is not on the tray")
	}
	if item.Tooltip() != "The gate" {
		t.Errorf("menu tooltip = %q, want the one the plugin described itself with", item.Tooltip())
	}
	if entry := fake.Find("Turnstile", "Hold Gate Open"); entry == nil {
		t.Fatal("the plugin's own entry is missing")
	}

	// A menu registered after the tray was built still reads as part of the
	// agent rather than landing under Quit, which is where anything added late
	// would go.
	var seen []string
	for _, top := range fake.Items() {
		if top.Visible() && top.Title() != "" {
			seen = append(seen, top.Title())
		}
	}
	joined := strings.Join(seen, "\n")
	if strings.Index(joined, "Turnstile") > strings.Index(joined, "Start Agent") {
		t.Fatalf("the plugin menu sits below the agent's own controls:\n%s", joined)
	}
}

func TestAPluginWithNothingToShowTakesNoMenu(t *testing.T) {
	plugin := &testPlugin{info: surface.Info{ID: "headless", Title: "Headless"}}
	app, fake := newTestTrayWith(t, newTestAgent(), plugin)

	if item := fake.Find("Headless"); item != nil && item.Visible() {
		t.Fatal("a plugin that never asked for a menu was given one anyway")
	}
	if app.pluginsTaken != 0 {
		t.Errorf("%d menus were taken, want none", app.pluginsTaken)
	}

	// It is on the tray all the same: its address is.
	if rows := app.endpoints.Rows(); len(rows) != 1 || rows[0].Value.ID != "headless" {
		t.Fatalf("the plugin's address is not on the menu: %v", rows)
	}
}

func TestAPluginThatRefusesToAttachIsLeftOut(t *testing.T) {
	plugin := &testPlugin{info: surface.Info{ID: "turnstile", Title: "Turnstile"}, err: errors.New("no gate to drive")}
	working := &testPlugin{info: surface.Info{ID: "badge", Title: "Badge"}, wantsMenu: true}

	app, fake := newTestTrayWith(t, newTestAgent(), plugin, working)

	if item := fake.Find("Turnstile"); item != nil && item.Visible() {
		t.Error("a plugin that refused to attach is on the tray")
	}
	// And the one after it is unaffected: one broken feature does not take the
	// rest of the menu with it.
	if item := fake.Find("Badge", "Hold Gate Open"); item == nil {
		t.Fatal("the plugin registered after the broken one did not attach")
	}

	app.detachPlugins()
	if plugin.detachs != 0 {
		t.Error("a plugin that never attached was detached")
	}
	if working.detachs != 1 {
		t.Errorf("the attached plugin was detached %d times, want 1", working.detachs)
	}
}

func TestPluginsSeeTheAgentMove(t *testing.T) {
	agent := newTestAgent()

	devices, err := NewDeviceRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("NewDeviceRegistry: %v", err)
	}
	agent.Devices = devices

	plugin := &testPlugin{info: surface.Info{ID: "turnstile", Title: "Turnstile"}, wantsMenu: true}
	app, _ := newTestTrayWith(t, agent, plugin)

	seen := len(plugin.states)

	// A device pairs. The tray redraws its own menu for it, and the plugins
	// hear about it in the same breath: nothing here polls.
	if _, _, err := devices.Pair("Pixel", "android"); err != nil {
		t.Fatalf("Pair: %v", err)
	}
	app.refreshDevicesMenu()

	if len(plugin.states) <= seen {
		t.Fatal("the plugin was not told a device had paired")
	}
	if last := plugin.states[len(plugin.states)-1]; last.Paired != 1 {
		t.Errorf("the plugin was told %d devices are paired, want 1", last.Paired)
	}
	seen = len(plugin.states)

	// The state a plugin reads is the one it was last handed, so a plugin that
	// keeps no copy of its own cannot fall behind.
	if app.State().Paired != 1 {
		t.Errorf("State reports %d paired devices", app.State().Paired)
	}

	app.showStopped("Stopped")
	if len(plugin.states) <= seen {
		t.Fatal("the plugin was not told the agent had stopped")
	}
	if plugin.states[len(plugin.states)-1].Running {
		t.Error("the plugin was told a stopped agent is running")
	}
}

func TestPairingPluginPublishesItsPageAndPIN(t *testing.T) {
	agent := newTestAgent()
	bootstrap := tls.NewBootstrapServer(nil, 9472)

	app, fake := newTestTrayWith(t, agent, newPairingPlugin(bootstrap, 9472))

	pin := bootstrap.PIN()

	// The address is published where the tray draws addresses, PIN and all, so
	// the URL that is copied is the one that works.
	endpoint, ok := agent.Endpoints().Get(pairingEndpoint)
	if !ok {
		t.Fatal("the pairing page was not published")
	}
	if !strings.Contains(endpoint.URL, "pin="+pin) {
		t.Fatalf("pairing URL %q does not carry the PIN a phone is asked for", endpoint.URL)
	}
	if rows := app.endpoints.Rows(); len(rows) != 1 || rows[0].Value.URL != endpoint.URL {
		t.Fatalf("the pairing address is not on the menu: %v", rows)
	}

	label := fake.Find("Pair a Phone", "Pairing PIN: "+pin)
	if label == nil {
		t.Fatal("the pairing menu does not show the PIN")
	}

	// Rotating it invalidates every URL carrying the old one, so both the label
	// and the published address have to move with it.
	fake.Find("Pair a Phone", "Regenerate Pairing PIN").Deliver()
	waitFor(t, "the PIN to rotate", func() bool { return !strings.Contains(label.Title(), pin) })

	fresh := bootstrap.PIN()
	if got := label.Title(); got != "Pairing PIN: "+fresh {
		t.Errorf("the menu shows %q after a rotation, want the fresh PIN", got)
	}
	rotated, _ := agent.Endpoints().Get(pairingEndpoint)
	if !strings.Contains(rotated.URL, "pin="+fresh) {
		t.Errorf("the published URL %q still carries the old PIN", rotated.URL)
	}
}

func TestPairingPluginWithoutAServerDoesNotAttach(t *testing.T) {
	_, fake := newTestTrayWith(t, newTestAgent(), newPairingPlugin(nil, 0))

	if item := fake.Find("Pair a Phone"); item != nil && item.Visible() {
		t.Fatal("the pairing menu is on the tray with no pairing server behind it")
	}
}
