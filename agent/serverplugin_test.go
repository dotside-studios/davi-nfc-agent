package agent

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/server/unifiedserver"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// serverAgent is an agent whose listener comes from the plugin under test.
// Nothing binds: the routes are exercised through the mux.
func serverAgent(t *testing.T, p *ServerPlugin, extra ...Plugin) *Agent {
	t.Helper()

	return New(Config{
		Manager: nfc.NewMockManager(),
		Logger:  log.New(io.Discard, "", 0),
		Plugins: append([]Plugin{p}, extra...),
	})
}

// get asks the listener's mux for a path, without binding anything.
func get(t *testing.T, srv *unifiedserver.Server, path string) int {
	t.Helper()

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code
}

// The plugin builds the listener and hands it to the agent, which puts its own
// routes on it. Those are the agent's contract with every client library, so
// they must not depend on the plugin having listed them.
func TestServerPluginPublishesTheListenerWithTheAgentsRoutes(t *testing.T) {
	p := &ServerPlugin{}
	a := serverAgent(t, p)

	if p.Listener() != nil {
		t.Error("the listener exists before activation")
	}
	if err := a.Activate(nil); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	srv := p.Listener()
	if srv == nil {
		t.Fatal("no listener after activation")
	}
	if a.UnifiedServer != srv {
		t.Error("the agent is not serving from the plugin's listener")
	}
	if code := get(t, srv, "/health"); code != http.StatusOK {
		t.Errorf("GET /health = %d, want 200", code)
	}
	if code := get(t, srv, "/ws"); code != http.StatusServiceUnavailable {
		t.Errorf("GET /ws before Start = %d, want 503", code)
	}
}

// An endpoint is a route, a lifetime and a menu entry, in any combination.
func TestServerPluginRegistersItsEndpoints(t *testing.T) {
	component := &counter{name: "pairing"}
	p := &ServerPlugin{Endpoints: []Endpoint{
		{Name: "pairing", Component: component},
		{Name: "control API", Pattern: "/control/", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		})},
	}}
	p.Add(Endpoint{Name: "extras", Menu: func(c traymenu.Container) {
		c.Add("Copy Device URL")
	}})

	fake := traymenu.NewFake()
	menu := traymenu.New(fake)
	t.Cleanup(menu.Close)

	a := serverAgent(t, p)
	if err := a.Activate(menu); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	if code := get(t, p.Listener(), "/control/"); code != http.StatusTeapot {
		t.Errorf("GET /control/ = %d, want the endpoint's handler", code)
	}
	if comps := a.Components(); len(comps) != 1 || comps[0].Name() != "pairing" {
		t.Errorf("Components() = %v, want the endpoint's component", comps)
	}
	if item := fake.Find("Servers", "Copy Device URL"); item == nil {
		t.Errorf("the endpoint's menu entry is missing:\n%s", fake.Render())
	}
}

// The section is the plugin's, so it is created only if an endpoint puts
// something in it: a headless build should not grow an empty submenu.
func TestServerPluginAddsNoEmptySection(t *testing.T) {
	fake := traymenu.NewFake()
	menu := traymenu.New(fake)
	t.Cleanup(menu.Close)

	a := serverAgent(t, &ServerPlugin{Endpoints: []Endpoint{{Name: "quiet", Pattern: "/quiet", Handler: http.NotFoundHandler()}}})
	if err := a.Activate(menu); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	if item := fake.Find("Servers"); item != nil {
		t.Errorf("an empty section was added:\n%s", fake.Render())
	}
}

// A route declared with nothing behind it is a mistake worth reporting: the
// alternative is a path that answers 404 for reasons nobody can find.
func TestServerPluginRefusesAnEndpointWithNoHandler(t *testing.T) {
	a := serverAgent(t, &ServerPlugin{Endpoints: []Endpoint{{Name: "control API", Pattern: "/control/"}}})

	err := a.Activate(nil)
	if err == nil {
		t.Fatal("an endpoint with a pattern and no handler was accepted")
	}
	if !strings.Contains(err.Error(), "control API") {
		t.Errorf("error = %q, want it to name the endpoint", err)
	}
}

// Two endpoints on one path is a build that has mounted something twice. It
// fails where it happens, naming the second, rather than at whichever one the
// mux happens to reach.
func TestServerPluginRefusesTwoEndpointsOnOnePath(t *testing.T) {
	a := serverAgent(t, &ServerPlugin{Endpoints: []Endpoint{
		{Name: "first", Pattern: "/control/", Handler: http.NotFoundHandler()},
		{Name: "second", Pattern: "/control/", Handler: http.NotFoundHandler()},
	}})

	err := a.Activate(nil)
	if err == nil {
		t.Fatal("two endpoints mounted on one path were accepted")
	}
	if !strings.Contains(err.Error(), "second") {
		t.Errorf("error = %q, want it to name the endpoint that could not be mounted", err)
	}
}

// The listener a route was mounted on is not something to swap underneath it,
// so the second plugin to claim one says so.
func TestOnlyOnePluginServesTheListener(t *testing.T) {
	a := serverAgent(t, &ServerPlugin{}, &ServerPlugin{})

	err := a.Activate(nil)
	if err == nil {
		t.Fatal("a second listener was accepted")
	}
	if !strings.Contains(err.Error(), "already being served") {
		t.Errorf("error = %q, want it to say a listener is already served", err)
	}
}

// Mount is for a plugin that adds a route to whatever listener is already
// there. Nothing to mount on is a wiring mistake, not a route that quietly
// never answers.
func TestMountSaysSoWhenNoListenerHasBeenPublished(t *testing.T) {
	a := quietAgent(t, pluginFunc(func(ctx AgentContext) error {
		return ctx.Mount("/late", http.NotFoundHandler())
	}))

	err := a.Activate(nil)
	if err == nil {
		t.Fatal("a route was mounted with no listener to mount it on")
	}
	if !strings.Contains(err.Error(), "/late") {
		t.Errorf("error = %q, want it to name the route", err)
	}
}

// A plugin registered after the one that serves the listener can mount on it,
// which is the ordering the plugin list promises.
func TestAPluginMountsOnTheListenerPublishedBeforeIt(t *testing.T) {
	p := &ServerPlugin{}
	a := serverAgent(t, p, pluginFunc(func(ctx AgentContext) error {
		return ctx.Mount("/late", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		}))
	}))

	if err := a.Activate(nil); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if code := get(t, p.Listener(), "/late"); code != http.StatusTeapot {
		t.Errorf("GET /late = %d, want the later plugin's handler", code)
	}
}

// What Setup builds is a server plugin, so a program adds its own endpoints to
// the same listener without knowing how the port was decided.
func TestSetupBuildsAServerPluginToAddEndpointsTo(t *testing.T) {
	rt, err := Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	rt.Servers.Add(Endpoint{Name: "control center", Pattern: "/control/", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})})

	if err := rt.Agent.Activate(nil); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if code := get(t, rt.Servers.Listener(), "/control/"); code != http.StatusTeapot {
		t.Errorf("GET /control/ = %d, want the endpoint added after Setup", code)
	}
	if code := get(t, rt.Servers.Listener(), "/health"); code != http.StatusOK {
		t.Errorf("GET /health = %d, want the agent's own route still there", code)
	}
}
