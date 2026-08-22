package plugin_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/plugin"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// recorder is a plugin that takes part in every phase and writes down what it
// was asked to do, in order.
type recorder struct {
	info plugin.Info
	log  *[]string

	initErr  error
	startErr error

	// routes are what it asks to have served.
	routes []plugin.Route

	// wantsMenu has it take a menu of its own.
	wantsMenu bool

	states []plugin.State
}

func (r *recorder) Describe() plugin.Info { return r.info }

func (r *recorder) Init(ctx *plugin.Context) error {
	r.note("init")
	if r.initErr != nil {
		return r.initErr
	}
	if r.wantsMenu {
		ctx.Menu().Add("Something")
	}
	ctx.Watch(func(state plugin.State) { r.states = append(r.states, state) })
	return nil
}

func (r *recorder) Start(*plugin.Context) error {
	r.note("start")
	return r.startErr
}

func (r *recorder) Stop(*plugin.Context) error {
	r.note("stop")
	return nil
}

func (r *recorder) Close(*plugin.Context) error {
	r.note("close")
	return nil
}

func (r *recorder) Routes() []plugin.Route { return r.routes }

func (r *recorder) note(phase string) { *r.log = append(*r.log, r.info.ID+":"+phase) }

func newRecorder(id string, log *[]string) *recorder {
	return &recorder{info: plugin.Info{ID: id, Title: strings.ToUpper(id)}, log: log}
}

func TestPhasesRunInOrderAndUnwindInReverse(t *testing.T) {
	var log []string
	first, second := newRecorder("first", &log), newRecorder("second", &log)

	host := plugin.NewHarness(first, second)
	t.Cleanup(func() { _ = host.Close() })

	if err := host.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := host.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := host.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	want := []string{
		"first:init", "second:init",
		"first:start", "second:start",
		// The listener everything else is mounted on is registered first, so it
		// is the last to go.
		"second:stop", "first:stop",
		"second:close", "first:close",
	}
	if strings.Join(log, " ") != strings.Join(want, " ") {
		t.Fatalf("phases ran:\n%s\nwant:\n%s", strings.Join(log, "\n"), strings.Join(want, "\n"))
	}
}

func TestAPluginNeedsNoPhaseAtAll(t *testing.T) {
	// Identity is all that is required, and a plugin with only that must not
	// hold up the ones that do have work.
	var log []string
	quiet := &stub{info: plugin.Info{ID: "quiet"}}
	working := newRecorder("working", &log)

	host := plugin.NewHarness(quiet, working)
	t.Cleanup(func() { _ = host.Close() })

	if err := host.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if strings.Join(log, " ") != "working:init working:start" {
		t.Fatalf("phases ran: %v", log)
	}
}

func TestStartWiresUpWhatInitDidNotReach(t *testing.T) {
	// A host started without being wired up first has to run, rather than
	// quietly running nothing.
	var log []string
	host := plugin.NewHarness(newRecorder("only", &log))
	t.Cleanup(func() { _ = host.Close() })

	if err := host.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if strings.Join(log, " ") != "only:init only:start" {
		t.Fatalf("phases ran: %v", log)
	}
}

func TestAPluginThatFailsToWireUpIsDropped(t *testing.T) {
	var log []string
	broken := newRecorder("broken", &log)
	broken.initErr = errors.New("no gate to drive")
	working := newRecorder("working", &log)

	host := plugin.NewHarness(broken, working)
	t.Cleanup(func() { _ = host.Close() })

	err := host.Init()
	if err == nil || !strings.Contains(err.Error(), "no gate to drive") {
		t.Fatalf("Init returned %v, want the plugin's own reason", err)
	}
	if err := host.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// It is never started, and the one after it is unaffected: one broken
	// feature does not take the agent with it.
	if strings.Join(log, " ") != "broken:init working:init working:start" {
		t.Fatalf("phases ran: %v", log)
	}
	if _, ok := host.Lookup("broken"); ok {
		t.Error("the plugin that could not be wired up is still registered")
	}
}

func TestAPluginThatFailsToStartStaysForTheNextTry(t *testing.T) {
	var log []string
	flaky := newRecorder("flaky", &log)
	flaky.startErr = errors.New("port already bound")

	host := plugin.NewHarness(flaky)
	t.Cleanup(func() { _ = host.Close() })

	if err := host.Start(); err == nil {
		t.Fatal("Start reported no error for a plugin that could not start")
	}

	// The port frees up, and a restart brings it back rather than needing the
	// agent restarted.
	flaky.startErr = nil
	if err := host.Restart(); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if last := log[len(log)-1]; last != "flaky:start" {
		t.Fatalf("phases ran: %v", log)
	}
}

func TestRestartTakesOnlyWhatItIsAskedFor(t *testing.T) {
	var log []string
	servers, pairing := newRecorder("servers", &log), newRecorder("pairing", &log)

	host := plugin.NewHarness(servers, pairing)
	t.Cleanup(func() { _ = host.Close() })

	if err := host.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	log = nil

	if err := host.Restart("servers"); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if strings.Join(log, " ") != "servers:stop servers:start" {
		t.Fatalf("a restart of the servers touched: %v", log)
	}
}

func TestAPluginRegisteredLateJoinsWhereTheAgentIs(t *testing.T) {
	var log []string
	host := plugin.NewHarness(newRecorder("first", &log))
	t.Cleanup(func() { _ = host.Close() })

	if err := host.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	log = nil

	late := newRecorder("late", &log)
	if err := host.Use(late); err != nil {
		t.Fatalf("Use: %v", err)
	}

	if strings.Join(log, " ") != "late:init late:start" {
		t.Fatalf("a plugin registered after the agent was up ran: %v", log)
	}
}

func TestReplacingAPluginRetiresTheOneItReplaces(t *testing.T) {
	var log []string
	host := plugin.NewHarness(newRecorder("turnstile", &log))
	t.Cleanup(func() { _ = host.Close() })

	if err := host.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	log = nil

	replacement := newRecorder("turnstile", &log)
	if err := host.Use(replacement); err != nil {
		t.Fatalf("Use: %v", err)
	}

	if strings.Join(log, " ") != "turnstile:stop turnstile:close turnstile:init turnstile:start" {
		t.Fatalf("replacing a running plugin ran: %v", log)
	}
	if host.Plugins()[0] != plugin.Plugin(replacement) {
		t.Error("the replacement did not take the place of the one it replaced")
	}
}

func TestRoutesAreCollectedInRegistrationOrderAndOwned(t *testing.T) {
	var log []string
	console := newRecorder("console", &log)
	console.routes = []plugin.Route{{Pattern: "/control/", Handler: http.NotFoundHandler()}}

	turnstile := newRecorder("turnstile", &log)
	turnstile.routes = []plugin.Route{
		{Pattern: "/turnstile/", Handler: http.NotFoundHandler()},
		{Pattern: "", Handler: http.NotFoundHandler()}, // no pattern
		{Pattern: "/nothing/", Handler: nil},           // no handler
	}

	host := plugin.NewHarness(console, turnstile)
	t.Cleanup(func() { _ = host.Close() })

	routes := host.Routes()
	if len(routes) != 2 {
		t.Fatalf("collected %d routes, want the two that were complete", len(routes))
	}
	if routes[0].Pattern != "/control/" || routes[0].Owner != "console" {
		t.Errorf("first route = %+v", routes[0])
	}
	if routes[1].Pattern != "/turnstile/" || routes[1].Owner != "turnstile" {
		t.Errorf("second route = %+v", routes[1])
	}
}

func TestFindReachesAPluginByWhatItCanDo(t *testing.T) {
	var log []string
	host := plugin.NewHarness(&stub{info: plugin.Info{ID: "quiet"}}, newRecorder("servers", &log))
	t.Cleanup(func() { _ = host.Close() })

	// What the agent does to ask "is anything serving, and where": it names the
	// capability, never the plugin.
	if _, ok := plugin.Find[plugin.RouteProvider](host.Host); !ok {
		t.Fatal("Find did not reach the plugin that can serve routes")
	}
	if _, ok := plugin.Find[interface{ Port() int }](host.Host); ok {
		t.Fatal("Find claimed a capability nothing registered has")
	}
}

func TestStateReachesEveryPluginWatching(t *testing.T) {
	var log []string
	first, second := newRecorder("first", &log), newRecorder("second", &log)

	host := plugin.NewHarness(first, second)
	t.Cleanup(func() { _ = host.Close() })

	if err := host.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	host.Publish(plugin.State{Running: true, Card: plugin.Card{Present: true, UID: "04A2"}})

	for _, r := range []*recorder{first, second} {
		if len(r.states) != 1 || r.states[0].Card.UID != "04A2" {
			t.Fatalf("%s saw %v", r.info.ID, r.states)
		}
	}
	if got := host.State(); got.Card.UID != "04A2" {
		t.Fatalf("State reports %+v, want the last one published", got.Card)
	}
}

func TestAMenuIsTakenOnlyByAPluginThatWantsOne(t *testing.T) {
	var log []string
	quiet := newRecorder("quiet", &log)
	shown := newRecorder("shown", &log)
	shown.wantsMenu = true

	host := plugin.NewHarness(quiet, shown)
	t.Cleanup(func() { _ = host.Close() })

	if err := host.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	if item := host.Tray.Find("QUIET"); item != nil {
		t.Error("a plugin that never asked for a menu was given one")
	}
	if item := host.Tray.Find("SHOWN", "Something"); item == nil {
		t.Fatal("the plugin's entry is not on the menu")
	}
}

func TestAPluginRunsWithNothingDrawingItsMenu(t *testing.T) {
	// A headless build: no tray, and a plugin that fills a menu all the same.
	host := plugin.New(plugin.Config{Logf: func(string, ...any) {}})

	shown := &recorder{info: plugin.Info{ID: "shown"}, log: new([]string), wantsMenu: true}
	if err := host.Use(shown); err != nil {
		t.Fatalf("Use: %v", err)
	}
	if err := host.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// The assertion is that none of that panicked or blocked: the menu went
	// nowhere, and the plugin neither knew nor cared.
	if err := host.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestDiscardedMenusStillBehave(t *testing.T) {
	menu := traymenu.Discard()

	item := menu.Add("Nowhere", traymenu.Tooltip("nothing draws this"))
	item.SetTitle("Still nowhere")
	item.Hide()

	if item.Title() != "Still nowhere" || item.Visible() {
		t.Fatalf("a discarded item does not hold its own state: %q visible=%v", item.Title(), item.Visible())
	}
}
