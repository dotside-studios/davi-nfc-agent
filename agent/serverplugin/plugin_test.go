package serverplugin

import (
	"encoding/json"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/clientserver"
	"github.com/dotside-studios/davi-nfc-agent/server/listener"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
	"github.com/gorilla/websocket"
)

// serverAgent is an agent whose listener comes from the plugin under test, with
// the client server declared on it the way a build declares one.
//
// Most tests here reach the routes through the mux and bind nothing, but a
// started agent binds for real, so the port is one nothing else holds: package
// test binaries run beside each other and the default port is one address.
func serverAgent(t *testing.T, p *Plugin, extra ...agent.Plugin) *agent.Agent {
	t.Helper()

	a := agent.New(agent.Config{
		Manager:    nfc.NewMockManager(),
		Logger:     log.New(io.Discard, "", 0),
		DevicePort: freePort(t),
	})
	serveClients(p, a)

	if err := a.Plugins.Add(append([]agent.Plugin{p}, extra...)...); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}
	return a
}

// serveClients declares the client server on the plugin, as cmd does. Nothing
// mounts one for a build that does not ask for it, so a test that wants clients
// served says so.
func serveClients(p *Plugin, a *agent.Agent) {
	if p.ServeMode == nil {
		p.ServeMode = map[string]http.Handler{}
	}
	if p.ServeMode[server.ModeClient] != nil {
		return
	}

	p.ServeMode[server.ModeClient] = clientserver.New(clientserver.Config{
		APISecret:            a.APISecret,
		OriginPolicy:         p.OriginPolicy(),
		TokenVerifier:        a.TokenVerifier(),
		Tags:                 a,
		AllowTagModification: a.TagModificationAllowed,
		Scans:                &a.Events().Tag,
		ReaderStatus:         &a.Events().Reader,
	})
}

// get asks the listener's mux for a path, without binding anything.
func get(t *testing.T, srv *listener.Server, path string) int {
	t.Helper()

	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	return rec.Code
}

// The plugin builds the listener and hands it to the agent, which puts its own
// routes on it. Those are the agent's contract with every client library, so
// they must not depend on the plugin having listed them.
func TestServerPluginPublishesTheListenerWithTheAgentsRoutes(t *testing.T) {
	p := &Plugin{}
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
	if code := get(t, srv, "/api/v1/health"); code != http.StatusOK {
		t.Errorf("GET /api/v1/health = %d, want 200", code)
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
	p := &Plugin{Endpoints: []Endpoint{
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
	if item := fake.Find("Server URLs", "Extras: http://"+net.JoinHostPort(serviceHost(), strconv.Itoa(p.Listener().Port()))+"/extras/"); item == nil {
		t.Errorf("the endpoint's menu entry is missing, or does not carry its address:\n%s", fake.Render())
	}
}

// An endpoint is listed only if it asks to be: a route nobody opens by hand is
// noise beside the addresses worth copying.
func TestAnEndpointIsListedOnlyIfItAsks(t *testing.T) {
	fake := traymenu.NewFake()
	menu := traymenu.New(fake)
	t.Cleanup(menu.Close)

	a := serverAgent(t, &Plugin{Endpoints: []Endpoint{{Name: "quiet", Pattern: "/quiet", Handler: http.NotFoundHandler()}}})
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
	a := serverAgent(t, &Plugin{Endpoints: []Endpoint{{Name: "control API", Pattern: "/control/"}}})

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
	a := serverAgent(t, &Plugin{Endpoints: []Endpoint{
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
	a := serverAgent(t, &Plugin{}, &Plugin{})

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
	a := quietAgent(t, pluginFunc(func(ctx agent.AgentContext) error {
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
	p := &Plugin{}
	a := serverAgent(t, p, pluginFunc(func(ctx agent.AgentContext) error {
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
// with, so a program need not repeat what it already told agent.Setup.
func TestServerPluginFallsBackToTheAgentsConfiguration(t *testing.T) {
	opts := testOptions(t)
	opts.DevicePort = 9496

	rt, err := agent.Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("agent.Setup: %v", err)
	}

	servers := &Plugin{}
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
	rt, err := agent.Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("agent.Setup: %v", err)
	}
	if err := rt.Agent.Activate(nil); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	// Nothing was published to serve them, so a plugin adding a route says so.
	if err := (agent.AgentContext{Agent: rt.Agent}).Mount("/late", http.NotFoundHandler()); err == nil {
		t.Error("a route was mounted with nothing published to serve it")
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

	rt, err := agent.Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("agent.Setup: %v", err)
	}
	if err := rt.Agent.Plugins.Add(&Plugin{Certificates: certificates}); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}

	rebound := make(chan struct{}, 1)
	rt.Agent.Events().Servers.Connect(func(int) {
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

	rt, err := agent.Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("agent.Setup: %v", err)
	}
	servers := &Plugin{}
	serveClients(servers, rt.Agent)
	if err := rt.Agent.Plugins.Add(servers); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}
	if err := rt.Agent.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer rt.Agent.Stop()

	before := servers.serving()
	if before == nil {
		t.Fatal("no client server after Start")
	}

	if err := servers.Rebind(); err != nil {
		t.Fatalf("Rebind: %v", err)
	}
	if servers.serving() != before {
		t.Error("rebinding rebuilt the client server; only the listener should have moved")
	}
}

// A plugin with no listener says so rather than reporting a rebind that did not
// happen.
func TestRebindingWithNoListener(t *testing.T) {
	if err := (&Plugin{}).Rebind(); err == nil {
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
	defer func() { _ = listener.Close() }()

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

	rt, err := agent.Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("agent.Setup: %v", err)
	}
	servers := &Plugin{}
	if err := rt.Agent.Plugins.Add(servers); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}
	if err := rt.Agent.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer rt.Agent.Stop()

	readers := rt.Agent.Supervisor()
	if readers == nil {
		t.Fatal("no readers after Start")
	}

	if err := servers.Rebind(); err != nil {
		t.Fatalf("Rebind: %v", err)
	}

	if rt.Agent.Supervisor() != readers {
		t.Error("the readers were replaced by a rebind of the listener")
	}
}

// The addresses follow the agent: a stopped one hands out nothing, and starting
// it fills them in.
func TestTheAddressesFollowTheAgent(t *testing.T) {
	opts := testOptions(t)
	opts.DevicePort = freePort(t)

	rt, err := agent.Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("agent.Setup: %v", err)
	}
	servers := &Plugin{}
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
	withSecret := agent.New(agent.Config{
		Manager:   nfc.NewMockManager(),
		Logger:    log.New(io.Discard, "", 0),
		APISecret: "abcdefgh-ijklmnop",
		Plugins:   []agent.Plugin{&Plugin{}},
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

	without := serverAgent(t, &Plugin{})
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
func runs(a *agent.Agent, name string) bool {
	for _, c := range a.Components() {
		if c.Name() == name {
			return true
		}
	}
	return false
}

func names(a *agent.Agent) []string {
	var out []string
	for _, c := range a.Components() {
		out = append(out, c.Name())
	}
	return out
}

// What the agent is reached on is mounted before the endpoints, so nothing can
// displace it.
func TestTheAgentsRoutesGoOnAheadOfTheEndpoints(t *testing.T) {
	p := &Plugin{}
	if err := serverAgent(t, p).Activate(nil); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	for _, pattern := range []string{"/ws", "/health", "/api/v1/health"} {
		if code := get(t, p.Listener(), pattern); code == http.StatusNotFound {
			t.Errorf("%s is not served", pattern)
		}
	}

	// An endpoint on one of them fails the start rather than taking it over.
	a := serverAgent(t, &Plugin{Endpoints: []Endpoint{
		{Name: "impostor", Pattern: "/health", Handler: http.NotFoundHandler()},
	}})

	err := a.Activate(nil)
	if err == nil {
		t.Fatal("an endpoint displaced one of the agent's own routes")
	}
	if !strings.Contains(err.Error(), "impostor") {
		t.Errorf("error = %q, want it to name the endpoint", err)
	}
}

// The listener serves the certificate Config names, and plain HTTP when it
// names none. Which certificate that should be is agent.Setup's decision; see
// TestSetupResolvesTheCertificateToServe.
func TestTheListenerServesTheCertificateItWasGiven(t *testing.T) {
	opts := testOptions(t)
	opts.AutoTLS = true

	rt, err := agent.Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("agent.Setup: %v", err)
	}

	named := &Plugin{
		Config: listener.Config{CertFile: "/tmp/named.pem", KeyFile: "/tmp/named.key"},
	}
	if got := named.config(rt.Agent).CertFile; got != "/tmp/named.pem" {
		t.Errorf("CertFile = %q, want the one Config named", got)
	}

	// A build given no certificate serves plain HTTP rather than half a
	// configuration.
	none := &Plugin{}
	if cfg := none.config(rt.Agent); cfg.CertFile != "" || cfg.KeyFile != "" {
		t.Errorf("CertFile/KeyFile = %q/%q, want empty", cfg.CertFile, cfg.KeyFile)
	}
}

// A build that manages no certificate hands Certificates a nil *tls.Manager,
// which is not a nil interface. Without normalising it the watch starts and
// dereferences the nil receiver, so this covers the one path where a typed nil
// reaches an interface field from ordinary wiring.
func TestAnUnmanagedCertificateDoesNotStartAWatch(t *testing.T) {
	opts := testOptions(t)
	opts.AutoTLS = false
	opts.DevicePort = freePort(t)

	rt, err := agent.Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("agent.Setup: %v", err)
	}
	if rt.Certificates != nil {
		t.Fatal("this build managed a certificate, so there is no typed nil to normalise")
	}

	if err := rt.Agent.Plugins.Add(&Plugin{Certificates: rt.Certificates}); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}
	if err := rt.Agent.Start(rt.DevicePath); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(rt.Agent.Shutdown)
}

// A device that connects to an agent built with no device driver has no device
// protocol to speak to. The connection used to be accepted and then ignored:
// the device registered, got "no handler for message type" logged at it, and
// waited for a reply that could never come. It reaches the client server now,
// which answers it.
func TestADeviceConnectingWithoutADriverIsAnswered(t *testing.T) {
	p := &Plugin{}
	a := serverAgent(t, p)

	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	ts := httptest.NewServer(p.Listener().Handler())
	defer ts.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/ws?mode=device", nil)
	if err != nil {
		t.Fatalf("a device connection was refused with no driver to serve it: %v", err)
	}
	defer func() { _ = conn.Close() }()

	if err := conn.WriteJSON(protocol.WebSocketRequest{Type: "registerDevice"}); err != nil {
		t.Fatalf("write: %v", err)
	}

	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	var out protocol.WebSocketResponse
	if err := conn.ReadJSON(&out); err != nil {
		t.Fatalf("the device was left waiting for an answer: %v", err)
	}
	if out.Success {
		t.Errorf("registering a device succeeded with no driver: %+v", out)
	}
}

// mark is a handler that answers with a code, so a test can tell which one a
// connection reached.
func mark(code int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(code) })
}

// The plugin serves the client connections and mounts the routes they arrive
// on. A build with none registered serves no HTTP at all, which is what a
// program driving the readers directly wants.
func TestTheClientServerBelongsToThePlugin(t *testing.T) {
	var none *Plugin
	if got := none.ClientCount(); got != 0 {
		t.Errorf("ClientCount() = %d with no plugin registered, want 0", got)
	}
	if got := none.Clients(); got != nil {
		t.Errorf("Clients() = %v with no plugin registered, want nil", got)
	}
	if err := none.DisconnectClient("whoever"); err == nil {
		t.Error("DisconnectClient succeeded with nothing serving clients")
	}

	p := &Plugin{}
	a := serverAgent(t, p)
	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	serving := p.serving()
	if serving == nil {
		t.Error("the plugin is running no client server")
	}
	if code := get(t, p.Listener(), "/health"); code != http.StatusOK {
		t.Errorf("GET /health = %d, want 200", code)
	}

	// It belongs to the plugin, not the run: a client stays connected across a
	// stop rather than being left holding a server nothing reports to.
	a.Stop()
	if p.serving() != serving {
		t.Error("stopping the agent replaced the client server")
	}
}

// The health check answers what the listener is serving, including how many
// clients it holds: a probe reads it to tell a live agent from a bound port.
func TestTheHealthCheckCountsTheClients(t *testing.T) {
	p := &Plugin{}
	a := serverAgent(t, p)
	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	if got := health(t, p).Clients; got != 0 {
		t.Errorf("/health reports %d clients with none connected, want 0", got)
	}

	clientOf(t, p.serving())

	got := health(t, p)
	if got.Status != "ok" || got.Type != "agent" {
		t.Errorf("/health reports %+v, want an agent reporting itself up", got)
	}
	if got.Clients != 1 {
		t.Errorf("/health reports %d clients with one connected, want 1", got.Clients)
	}
}

// health reads the health check through the listener's mux.
func health(t *testing.T, p *Plugin) struct {
	Status  string `json:"status"`
	Type    string `json:"type"`
	Clients int    `json:"clients"`
} {
	t.Helper()

	var got struct {
		Status  string `json:"status"`
		Type    string `json:"type"`
		Clients int    `json:"clients"`
	}

	rec := httptest.NewRecorder()
	p.Listener().Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /health = %d, want 200", rec.Code)
	}
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decoding /health: %v", err)
	}
	return got
}

// ServeMode is what a build names its own handlers with. What it names replaces
// what the plugin would have mounted, clients included.
func TestServeModeReplacesWhatThePluginWouldMount(t *testing.T) {
	p := &Plugin{ServeMode: map[string]http.Handler{
		server.ModeClient: mark(http.StatusTeapot),
		server.ModeDevice: mark(299),
	}}
	a := serverAgent(t, p)
	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	if code := get(t, p.Listener(), "/ws"); code != http.StatusTeapot {
		t.Errorf("a client connection got %d, want the handler ServeMode named", code)
	}
	if code := get(t, p.Listener(), "/ws?mode=device"); code != 299 {
		t.Errorf("a device connection got %d, want the handler ServeMode named", code)
	}
}
