package tray

import (
	"strings"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/plugin"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// trayPlugin is a consumer's feature as the tray sees one.
type trayPlugin struct {
	info      plugin.Info
	wantsMenu bool

	started int
	stopped int
	states  []plugin.State
}

func (p *trayPlugin) Describe() plugin.Info { return p.info }

func (p *trayPlugin) Init(ctx *plugin.Context) error {
	if p.wantsMenu {
		ctx.Menu().Add("Hold Gate Open")
	}
	ctx.Watch(func(state plugin.State) { p.states = append(p.states, state) })
	return nil
}

func (p *trayPlugin) Start(*plugin.Context) error {
	p.started++
	return nil
}

func (p *trayPlugin) Stop(*plugin.Context) error {
	p.stopped++
	return nil
}

// newTrayWithPlugins builds the tray with plugins registered on the a, as
// the command line does, and wires them up as the tray does on the way up.
func newTrayWithPlugins(t *testing.T, a *agent.Agent, plugins ...plugin.Plugin) (*App, *traymenu.Fake) {
	t.Helper()

	if err := a.Plugins().Use(plugins...); err != nil {
		t.Fatalf("Use: %v", err)
	}
	return newTestTray(t, a)
}

func TestAPluginsMenuSitsWithTheAgentsOwn(t *testing.T) {
	feature := &trayPlugin{
		info:      plugin.Info{ID: "turnstile", Title: "Turnstile", Tooltip: "The gate"},
		wantsMenu: true,
	}
	_, fake := newTrayWithPlugins(t, newTestAgent(), feature)

	item := fake.Find("Turnstile")
	if item == nil || !item.Visible() {
		t.Fatal("the plugin's menu is not on the tray")
	}
	if item.Tooltip() != "The gate" {
		t.Errorf("menu tooltip = %q, want the one the plugin described itself with", item.Tooltip())
	}
	if fake.Find("Turnstile", "Hold Gate Open") == nil {
		t.Fatal("the plugin's own entry is missing")
	}

	// Not under Quit, which is where anything added after the menu was built
	// would land.
	var shown []string
	for _, top := range fake.Items() {
		if top.Visible() && top.Title() != "" {
			shown = append(shown, top.Title())
		}
	}
	joined := strings.Join(shown, "\n")
	if strings.Index(joined, "Turnstile") > strings.Index(joined, "Start Agent") {
		t.Fatalf("the plugin menu sits below the a's own controls:\n%s", joined)
	}
}

func TestAPluginWithNothingToShowTakesNoMenu(t *testing.T) {
	app, fake := newTrayWithPlugins(t, newTestAgent(), &trayPlugin{
		info: plugin.Info{ID: "headless", Title: "Headless"},
	})

	if item := fake.Find("Headless"); item != nil && item.Visible() {
		t.Fatal("a plugin that never asked for a menu was given one anyway")
	}
	if app.pluginsTaken != 0 {
		t.Errorf("%d menus were taken, want none", app.pluginsTaken)
	}
}

func TestTheAgentStartsAndStopsWhatServesIt(t *testing.T) {
	a := newTestAgent()
	a.Manager = remotenfc.NewManager(remotenfc.DeviceTimeout)

	feature := &trayPlugin{info: plugin.Info{ID: "turnstile", Title: "Turnstile"}}
	newTrayWithPlugins(t, a, feature)

	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if feature.started != 1 {
		t.Fatalf("the plugin was started %d times with the a", feature.started)
	}

	// A restart of what serves the a, which is what a reissued certificate
	// or a rotated secret asks for. The reader keeps running through it.
	if err := a.RestartServers(); err != nil {
		t.Fatalf("RestartServers: %v", err)
	}
	if feature.started != 2 || feature.stopped != 1 {
		t.Fatalf("the plugin was started %d times and stopped %d", feature.started, feature.stopped)
	}
	if a.Reader == nil {
		t.Error("restarting what serves the a stopped the reader with it")
	}

	a.Stop()
	if feature.stopped != 2 {
		t.Fatalf("the plugin was stopped %d times in all", feature.stopped)
	}

	// And nothing is brought back up behind an a the operator has stopped.
	if err := a.RestartServers(); err != nil {
		t.Fatalf("RestartServers: %v", err)
	}
	if feature.started != 2 {
		t.Fatalf("a stopped a started what serves it: %d starts", feature.started)
	}

	a.Shutdown()
}

func TestCardLabelsFollowTheAgentsState(t *testing.T) {
	a := newTestAgent()
	feature := &trayPlugin{info: plugin.Info{ID: "turnstile", Title: "Turnstile"}}
	app, _ := newTrayWithPlugins(t, a, feature)

	// What the a's own watcher publishes when a card arrives. The tray and
	// the plugin read the same snapshot, so they cannot disagree about what is
	// on the reader.
	a.Plugins().Publish(plugin.State{
		Running: true,
		Card:    plugin.Card{Present: true, UID: "04A224", Type: "NTAG213"},
	})

	if got := app.mCardUID.Title(); got != "Card UID: 04A224" {
		t.Errorf("card UID label = %q", got)
	}
	if got := app.mCardType.Title(); got != "Card Type: NTAG213" {
		t.Errorf("card type label = %q", got)
	}
	if len(feature.states) == 0 || feature.states[len(feature.states)-1].Card.UID != "04A224" {
		t.Fatalf("the plugin saw %v", feature.states)
	}

	a.Plugins().Publish(plugin.State{Running: true})
	if got := app.mCardUID.Title(); got != "Card UID: None" {
		t.Errorf("card UID label with the card gone = %q", got)
	}
}
