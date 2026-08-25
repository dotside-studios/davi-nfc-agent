package agent

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/server/unifiedserver"
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
	// [unifiedserver.Server.Mount] would. Leave both blank for an endpoint
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
// It owns the [unifiedserver.Server]. It builds one from Config or serves the
// one it is given, publishes it to the agent, which mounts its own routes on
// it, then mounts the endpoints registered here. A build decides what the agent
// serves by registering one and listing what goes on it:
//
//	a.Plugins.Add(&agent.ServerPlugin{Endpoints: []agent.Endpoint{
//		{Name: "control API", Pattern: "/control/", Handler: console.Routes()},
//		{Name: "control center", Pattern: "/", Handler: console.Assets()},
//	}})
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
	Config unifiedserver.Config

	// Server is a listener built elsewhere, for a program that mounts on it
	// before handing it over. Nil builds one from Config.
	Server *unifiedserver.Server

	// Endpoints are served in order: each is mounted, its component
	// registered, and its menu entries added. See [ServerPlugin.Add].
	Endpoints []Endpoint

	// MenuTitle names the tray submenu the addresses are listed under. Blank
	// uses "Server URLs".
	MenuTitle string

	// Trust supplies the certificate the listener serves and reports when it
	// is reissued, so that the listener binds again on its own. Blank serves
	// no TLS unless Config names a certificate itself.
	Trust *TrustPlugin

	// Certificates overrides where the reissue signal comes from. Blank takes
	// Trust's, which is what a build wants.
	Certificates tlspkg.CertificateWatcher

	// The entries whose labels follow what is being served.
	device *traymenu.Item
	client *traymenu.Item
	secret *traymenu.Item
	logger *log.Logger
	agent  *Agent
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
func (p *ServerPlugin) Listener() *unifiedserver.Server { return p.Server }

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

// Port is the port being served, which is what a client should be told to
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
	if p.Server == nil {
		p.Server = unifiedserver.New(p.config(ctx.Agent))
	}
	// The agent's own routes go on first, so an endpoint cannot displace /ws
	// or the health checks.
	for _, route := range ctx.Agent.Routes() {
		if err := p.Server.Mount(route.Pattern, route.Handler); err != nil {
			return fmt.Errorf("the agent's own route %q: %w", route.Pattern, err)
		}
	}
	if err := ctx.Serve(p.Server); err != nil {
		return err
	}

	if err := p.watchCertificates(ctx); err != nil {
		return err
	}
	p.logger = ctx.Logger()
	p.agent = ctx.Agent

	for _, endpoint := range p.Endpoints {
		if err := p.register(ctx, endpoint); err != nil {
			return err
		}
	}

	if err := p.serverURLs(ctx); err != nil {
		return err
	}

	// Last, so it is the first thing to come down: nothing new arrives while
	// what answers it is being torn down. Components stop in reverse.
	return ctx.Use(&listener{server: p.Server, agent: ctx.Agent})
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
		p.agent.notifyServerRestart()
	}
	return nil
}

// listener binds the port for as long as the agent is running. It goes on as a
// component so that starting and stopping it is the agent's ordinary lifecycle
// rather than something the agent does about servers specifically.
type listener struct {
	server *unifiedserver.Server
	agent  *Agent
}

func (l *listener) Name() string { return "listener" }

// Start binds before returning, so a port already in use fails the agent's
// start rather than leaving it reporting itself running with nothing listening.
func (l *listener) Start(context.Context) error { return l.server.Start() }

func (l *listener) Stop() error {
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
	ctx.Agent.OnStateChange(func(State) { p.refresh(ctx.Agent) })
	ctx.Agent.OnServerRestart(func() { p.refresh(ctx.Agent) })
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
// from any goroutine, which is what the hooks calling it need.
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
	if p.Certificates == nil {
		p.Certificates = p.Trust.Watcher()
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

// Stop ends the watch, which nothing used to do: the manager's goroutine
// outlived every agent that started one.
func (w *certificateWatch) Stop() error {
	w.certificates.StopWatching()
	return nil
}

// config fills what Config left blank from the agent.
func (p *ServerPlugin) config(a *Agent) unifiedserver.Config {
	cfg := p.Config
	if cfg.Port == 0 {
		cfg.Port = a.DevicePort()
	}
	// As a pair: half a certificate is not something to complete from
	// somewhere else.
	if cfg.CertFile == "" && cfg.KeyFile == "" {
		cfg.CertFile, cfg.KeyFile = p.Trust.CertFile(), p.Trust.KeyFile()
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
