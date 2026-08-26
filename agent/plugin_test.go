package agent

import (
	"context"
	"errors"
	"io"
	"log"
	"strings"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// quietAgent is an agent with nothing behind it: no listener, no reader worth
// the name, and a log that goes nowhere. Enough to activate plugins against.
func quietAgent(t *testing.T, plugins ...Plugin) *Agent {
	t.Helper()

	return New(Config{
		Manager: nfc.NewMockManager(),
		Logger:  log.New(io.Discard, "", 0),
		Plugins: plugins,
	})
}

// recorder is a plugin that notes it was activated, and what it was given.
type recorder struct {
	name string
	log  *[]string
	ctx  AgentContext
	err  error
}

func (r *recorder) Name() string { return r.name }

func (r *recorder) Activate(ctx AgentContext) error {
	r.ctx = ctx
	*r.log = append(*r.log, r.name)
	return r.err
}

// counter is a component that says how many times it was started and stopped.
type counter struct {
	name    string
	started int
	stopped int
}

func (c *counter) Name() string                { return c.name }
func (c *counter) Start(context.Context) error { c.started++; return nil }
func (c *counter) Stop() error                 { c.stopped++; return nil }

// captured is a counter that declares it captured its configuration, so a
// restart stops and starts it again. restartErr fails the second start, which
// is the one a restart makes.
type captured struct {
	counter
	restartErr error
}

func (c *captured) Rebuildable() {}

func (c *captured) Start(ctx context.Context) error {
	if err := c.counter.Start(ctx); err != nil {
		return err
	}
	if c.started > 1 {
		return c.restartErr
	}
	return nil
}

// A restart replaces the components that captured configuration and leaves the
// rest running: rotating the API secret should not take down a metrics exporter
// that follows its own.
func TestRestartingTheServersRebuildsOnlyWhatCapturedItsConfiguration(t *testing.T) {
	plain := &counter{name: "exporter"}
	rebuilt := &captured{counter: counter{name: "captured"}}

	a := quietAgent(t, &using{components: []Component{plain, rebuilt}})
	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	if err := a.RestartServers(); err != nil {
		t.Fatalf("RestartServers: %v", err)
	}

	if rebuilt.stopped != 1 || rebuilt.started != 2 {
		t.Errorf("the component that captured its configuration was stopped %d and started %d times, want 1 and 2",
			rebuilt.stopped, rebuilt.started)
	}
	if plain.stopped != 0 {
		t.Errorf("a component that follows its own configuration was stopped %d times, want 0", plain.stopped)
	}
}

// A rebuild that fails fails the restart, naming what could not be rebuilt: the
// caller rotated a secret and needs to know it did not take.
func TestARebuildThatFailsFailsTheRestart(t *testing.T) {
	broken := &captured{counter: counter{name: "captured"}, restartErr: errors.New("no")}

	a := quietAgent(t, &using{components: []Component{broken}})
	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	err := a.RestartServers()
	if err == nil {
		t.Fatal("a component that could not be rebuilt reported a successful restart")
	}
	if !strings.Contains(err.Error(), "captured") {
		t.Errorf("the restart failed with %v, want it to name the component", err)
	}
}

// using is a plugin that registers the components it was given.
type using struct {
	components []Component
}

func (u *using) Name() string { return "using" }

func (u *using) Activate(ctx AgentContext) error {
	for _, c := range u.components {
		if err := ctx.Use(c); err != nil {
			return err
		}
	}
	return nil
}

// TestPluginsActivateInOrderAndOnlyOnce is the whole contract: a plugin gets
// one call, in the order it was registered, and never again, so a restart does
// not register everything a second time.
func TestPluginsActivateInOrderAndOnlyOnce(t *testing.T) {
	var order []string
	a := quietAgent(t,
		&recorder{name: "first", log: &order},
		&recorder{name: "second", log: &order},
	)
	if err := a.Plugins.Add(&recorder{name: "third", log: &order}); err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := a.Activate(nil); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if err := a.Activate(nil); err != nil {
		t.Fatalf("Activate again: %v", err)
	}

	if got := strings.Join(order, ","); got != "first,second,third" {
		t.Errorf("activated %q, want first,second,third exactly once each", got)
	}
	if !a.Activated() {
		t.Error("Activated() = false after activating")
	}
}

// A plugin registered once the agent has activated would never be activated, so
// registering it is refused rather than accepted and forgotten.
func TestPluginsCannotBeAddedAfterActivation(t *testing.T) {
	var order []string
	a := quietAgent(t)

	if err := a.Activate(nil); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	err := a.Plugins.Add(&recorder{name: "late", log: &order})
	if err == nil {
		t.Fatal("a plugin registered after activation was accepted; it would never run")
	}
	if !strings.Contains(err.Error(), "late") {
		t.Errorf("error = %q, want it to name the plugin", err)
	}
	if len(order) != 0 {
		t.Error("the late plugin was activated")
	}
}

// A plugin that cannot wire itself in fails the start, named, rather than
// leaving an agent running without whatever it was supposed to bring.
func TestAFailingPluginFailsTheStartAndStaysFailed(t *testing.T) {
	var order []string
	a := quietAgent(t,
		&recorder{name: "ok", log: &order},
		&recorder{name: "broken", log: &order, err: errors.New("no")},
		&recorder{name: "never", log: &order},
	)

	first := a.Start("")
	if first == nil {
		t.Fatal("Start succeeded with a plugin that failed to activate")
	}
	if !strings.Contains(first.Error(), "broken") {
		t.Errorf("error = %q, want it to name the plugin", first)
	}
	if got := strings.Join(order, ","); got != "ok,broken" {
		t.Errorf("activated %q, want the run to stop at the failure", got)
	}
	if a.State() != StateStopped {
		t.Errorf("State() = %s, want stopped", a.State())
	}

	// Activation is decided once. Trying again must not run the plugins over
	// the components the first attempt already registered.
	if second := a.Start(""); second == nil || second.Error() != first.Error() {
		t.Errorf("second Start = %v, want the same failure as the first", second)
	}
	if got := strings.Join(order, ","); got != "ok,broken" {
		t.Errorf("activated %q on the second try, want no repeat", got)
	}
}

// What a plugin registers is what the agent runs: a component goes on the same
// list as anything else, and starts and stops with it.
func TestAPluginRegistersComponents(t *testing.T) {
	component := &counter{name: "worker"}
	a := quietAgent(t, pluginFunc(func(ctx AgentContext) error {
		return ctx.Use(component)
	}))

	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if component.started != 1 {
		t.Errorf("started %d times, want 1", component.started)
	}

	a.Stop()
	if component.stopped != 1 {
		t.Errorf("stopped %d times, want 1", component.stopped)
	}

	// The second start reuses the registration rather than making another.
	if err := a.Start(""); err != nil {
		t.Fatalf("second Start: %v", err)
	}
	defer a.Stop()
	if component.started != 2 {
		t.Errorf("started %d times over two starts, want 2", component.started)
	}
	if n := len(a.Components()); n != 1 {
		t.Errorf("Components() = %d, want the one registration", n)
	}
}

// Start is the backstop: a program that registers a plugin and never activates
// it still gets one, rather than an agent quietly missing half of itself.
func TestStartActivatesPluginsThatNothingElseDid(t *testing.T) {
	var order []string
	a := quietAgent(t, &recorder{name: "unactivated", log: &order})

	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	if len(order) != 1 {
		t.Errorf("activated %v, want the plugin activated by Start", order)
	}
}

// A plugin adds menu entries without asking whether anyone is looking, so the
// menu it is handed is never nil, headless or not.
func TestSystrayIsNeverNil(t *testing.T) {
	var seen traymenu.Container
	a := quietAgent(t, pluginFunc(func(ctx AgentContext) error {
		seen = ctx.Systray
		ctx.Systray.Add("Back Up Now")
		return nil
	}))

	if err := a.Activate(nil); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if seen == nil {
		t.Fatal("a plugin activated with no tray was handed a nil menu")
	}
	a.Shutdown()
}

// The entries land on the menu the host handed over, wherever that points: the
// shipped tray hands over its top level, so a plugin's entry sits beside the
// ones the tray declared itself.
func TestSystrayEntriesLandOnTheHostsMenu(t *testing.T) {
	fake := traymenu.NewFake()
	menu := traymenu.New(fake)
	t.Cleanup(menu.Close)

	menu.Add("Start Agent")
	a := quietAgent(t, pluginFunc(func(ctx AgentContext) error {
		ctx.Systray.Add("Back Up Now")
		return nil
	}))

	if err := a.Activate(menu); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	var titles []string
	for _, item := range fake.Items() {
		titles = append(titles, item.Title())
	}
	if got := strings.Join(titles, ","); got != "Start Agent,Back Up Now" {
		t.Errorf("menu reads %q, want the plugin's entry beside the host's", got)
	}
}

// A plugin is called what it says, or after its type when it says nothing.
func TestPluginName(t *testing.T) {
	var order []string
	if got := PluginName(&recorder{name: "backups", log: &order}); got != "backups" {
		t.Errorf("PluginName() = %q, want backups", got)
	}
	if got := PluginName(&ServerPlugin{}); got != "server" {
		t.Errorf("PluginName() = %q, want server", got)
	}
	if got := PluginName(&unnamed{}); got != "unnamed" {
		t.Errorf("PluginName() = %q, want the type name", got)
	}
}

type unnamed struct{}

func (*unnamed) Activate(AgentContext) error { return nil }

// pluginFunc is a plugin written as one function, which is all most of them
// are.
type pluginFunc func(AgentContext) error

func (f pluginFunc) Activate(ctx AgentContext) error { return f(ctx) }
