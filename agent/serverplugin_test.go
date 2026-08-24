package agent

import (
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

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
	p.Add(Endpoint{Name: "extras", Pattern: "/extras/", Handler: http.NotFoundHandler(), Menu: func(menu traymenu.Container, url string) {
		menu.Add("Extras: " + url)
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
	if !runs(a, "pairing") {
		t.Errorf("Components() = %v, want the endpoint's component among them", names(a))
	}
	// The listener is one too: its lifetime is the plugin's.
	if !runs(a, "listener") {
		t.Errorf("Components() = %v, want the listener among them", names(a))
	}
	if item := fake.Find("Server URLs", "Extras: http://"+serviceAddress(serviceHost(), p.Listener().Port())+"/extras/"); item == nil {
		t.Errorf("the endpoint's menu entry is missing, or does not carry its address:\n%s", fake.Render())
	}
}

// An endpoint is listed only if it asks to be: a route nobody opens by hand is
// noise beside the addresses worth copying.
func TestAnEndpointIsListedOnlyIfItAsks(t *testing.T) {
	fake := traymenu.NewFake()
	menu := traymenu.New(fake)
	t.Cleanup(menu.Close)

	a := serverAgent(t, &ServerPlugin{Endpoints: []Endpoint{{Name: "quiet", Pattern: "/quiet", Handler: http.NotFoundHandler()}}})
	if err := a.Activate(menu); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	if item := fake.Find("Server URLs", "quiet"); item != nil {
		t.Errorf("an endpoint with no menu of its own was listed:\n%s", fake.Render())
	}
	// The addresses the listener answers on are always there.
	if item := fake.Find("Server URLs", "Device: Not running"); item == nil {
		t.Errorf("the device address is missing:\n%s", fake.Render())
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

// A plugin registered with no configuration serves what the agent was set up
// with, so a program need not repeat what it already told Setup.
func TestServerPluginFallsBackToTheAgentsConfiguration(t *testing.T) {
	opts := testOptions(t)
	opts.DevicePort = 9496
	opts.Explicit.Port = true

	rt, err := Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	servers := &ServerPlugin{}
	servers.Add(Endpoint{Name: "control center", Pattern: "/control/", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	})})
	if err := rt.Agent.Plugins.Add(servers); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}
	if err := rt.Agent.Activate(nil); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	if got := servers.Listener().Port(); got != 9496 {
		t.Errorf("Port() = %d, want the port the agent was set up with", got)
	}
	if code := get(t, servers.Listener(), "/control/"); code != http.StatusTeapot {
		t.Errorf("GET /control/ = %d, want the endpoint the program added", code)
	}
	if code := get(t, servers.Listener(), "/health"); code != http.StatusOK {
		t.Errorf("GET /health = %d, want the agent's own route still there", code)
	}
}

// An agent with no server plugin registered serves no HTTP, which is what a
// program driving the reader directly wants.
func TestAnAgentWithNoServerPluginServesNothing(t *testing.T) {
	rt, err := Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := rt.Agent.Activate(nil); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	if rt.Agent.UnifiedServer != nil {
		t.Error("a listener appeared with no server plugin registered")
	}
}

// fakeCertificates stands in for the TLS manager's watching half.
type fakeCertificates struct {
	changes chan struct{}
	stopped chan struct{}
}

func newFakeCertificates() *fakeCertificates {
	return &fakeCertificates{changes: make(chan struct{}, 1), stopped: make(chan struct{})}
}

func (f *fakeCertificates) WatchReissues() <-chan struct{} { return f.changes }

func (f *fakeCertificates) StopWatching() {
	select {
	case <-f.stopped:
	default:
		close(f.stopped)
	}
}

// A reissued certificate reaches a browser only on a fresh listener, so the
// plugin binds again when its watcher reports one, whoever asked for it.
func TestTheListenerRebindsWhenItsCertificateIsReissued(t *testing.T) {
	certificates := newFakeCertificates()

	opts := testOptions(t)
	opts.DevicePort = freePort(t)
	opts.Explicit.Port = true

	rt, err := Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := rt.Agent.Plugins.Add(&ServerPlugin{Certificates: certificates}); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}

	rebound := make(chan struct{}, 1)
	rt.Agent.OnServerRestart(func() {
		select {
		case rebound <- struct{}{}:
		default:
		}
	})

	if err := rt.Agent.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}

	certificates.changes <- struct{}{}
	select {
	case <-rebound:
	case <-time.After(5 * time.Second):
		t.Fatal("a reissued certificate did not rebind the listener")
	}

	// The watch ends with the agent. Nothing used to end it at all.
	rt.Agent.Stop()
	select {
	case <-certificates.stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("stopping the agent left the certificate watch running")
	}
}

// The listener is rebound, not rebuilt: what the client server captured when it
// was built is still there afterwards.
func TestRebindingLeavesTheServingStateAlone(t *testing.T) {
	opts := testOptions(t)
	opts.DevicePort = freePort(t)
	opts.Explicit.Port = true

	rt, err := Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	servers := &ServerPlugin{}
	if err := rt.Agent.Plugins.Add(servers); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}
	if err := rt.Agent.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer rt.Agent.Stop()

	before := rt.Agent.ClientServer
	if before == nil {
		t.Fatal("no client server after Start")
	}

	if err := servers.Rebind(); err != nil {
		t.Fatalf("Rebind: %v", err)
	}
	if rt.Agent.ClientServer != before {
		t.Error("rebinding rebuilt the client server; only the listener should have moved")
	}

	// RestartServers is the one that rebuilds it, which is what an API secret
	// rotation needs.
	if err := rt.Agent.RestartServers(); err != nil {
		t.Fatalf("RestartServers: %v", err)
	}
	if rt.Agent.ClientServer == before {
		t.Error("RestartServers left the old client server in place")
	}
}

// A plugin with no listener says so rather than reporting a rebind that did not
// happen.
func TestRebindingWithNoListener(t *testing.T) {
	if err := (&ServerPlugin{}).Rebind(); err == nil {
		t.Error("rebinding succeeded with no listener to rebind")
	}
}

// freePort asks the kernel for a port nothing is using, so these tests can bind
// beside an agent already running on this machine.
func freePort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	defer listener.Close()

	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("read the reserved port: %v", err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("parse the reserved port: %v", err)
	}
	return n
}

// Restarting the servers left a second worker polling the same reader, racing
// the first and reporting every card twice. The reader's lifetime is the
// agent's, so a restart leaves it alone.
func TestRestartingTheServersLeavesTheReaderAlone(t *testing.T) {
	opts := testOptions(t)
	opts.DevicePort = freePort(t)
	opts.Explicit.Port = true

	rt, err := Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	servers := &ServerPlugin{}
	if err := rt.Agent.Plugins.Add(servers); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}
	if err := rt.Agent.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer rt.Agent.Stop()

	reader := rt.Agent.Reader()
	if reader == nil {
		t.Fatal("no reader after Start")
	}

	if err := rt.Agent.RestartServers(); err != nil {
		t.Fatalf("RestartServers: %v", err)
	}
	if err := servers.Rebind(); err != nil {
		t.Fatalf("Rebind: %v", err)
	}

	if rt.Agent.Reader() != reader {
		t.Error("the reader was replaced by a restart of the servers")
	}
}

// The addresses follow the agent: a stopped one hands out nothing, and starting
// it fills them in.
func TestTheAddressesFollowTheAgent(t *testing.T) {
	opts := testOptions(t)
	opts.DevicePort = freePort(t)
	opts.Explicit.Port = true

	rt, err := Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	servers := &ServerPlugin{}
	if err := rt.Agent.Plugins.Add(servers); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}

	fake := traymenu.NewFake()
	menu := traymenu.New(fake)
	t.Cleanup(menu.Close)
	if err := rt.Agent.Activate(menu); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	device := fake.Find("Server URLs", "Device: Not running")
	if device == nil {
		t.Fatalf("the device address is missing:\n%s", fake.Render())
	}

	if err := rt.Agent.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := device.Title(); got != "Device: "+servers.deviceURL() {
		t.Errorf("device entry reads %q, want the address it is serving on", got)
	}
	if !strings.HasSuffix(device.Title(), "/ws?mode=device") {
		t.Errorf("device entry reads %q, want it to carry the device mode", device.Title())
	}

	rt.Agent.Stop()
	if got := device.Title(); got != "Device: Not running" {
		t.Errorf("a stopped agent still shows %q", got)
	}
}

// The secret is shown redacted, so an operator can tell it changed without it
// being readable over their shoulder. No secret, no entries.
func TestTheAPISecretIsListedRedacted(t *testing.T) {
	withSecret := New(Config{
		Manager:   nfc.NewMockManager(),
		Logger:    log.New(io.Discard, "", 0),
		APISecret: "abcdefgh-ijklmnop",
		Plugins:   []Plugin{&ServerPlugin{}},
	})

	fake := traymenu.NewFake()
	menu := traymenu.New(fake)
	t.Cleanup(menu.Close)
	if err := withSecret.Activate(menu); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	secret := fake.Find("Server URLs", "API Secret: hidden")
	if secret == nil {
		t.Fatalf("the API secret entry is missing:\n%s", fake.Render())
	}
	if got := redact("abcdefgh-ijklmnop"); strings.Contains(got, "efgh-ijkl") {
		t.Errorf("redact() = %q, want the middle hidden", got)
	}

	without := serverAgent(t, &ServerPlugin{})
	plainFake := traymenu.NewFake()
	plainMenu := traymenu.New(plainFake)
	t.Cleanup(plainMenu.Close)
	if err := without.Activate(plainMenu); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if item := plainFake.Find("Server URLs", "API Secret: hidden"); item != nil {
		t.Errorf("the API secret is listed with none configured:\n%s", plainFake.Render())
	}
}

// runs reports whether a component of that name is registered, and names lists
// them for a failure message. The plugin registers a listener of its own, so a
// count is no longer the thing to assert.
func runs(a *Agent, name string) bool {
	for _, c := range a.Components() {
		if c.Name() == name {
			return true
		}
	}
	return false
}

func names(a *Agent) []string {
	var out []string
	for _, c := range a.Components() {
		out = append(out, c.Name())
	}
	return out
}
