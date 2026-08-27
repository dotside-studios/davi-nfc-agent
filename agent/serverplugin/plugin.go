package serverplugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"net/http"
	"sync"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/clipboard"
	"github.com/dotside-studios/davi-nfc-agent/event"
	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	tlspkg "github.com/dotside-studios/davi-nfc-agent/secure/tls"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/clientserver"
	"github.com/dotside-studios/davi-nfc-agent/server/listener"
	"github.com/dotside-studios/davi-nfc-agent/server/netinfo"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// Endpoint is one thing served from the agent's listener, or alongside it: a
// route, something with a lifetime, a menu entry, or any combination.
//
// It is a description rather than an interface, since that is all endpoints
// have in common. The pairing server has no route and a listener of its own,
// the control center has two routes and no lifetime, a device bridge has all
// three. The parts left blank cost nothing.
type Endpoint struct {
	// Name identifies the endpoint in logs and in the errors registering it
	// can produce. Blank falls back to the pattern it mounts on.
	Name string

	// Pattern and Handler are the route, mounted on the agent's listener as
	// [listener.Server.Mount] would. Leave both blank for an endpoint
	// serving from somewhere else, such as the pairing server, which binds a
	// port of its own.
	//
	// Whoever supplies the handler decides what stands in front of it: CORS
	// and authentication belong here, since the answer differs per route.
	Pattern string
	Handler http.Handler

	// Component, when set, starts and stops with the agent, in the order the
	// endpoints are listed.
	Component agent.Component

	// Menu, when set, adds the endpoint's entries to the tray's Server URLs
	// submenu, beside the addresses the agent serves from the same listener.
	// url is where this endpoint answers, empty for one with no route.
	//
	// An endpoint is not listed unless it asks to be: a route a person would
	// never open, such as an API behind a page, is noise beside the addresses
	// worth copying.
	Menu func(menu traymenu.Container, url string)
}

// name is what to call the endpoint in an error.
func (e Endpoint) name() string {
	switch {
	case e.Name != "":
		return e.Name
	case e.Pattern != "":
		return e.Pattern
	default:
		return "endpoint"
	}
}

// Plugin is the agent's listener and everything served from it.
//
// It owns the [listener.Server]. It builds one from Config or serves the one it
// is given, mounts what the agent is reached on, publishes it for the plugins
// registered after it, then mounts the endpoints registered here. A build decides what the agent
// serves by registering one and listing what goes on it:
//
//	a.Plugins.Add(&serverplugin.Plugin{Endpoints: []serverplugin.Endpoint{
//		{Name: "control API", Pattern: "/control/", Handler: console.Routes()},
//		{Name: "control center", Pattern: "/", Handler: console.Assets()},
//	}})
//
// It also answers for the connections: the origin allowlist that admits them is
// its own, and so are the clients holding one right now.
//
// An agent with none of these registered serves no HTTP at all, which is what a
// program driving the reader directly wants.
//
// There is one of these per agent. A second has no listener to publish and says
// so rather than quietly serving nothing.
type Plugin struct {
	// Config is the listener to build, read only when Server is nil. The port,
	// the certificate and the advertised name fall back to the agent's own, so
	// a plugin registered with no configuration serves what the agent was set
	// up with.
	Config listener.Config

	// Server is a listener built elsewhere, for a program that mounts on it
	// before handing it over. Nil builds one from Config.
	Server *listener.Server

	// Endpoints are served in order: each is mounted, its component
	// registered, and its menu entries added. See [Plugin.Add].
	Endpoints []Endpoint

	// MenuTitle names the tray submenu the addresses are listed under. Blank
	// uses "Server URLs".
	MenuTitle string

	// Certificates is the reissue signal: when it reports one, the listener
	// binds again so the new certificate is served. Nil for a build whose
	// certificate never changes under it, including one serving a certificate
	// provisioned elsewhere.
	//
	// The certificate itself comes from Config.CertFile and Config.KeyFile,
	// which Setup resolves onto Runtime.
	Certificates tlspkg.CertificateWatcher

	// Origins is the allowlist of pages permitted to connect, consulted on
	// every upgrade. Nil builds one from the agent's config directory, seeded
	// with AllowedOrigins.
	Origins *server.OriginStore

	// AllowedOrigins seeds a store built here, for a build passing through what
	// it was told on the command line. Ignored when Origins is set.
	AllowedOrigins []string

	// ClientServer is what browser clients connect to, mounted on /ws under
	// server.ModeClient. Nil builds one from the agent at activation, which is
	// what a build with nothing to say about it wants.
	//
	// It is what ClientCount, Clients and DisconnectClient report on, and lives
	// as long as the plugin: a client stays connected across a stop and start of
	// the agent, and receives again once it runs.
	ClientServer *clientserver.Server

	// ServeMode names the handler serving each connection mode on /ws. A
	// connection declaring none is a client, and takes the entry under
	// server.ModeClient; server.ModeDevice is the device driver's endpoint.
	//
	// Both are mounted for you: the client server the plugin runs for the agent
	// under the first, and whatever built the device endpoint under the second.
	// An entry here replaces what would have been mounted.
	ServeMode map[string]http.Handler

	// The entries whose labels follow what is being served.
	device   *traymenu.Item
	client   *traymenu.Item
	secret   *traymenu.Item
	logger   *log.Logger
	failures *log.Logger
	agent    *agent.Agent

	// events is what the plugin reports, published by Events. It republishes
	// what the server and the store behind it report, so a subscriber connects
	// to the plugin rather than to whichever of them this build put there.
	events     Events
	eventsOnce sync.Once

	// The allowlist entries, redrawn from the store rather than from clicks.
	origins        *traymenu.List[originRow]
	originAllowAny *traymenu.Item
}

// Events is what the server plugin reports. Both are [event.Property], so
// connecting answers with the current value: a console built alongside the
// plugin draws its first page without reading the plugin separately, and
// without missing what changed in between.
type Events struct {
	// Clients carries the connected count after each connect and disconnect,
	// and 0 while nothing is serving them.
	Clients event.Property[int]

	// Origins carries the allowlist after each change and after a refused
	// connection, which are the two things that alter what an operator is
	// shown.
	Origins event.Property[OriginState]
}

// OriginState is the allowlist as something displaying it reads it.
type OriginState struct {
	// Allowed are the origins on the list, Blocked those refused since the
	// agent started.
	Allowed []string
	Blocked []string

	// CheckDisabled reports the session-wide bypass, which admits any origin
	// until the agent restarts.
	CheckDisabled bool
}

// Events is what the plugin reports. Live before the plugin activates, so a
// console built alongside it subscribes without waiting.
func (p *Plugin) Events() *Events {
	p.eventsOnce.Do(func() {
		p.events.Clients.Current = p.ClientCount
		p.events.Origins.Current = p.OriginState
	})
	return &p.events
}

var _ agent.Plugin = (*Plugin)(nil)

// Name identifies the plugin.
func (p *Plugin) Name() string { return "server" }

// Add registers endpoints, in order. Call it before the agent activates its
// plugins. This is how a program puts what only it knows about, its control
// center or its own routes, on the listener the agent was set up with.
func (p *Plugin) Add(endpoints ...Endpoint) {
	p.Endpoints = append(p.Endpoints, endpoints...)
}

// Listener returns the server the plugin serves from: the one it was given, or
// the one it built at activation. Nil before activation when neither.
func (p *Plugin) Listener() *listener.Server { return p.Server }

// CertFile is the certificate being served, empty when none is, and TLSEnabled
// reports the same as a question. Both are what the listener resolved, which is
// not always what any one place configured.
func (p *Plugin) CertFile() string {
	if p == nil || p.Server == nil {
		return ""
	}
	return p.Server.CertFile()
}

// TLSEnabled reports whether the listener is serving HTTPS and WSS.
func (p *Plugin) TLSEnabled() bool {
	if p == nil || p.Server == nil {
		return false
	}
	return p.Server.TLSEnabled()
}

// Port is the port being served, which a client should be told to connect to.
// It differs from the agent's configured port once a preference saves a
// different one, since the listener keeps the port it was built with, and is 0
// before activation.
func (p *Plugin) Port() int {
	if p == nil || p.Server == nil {
		return 0
	}
	return p.Server.Port()
}

// Activate publishes the listener and puts the endpoints on it.
//
// It stops at the first endpoint it cannot register, failing the agent's start.
// A control center missing its API is worse than one that is not there.
func (p *Plugin) Activate(ctx agent.AgentContext) error {
	p.logger = ctx.Logger()
	p.failures = ctx.LoggerAt(logbuf.LevelError)
	p.agent = ctx.Agent
	p.loadOrigins(ctx)
	p.serveModes()

	if p.Server == nil {
		p.Server = listener.New(p.config(ctx.Agent))
	}
	// What the agent is reached on goes first, so an endpoint cannot displace
	// /ws or the health checks.
	routes := map[string]http.Handler{
		"/ws":            p.wsHandler(),
		"/health":        p.healthHandler(),
		"/api/v1/health": p.healthHandler(),
	}
	for _, pattern := range []string{"/ws", "/health", "/api/v1/health"} {
		if err := p.Server.Mount(pattern, server.CORS(routes[pattern])); err != nil {
			return fmt.Errorf("the agent's own route %q: %w", pattern, err)
		}
	}
	if err := ctx.Serve(p.Server); err != nil {
		return err
	}

	if err := p.watchCertificates(ctx); err != nil {
		return err
	}

	for _, endpoint := range p.Endpoints {
		if err := p.register(ctx, endpoint); err != nil {
			return err
		}
	}

	if err := p.serverURLs(ctx); err != nil {
		return err
	}
	p.originsMenu(ctx)

	return ctx.Use(&listenerComponent{server: p.Server, agent: ctx.Agent})
}

// Rebind stops the listener and starts it again, so a certificate reissued on
// disk is the one served. The reader, the router and the client server carry
// on; the connections they hold are dropped by the listener's own shutdown.
//
// Whoever reissues a certificate does not call this. The manager reports the
// reissue and the watch below binds again; see [tlspkg.CertificateWatcher].
func (p *Plugin) Rebind() error {
	if p.Server == nil {
		return fmt.Errorf("serverplugin: no listener to rebind")
	}

	p.logf("Rebinding the listener...")
	p.Server.Stop()

	// Brief pause to allow the port to be released.
	time.Sleep(100 * time.Millisecond)

	if err := p.Server.Start(); err != nil {
		return err
	}

	p.logf("Listener rebound successfully")
	if p.agent != nil {
		p.agent.ServerRebound()
	}
	return nil
}

// listenerComponent binds the port for as long as the agent is running. It goes
// on as a component so that starting and stopping it is the agent's ordinary
// lifecycle rather than something the agent does about servers specifically.
type listenerComponent struct {
	server *listener.Server
	agent  *agent.Agent
}

func (l *listenerComponent) Name() string { return "listener" }

// Start binds before returning, so a port already in use fails the agent's
// start rather than leaving it reporting itself running with nothing listening.
func (l *listenerComponent) Start(context.Context) error { return l.server.Start() }

func (l *listenerComponent) Stop() error {
	l.server.Stop()
	return nil
}

// serverURLs builds the submenu of addresses this listener answers on, and the
// credential a client presents to them. The plugin owns it because it owns the
// listener: what is served from a port is what the thing holding the port
// knows.
func (p *Plugin) serverURLs(ctx agent.AgentContext) error {
	menu := ctx.Systray.Section(p.menuTitle(), traymenu.Tooltip("Addresses this agent serves on"))

	p.device = menu.Set("device", "Device: Not running", traymenu.Tooltip("The URL a reader or a phone connects to"), traymenu.Disabled())
	menu.Set("copy-device", "  Copy Device URL",
		traymenu.OnClick(func() { clipboard.CopyValue(p.logger, "device URL", p.deviceURL()) }),
	)

	p.client = menu.Set("client", "Client: Not running", traymenu.Tooltip("The URL a web app connects to"), traymenu.Disabled())
	menu.Set("copy-client", "  Copy Client URL",
		traymenu.OnClick(func() { clipboard.CopyValue(p.logger, "client URL", p.clientURL()) }),
	)

	// Then whatever is mounted on the same listener, each under its own name.
	for _, endpoint := range p.Endpoints {
		if endpoint.Menu == nil {
			continue
		}
		endpoint.Menu(menu, p.endpointURL(endpoint))
	}

	p.apiSecret(ctx, menu)

	// The addresses follow the machine's own, so they are redrawn whenever the
	// agent starts or stops and whenever the listener is bound again.
	p.refresh(ctx.Agent)
	ctx.Events.State.Connect(func(agent.State) { p.refresh(ctx.Agent) })
	ctx.Events.Servers.Connect(func(int) { p.refresh(ctx.Agent) })
	return nil
}

// apiSecret adds the credential entries, which mean nothing without one.
func (p *Plugin) apiSecret(ctx agent.AgentContext, menu *traymenu.Section) {
	if ctx.Agent.APISecret() == "" {
		return
	}

	p.secret = menu.Set("secret", "API Secret: hidden",
		traymenu.Tooltip("Required from non-loopback phones and clients"),
		traymenu.Disabled(),
	)
	menu.Set("copy-secret", "  Copy API Secret",
		traymenu.OnClick(func() { clipboard.CopyValue(p.logger, "API secret", ctx.Agent.APISecret()) }),
	)
	menu.Set("rotate-secret", "  Regenerate API Secret",
		traymenu.Tooltip("Generate a fresh secret; every phone must handshake again"),
		traymenu.OnClick(func() {
			fresh, err := ctx.Agent.RotateAPISecret()
			if err != nil {
				p.failf("Failed to rotate the API secret: %v", err)
				return
			}
			p.logf("API secret rotated; it is required from the next connection")
			p.secret.SetTitle("API Secret: " + redact(fresh))
		}),
	)
}

// refresh brings the addresses back in step with what is being served. Safe
// from any goroutine, as the hooks calling it need.
func (p *Plugin) refresh(a *agent.Agent) {
	if !a.Running() {
		p.device.SetTitle("Device: Not running")
		p.client.SetTitle("Client: Not running")
		return
	}

	p.device.SetTitle("Device: " + p.deviceURL())
	p.client.SetTitle("Client: " + p.clientURL())
	if p.secret != nil {
		p.secret.SetTitle("API Secret: " + redact(a.APISecret()))
	}
}

// clientURL is where a web app connects, and deviceURL where a reader or a
// phone does. They share the port and the path, and differ by the mode a
// device declares.
func (p *Plugin) clientURL() string {
	scheme := "ws"
	if p.Config.TLSEnabled() {
		scheme = "wss"
	}
	return scheme + "://" + netinfo.ServiceAddress(p.Port()) + "/ws"
}

func (p *Plugin) deviceURL() string { return p.clientURL() + "?mode=device" }

// endpointURL is where an endpoint answers, empty for one with no route.
func (p *Plugin) endpointURL(endpoint Endpoint) string {
	if endpoint.Pattern == "" {
		return ""
	}

	scheme := "http"
	if p.Config.TLSEnabled() {
		scheme = "https"
	}
	return scheme + "://" + netinfo.ServiceAddress(p.Port()) + endpoint.Pattern
}

// redact shows enough of a secret to tell it apart from the one it replaced,
// and no more: this ends up on a screen someone else can be looking at.
func redact(secret string) string {
	if secret == "" {
		return "not set"
	}
	if len(secret) > 12 {
		return secret[:4] + "…" + secret[len(secret)-4:]
	}
	return secret
}

func (p *Plugin) logf(format string, args ...any) {
	if p.logger != nil {
		p.logger.Printf(format, args...)
	}
}

// failf reports something that did not work. The severity is stated here rather
// than left for the console to read off the words.
func (p *Plugin) failf(format string, args ...any) {
	if p.failures != nil {
		p.failures.Printf(format, args...)
		return
	}
	p.logf(format, args...)
}

// register wires one endpoint: its route and its lifetime. Its menu entries
// come later, with the addresses; see serverURLs.
func (p *Plugin) register(ctx agent.AgentContext, endpoint Endpoint) error {
	if endpoint.Pattern != "" {
		if endpoint.Handler == nil {
			return fmt.Errorf("endpoint %q: mounted on %q with no handler", endpoint.name(), endpoint.Pattern)
		}
		if err := p.Server.Mount(endpoint.Pattern, endpoint.Handler); err != nil {
			return fmt.Errorf("endpoint %q: %w", endpoint.name(), err)
		}
	}

	if endpoint.Component != nil {
		if err := ctx.Use(endpoint.Component); err != nil {
			return fmt.Errorf("endpoint %q: %w", endpoint.name(), err)
		}
	}

	return nil
}

// watchCertificates registers the component that rebinds the listener when its
// certificate is reissued. It is here rather than on the agent because the
// certificate is this plugin's configuration: an agent that serves no HTTP has
// nothing to keep current.
func (p *Plugin) watchCertificates(ctx agent.AgentContext) error {
	// A nil *tls.Manager assigned to this interface is not a nil interface, and
	// its methods dereference the receiver. Normalised here as
	// tls.NewBootstrapServer does for the authority.
	if m, ok := p.Certificates.(*tlspkg.Manager); ok && m == nil {
		p.Certificates = nil
	}
	if p.Certificates == nil {
		return nil
	}

	return ctx.Use(&certificateWatch{
		certificates: p.Certificates,
		rebind:       p.Rebind,
		logf:         ctx.Logger().Printf,
		failf:        ctx.LoggerAt(logbuf.LevelError).Printf,
	})
}

// certificateWatch rebinds the listener whenever the certificate behind it is
// reissued, whoever asked for the reissue.
type certificateWatch struct {
	certificates tlspkg.CertificateWatcher
	rebind       func() error
	logf         func(format string, args ...any)
	failf        func(format string, args ...any)
}

func (w *certificateWatch) Name() string { return "certificate watch" }

func (w *certificateWatch) Start(ctx context.Context) error {
	changes := w.certificates.WatchReissues()

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-changes:
				if !ok {
					return
				}
				w.logf("Certificate reissued; rebinding the listener")
				if err := w.rebind(); err != nil {
					w.failf("Failed to rebind the listener: %v", err)
				}
			}
		}
	}()
	return nil
}

// Stop ends the watch, so the manager's goroutine does not outlive the agent
// that started it.
func (w *certificateWatch) Stop() error {
	w.certificates.StopWatching()
	return nil
}

// config fills what Config left blank from the agent.
func (p *Plugin) config(a *agent.Agent) listener.Config {
	cfg := p.Config
	if cfg.Port == 0 {
		cfg.Port = a.DevicePort()
	}
	if cfg.MDNSServiceName == "" {
		cfg.MDNSServiceName = a.Info().DisplayName + " Device"
	}
	return cfg
}

func (p *Plugin) menuTitle() string {
	if p.MenuTitle != "" {
		return p.MenuTitle
	}
	return "Server URLs"
}

// serving is the client server mounted under server.ModeClient, nil before
// activation and for a build serving clients with something else. As with the
// listener accessors, a build that registered no plugin holds a nil one and is
// answered rather than panicked.
func (p *Plugin) serving() *clientserver.Server {
	if p == nil {
		return nil
	}
	srv, _ := p.ServeMode[server.ModeClient].(*clientserver.Server)
	return srv
}

// ClientCount is how many clients are connected, 0 when nothing is serving
// them.
func (p *Plugin) ClientCount() int {
	serving := p.serving()
	if serving == nil {
		return 0
	}
	return serving.ClientCount()
}

// Clients lists the connected clients, most recently connected first.
func (p *Plugin) Clients() []clientserver.ClientInfo {
	serving := p.serving()
	if serving == nil {
		return nil
	}
	return serving.Clients()
}

// DisconnectClient drops one client's connection. It reports an error for a
// client that is not connected, which includes one that just left.
func (p *Plugin) DisconnectClient(id string) error {
	serving := p.serving()
	if serving == nil {
		return errors.New("nothing is serving clients")
	}
	if !serving.Disconnect(id) {
		return errors.New("no such client: it may have already disconnected")
	}
	return nil
}

// OnClientsChange calls fn with the connected count after each connect and
// disconnect. The connection it returns removes it.
//
// Deprecated: use Events().Clients, which also reports the current count.
func (p *Plugin) OnClientsChange(fn func(int)) *event.Connection {
	if p == nil {
		return nil
	}
	return p.Events().Clients.Signal.Connect(fn)
}

// serveModes republishes what the client server reports, so a subscriber
// connects to the plugin rather than to the server the build mounted.
func (p *Plugin) serveModes() {
	if srv := p.serving(); srv != nil {
		srv.OnClientsChange(p.Events().Clients.Emit)
	}
}

// wsHandler routes a connection to the handler for the mode it declares.
// Clients take the modes nothing is mounted for too: a connection naming a mode
// this build does not run is not a device it can answer.
func (p *Plugin) wsHandler() http.Handler {
	byMode := maps.Clone(p.ServeMode)
	clients := p.whileRunning(byMode[server.ModeClient])
	delete(byMode, server.ModeClient)

	return server.RouteByMode(clients, byMode)
}

// whileRunning admits clients only while the agent is running. One arriving
// before it is told so rather than left holding a connection that reports
// nothing. Devices are not gated: a driver decides for itself what to do with
// one that connects early.
func (p *Plugin) whileRunning(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h == nil {
			http.Error(w, "this agent serves no clients", http.StatusServiceUnavailable)
			return
		}
		if p.agent == nil || !p.agent.Running() {
			http.Error(w, "agent is not running", http.StatusServiceUnavailable)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// healthHandler reports that the agent is up and how many clients it is
// serving. Mounted at both /health and /api/v1/health: the two spellings
// predate each other and clients in the wild use both.
func (p *Plugin) healthHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodOptions {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":    "ok",
			"type":      "agent",
			"timestamp": time.Now().Format("2006-01-02T15:04:05Z07:00"),
			"clients":   p.ClientCount(),
		})
	})
}
