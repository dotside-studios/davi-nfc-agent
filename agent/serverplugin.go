package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/event"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/clientserver"
	"github.com/dotside-studios/davi-nfc-agent/server/listener"
	tlspkg "github.com/dotside-studios/davi-nfc-agent/tls"
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
	Component Component

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

// ServerPlugin is the agent's listener and everything served from it.
//
// It owns the [listener.Server]. It builds one from Config or serves the
// one it is given, publishes it to the agent, which mounts its own routes on
// it, then mounts the endpoints registered here. A build decides what the agent
// serves by registering one and listing what goes on it:
//
//	a.Plugins.Add(&agent.ServerPlugin{Endpoints: []agent.Endpoint{
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
type ServerPlugin struct {
	// Config is the listener to build, read only when Server is nil. The port,
	// the certificate and the advertised name fall back to the agent's own, so
	// a plugin registered with no configuration serves what the agent was set
	// up with.
	Config listener.Config

	// Server is a listener built elsewhere, for a program that mounts on it
	// before handing it over. Nil builds one from Config.
	Server *listener.Server

	// Endpoints are served in order: each is mounted, its component
	// registered, and its menu entries added. See [ServerPlugin.Add].
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

	// ServeMode names the handler serving each connection mode on /ws. A
	// connection declaring none is a client, and takes the entry under
	// server.ModeClient; server.ModeDevice is the device driver's endpoint.
	//
	// Both are mounted for you: the client server the plugin runs for the agent
	// under the first, and whatever built the device endpoint under the second.
	// An entry here replaces what would have been mounted.
	ServeMode map[string]http.Handler

	// The entries whose labels follow what is being served.
	device *traymenu.Item
	client *traymenu.Item
	secret *traymenu.Item
	logger *log.Logger
	agent  *Agent

	// clients is the server running for the agent, replaced by every start and
	// read by the routes mounted before it existed.
	clients atomic.Pointer[clientserver.Server]

	// clientChanges carries the connected count after each connect and
	// disconnect. It outlives the server emitting it, so a subscriber stays
	// connected across a restart.
	clientChanges event.Signal[int]

	// The allowlist entries, redrawn from the store rather than from clicks.
	origins        *traymenu.List[originRow]
	originAllowAny *traymenu.Item

	// originWatchers are registered before the store exists; see
	// OnOriginsChange.
	originWatchers []func()
}

var _ Plugin = (*ServerPlugin)(nil)

// Name identifies the plugin.
func (p *ServerPlugin) Name() string { return "server" }

// Add registers endpoints, in order. Call it before the agent activates its
// plugins. This is how a program puts what only it knows about, its control
// center or its own routes, on the listener the agent was set up with.
func (p *ServerPlugin) Add(endpoints ...Endpoint) {
	p.Endpoints = append(p.Endpoints, endpoints...)
}

// Listener returns the server the plugin serves from: the one it was given, or
// the one it built at activation. Nil before activation when neither.
func (p *ServerPlugin) Listener() *listener.Server { return p.Server }

// CertFile is the certificate being served, empty when none is, and TLSEnabled
// reports the same as a question. Both are what the listener resolved, which is
// not always what any one place configured.
func (p *ServerPlugin) CertFile() string {
	if p == nil || p.Server == nil {
		return ""
	}
	return p.Server.CertFile()
}

// TLSEnabled reports whether the listener is serving HTTPS and WSS.
func (p *ServerPlugin) TLSEnabled() bool {
	if p == nil || p.Server == nil {
		return false
	}
	return p.Server.TLSEnabled()
}

// Port is the port being served, which a client should be told to
// connect to. It differs from the agent's configured port after one is saved
// and before the listener is rebuilt, and is 0 before activation.
func (p *ServerPlugin) Port() int {
	if p == nil || p.Server == nil {
		return 0
	}
	return p.Server.Port()
}

// Activate publishes the listener and puts the endpoints on it.
//
// It stops at the first endpoint it cannot register, failing the agent's start.
// A control center missing its API is worse than one that is not there.
func (p *ServerPlugin) Activate(ctx AgentContext) error {
	p.logger = ctx.Logger()
	p.loadOrigins(ctx)

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
	p.agent = ctx.Agent

	for _, endpoint := range p.Endpoints {
		if err := p.register(ctx, endpoint); err != nil {
			return err
		}
	}

	if err := p.serverURLs(ctx); err != nil {
		return err
	}
	p.originsMenu(ctx)

	// The client server comes up before the listener and goes down after it:
	// components stop in reverse, and nothing new arrives while what answers it
	// is being torn down.
	if err := ctx.Use(&clientsComponent{plugin: p, agent: ctx.Agent}); err != nil {
		return err
	}
	return ctx.Use(&listenerComponent{server: p.Server, agent: ctx.Agent})
}

// Rebind stops the listener and starts it again, so a certificate reissued on
// disk is the one served. The reader, the router and the client server carry
// on; the connections they hold are dropped by the listener's own shutdown.
//
// Whoever reissues a certificate does not call this. The manager reports the
// reissue and the watch below binds again; see [tlspkg.CertificateWatcher].
func (p *ServerPlugin) Rebind() error {
	if p.Server == nil {
		return fmt.Errorf("agent: no listener to rebind")
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
		p.agent.fireServerRestart()
	}
	return nil
}

// listenerComponent binds the port for as long as the agent is running. It goes
// on as a component so that starting and stopping it is the agent's ordinary
// lifecycle rather than something the agent does about servers specifically.
type listenerComponent struct {
	server *listener.Server
	agent  *Agent
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
func (p *ServerPlugin) serverURLs(ctx AgentContext) error {
	menu := ctx.Systray.Section(p.menuTitle(), traymenu.Tooltip("Addresses this agent serves on"))

	p.device = menu.Set("device", "Device: Not running", traymenu.Tooltip("The URL a reader or a phone connects to"), traymenu.Disabled())
	menu.Set("copy-device", "  Copy Device URL",
		traymenu.OnClick(func() { copyValue(p.logger, "device URL", p.deviceURL()) }),
	)

	p.client = menu.Set("client", "Client: Not running", traymenu.Tooltip("The URL a web app connects to"), traymenu.Disabled())
	menu.Set("copy-client", "  Copy Client URL",
		traymenu.OnClick(func() { copyValue(p.logger, "client URL", p.clientURL()) }),
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
	ctx.Events.State.Connect(func(State) { p.refresh(ctx.Agent) })
	ctx.Events.Servers.Connect(func(int) { p.refresh(ctx.Agent) })
	return nil
}

// apiSecret adds the credential entries, which mean nothing without one.
func (p *ServerPlugin) apiSecret(ctx AgentContext, menu *traymenu.Section) {
	if ctx.Agent.APISecret() == "" {
		return
	}

	p.secret = menu.Set("secret", "API Secret: hidden",
		traymenu.Tooltip("Required from non-loopback phones and clients"),
		traymenu.Disabled(),
	)
	menu.Set("copy-secret", "  Copy API Secret",
		traymenu.OnClick(func() { copyValue(p.logger, "API secret", ctx.Agent.APISecret()) }),
	)
	menu.Set("rotate-secret", "  Regenerate API Secret",
		traymenu.Tooltip("Generate a fresh secret; every phone must handshake again"),
		traymenu.OnClick(func() {
			fresh, err := ctx.Agent.RotateAPISecret()
			if err != nil {
				p.logf("Failed to rotate the API secret: %v", err)
				return
			}
			p.logf("API secret rotated; the servers were restarted")
			p.secret.SetTitle("API Secret: " + redact(fresh))
		}),
	)
}

// refresh brings the addresses back in step with what is being served. Safe
// from any goroutine, as the hooks calling it need.
func (p *ServerPlugin) refresh(a *Agent) {
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
func (p *ServerPlugin) clientURL() string {
	scheme := "ws"
	if p.Config.TLSEnabled() {
		scheme = "wss"
	}
	return scheme + "://" + serviceAddress(serviceHost(), p.Port()) + "/ws"
}

func (p *ServerPlugin) deviceURL() string { return p.clientURL() + "?mode=device" }

// endpointURL is where an endpoint answers, empty for one with no route.
func (p *ServerPlugin) endpointURL(endpoint Endpoint) string {
	if endpoint.Pattern == "" {
		return ""
	}

	scheme := "http"
	if p.Config.TLSEnabled() {
		scheme = "https"
	}
	return scheme + "://" + serviceAddress(serviceHost(), p.Port()) + endpoint.Pattern
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

func (p *ServerPlugin) logf(format string, args ...any) {
	if p.logger != nil {
		p.logger.Printf(format, args...)
	}
}

// register wires one endpoint: its route and its lifetime. Its menu entries
// come later, with the addresses; see serverURLs.
func (p *ServerPlugin) register(ctx AgentContext, endpoint Endpoint) error {
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
func (p *ServerPlugin) watchCertificates(ctx AgentContext) error {
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
	})
}

// certificateWatch rebinds the listener whenever the certificate behind it is
// reissued, whoever asked for the reissue.
type certificateWatch struct {
	certificates tlspkg.CertificateWatcher
	rebind       func() error
	logf         func(format string, args ...any)
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
					w.logf("Failed to rebind the listener: %v", err)
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
func (p *ServerPlugin) config(a *Agent) listener.Config {
	cfg := p.Config
	if cfg.Port == 0 {
		cfg.Port = a.DevicePort()
	}
	if cfg.MDNSServiceName == "" {
		cfg.MDNSServiceName = a.Info().DisplayName + " Device"
	}
	return cfg
}

func (p *ServerPlugin) menuTitle() string {
	if p.MenuTitle != "" {
		return p.MenuTitle
	}
	return "Server URLs"
}

// serving is the client server running right now, nil before the agent starts
// and after it stops. As with the listener accessors, a build that registered
// no plugin holds a nil one and is answered rather than panicked.
func (p *ServerPlugin) serving() *clientserver.Server {
	if p == nil {
		return nil
	}
	return p.clients.Load()
}

// ClientCount is how many clients are connected, 0 when nothing is serving
// them.
func (p *ServerPlugin) ClientCount() int {
	serving := p.serving()
	if serving == nil {
		return 0
	}
	return serving.ClientCount()
}

// Clients lists the connected clients, most recently connected first.
func (p *ServerPlugin) Clients() []clientserver.ClientInfo {
	serving := p.serving()
	if serving == nil {
		return nil
	}
	return serving.Clients()
}

// DisconnectClient drops one client's connection. It reports an error for a
// client that is not connected, which includes one that just left.
func (p *ServerPlugin) DisconnectClient(id string) error {
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
// disconnect. The connection it returns removes it. A console built alongside
// this plugin subscribes before the agent starts, and stays subscribed across
// every restart of the server behind it.
func (p *ServerPlugin) OnClientsChange(fn func(int)) *event.Connection {
	if p == nil {
		return nil
	}
	return p.clientChanges.Connect(fn)
}

// CheckOrigin admits or rejects an upgrade by Origin, for whatever else this
// build serves on the agent's behalf, such as a device driver's endpoint.
//
// It reads the allowlist per request, so it can be handed over before the
// plugin has one and follows an origin allowed while the agent runs.
func (p *ServerPlugin) CheckOrigin() func(r *http.Request) bool {
	return func(r *http.Request) bool {
		if p.Origins == nil {
			return server.CheckOrigin(nil)(r)
		}
		return server.CheckOriginPolicy(p.Origins)(r)
	}
}

// wsHandler routes a connection to the handler for the mode it declares. A
// connection arriving before the agent is serving is told so rather than left
// waiting on a handler that does not exist yet.
func (p *ServerPlugin) wsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		byMode := map[string]http.Handler{}
		for mode, handler := range p.ServeMode {
			byMode[mode] = handler
		}

		clients := byMode[server.ModeClient]
		delete(byMode, server.ModeClient)
		if clients == nil {
			if running := p.serving(); running != nil {
				clients = http.HandlerFunc(running.ServeWS)
			}
		}
		if clients == nil {
			http.Error(w, "agent is not running", http.StatusServiceUnavailable)
			return
		}

		server.RouteByMode(clients, byMode).ServeHTTP(w, r)
	})
}

// healthHandler reports that the agent is up and how many clients it is
// serving. Mounted at both /health and /api/v1/health: the two spellings
// predate each other and clients in the wild use both.
func (p *ServerPlugin) healthHandler() http.Handler {
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

// clientsComponent runs the client server for as long as the agent is running.
// It subscribes to what the agent reports rather than being fed, so the scans a
// client receives are the ones the agent passed its own filters.
type clientsComponent struct {
	plugin *ServerPlugin
	agent  *Agent

	tags   *event.Connection
	status *event.Connection
}

func (c *clientsComponent) Name() string { return "clients" }

func (c *clientsComponent) Start(context.Context) error {
	srv := clientserver.New(clientserver.Config{
		APISecret:            c.agent.APISecret,
		OriginPolicy:         c.plugin.Origins,
		TokenVerifier:        c.agent.tokenVerifier(),
		Tags:                 c.agent,
		AllowTagModification: c.agent.TagModificationAllowed,
		OnChange:             c.plugin.clientChanges.Emit,
	})

	c.tags = c.agent.events.Tag.Connect(srv.Broadcast)
	c.status = c.agent.events.Reader.Connect(func(status nfc.DeviceStatus) {
		srv.BroadcastDeviceStatus(status)
	})

	c.plugin.clients.Store(srv)
	return nil
}

func (c *clientsComponent) Stop() error {
	c.tags.Disconnect()
	c.status.Disconnect()
	c.tags, c.status = nil, nil

	c.plugin.clients.Store(nil)
	return nil
}
