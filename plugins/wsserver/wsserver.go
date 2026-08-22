// Package wsserver is the agent's WebSocket endpoints as a plugin: the single
// listener a device and a web page both connect to, and the device and client
// handlers behind it.
//
// It is a plugin rather than part of the agent because an agent does not have
// to serve anything. A build that drives a reader from its own code, or serves
// it over something else entirely, leaves this out and still has an agent.
//
//	host.Use(wsserver.New(wsserver.Config{Agent: agentAdapter{agent}}))
//
// Everything it needs from the agent is [Agent], so this package knows nothing
// about how the agent is put together.
package wsserver

import (
	"errors"
	"log"
	"sync"

	"github.com/dotside-studios/davi-nfc-agent/netaddr"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/plugin"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/clientserver"
	"github.com/dotside-studios/davi-nfc-agent/server/deviceserver"
	"github.com/dotside-studios/davi-nfc-agent/server/unifiedserver"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// ID is what this plugin is registered under.
const ID = "wsserver"

// addressSlots bounds the addresses the menu shows: the two endpoints this
// server answers on, and a row for every page mounted on it. The pool is fixed
// and reused, since no platform can insert a menu item in the middle.
const addressSlots = 8

// Agent is what the servers need from the agent they serve.
//
// Every value is read again each time the listener comes up, never taken once
// and kept, so a restart after a rotated secret or a reissued certificate
// serves the new one.
type Agent interface {
	// Reader is the reader the device endpoint drives. Nil means there is
	// nothing to serve yet, and the plugin refuses to start.
	Reader() *nfc.NFCReader

	// RemoteDevices is the driver a phone reports its scans through, or nil
	// when this agent has none.
	RemoteDevices() *remotenfc.Manager

	// APISecret is the shared secret a connection handshakes with, empty when
	// there is none, and RotateAPISecret issues a fresh one. The secret is
	// asked for by these endpoints and nothing else, so the menu that hands out
	// the addresses hands out the secret with them.
	APISecret() string
	RotateAPISecret() (string, error)

	// PublicKeyPin identifies this agent to a device across certificate
	// reissues.
	PublicKeyPin() string

	// TokenVerifier checks a paired device's credential, nil when nothing
	// pairs. OriginPolicy is the live browser allowlist, nil to fall back to
	// AllowedOrigins.
	TokenVerifier() server.TokenVerifier
	OriginPolicy() server.OriginPolicy
	AllowedOrigins() []string

	// AllowedCardTypes is the card-type filter the device endpoint applies.
	AllowedCardTypes() map[string]bool

	// RequirePairedDevice reports whether only paired devices are admitted.
	RequirePairedDevice() bool

	// Port is the port to bind, and Certificates the pair that decides whether
	// it is served over TLS.
	Port() int
	Certificates() (certFile, keyFile string)

	// ClientsChanged is called whenever a client connects or disconnects.
	ClientsChanged()
}

// Config assembles the plugin.
type Config struct {
	// Agent is what is being served. Required.
	Agent Agent

	// Logf is where the listener's own diagnostics go. Nil means the standard
	// logger.
	Logf func(format string, args ...any)
}

// Plugin is the listener and the two handlers behind it.
type Plugin struct {
	config Config

	// The menu this plugin draws: a row per address, and the secret they ask
	// for. Nothing else on the tray knows what a server address is.
	addresses *traymenu.List[address]
	secret    *traymenu.Item

	mu      sync.Mutex
	mounted []Route
	bridge  *server.ServerBridge
	device  *deviceserver.Server
	client  *clientserver.Server
	unified *unifiedserver.Server
}

// address is one row of that menu: what it is called, and where it is.
type address struct {
	label string
	url   string
}

// New returns the plugin, with nothing serving yet.
func New(config Config) *Plugin { return &Plugin{config: config} }

func (p *Plugin) Describe() plugin.Info {
	return plugin.Info{ID: ID, Title: "Server URLs", Tooltip: "Where devices and web pages connect"}
}

// Init draws the menu, before anything is serving.
//
// The addresses are this plugin's to show. Nothing else on the tray knows what
// this server serves, or has to be changed for it to serve something else.
func (p *Plugin) Init(ctx *plugin.Context) error {
	if p.config.Agent == nil {
		return errors.New("there is no agent to serve")
	}

	menu := ctx.Menu()

	p.addresses = traymenu.NewList[address](menu, addressSlots)
	p.addresses.OnActivate(func(row traymenu.Row[address]) {
		ctx.Copy(row.Value.label+" URL", row.Value.url)
	})

	// The secret those addresses ask for. It is the agent's, but it is useless
	// away from the endpoints that want it, so it is handed out with them.
	noSecret := p.config.Agent.APISecret() == ""
	p.secret = menu.Add("API Secret: hidden",
		traymenu.Tooltip("Required from non-loopback phones and clients"),
		traymenu.Disabled(),
		traymenu.HiddenIf(noSecret),
	)
	menu.Add("Copy API Secret",
		traymenu.Tooltip("Copy the agent's API secret to clipboard"),
		traymenu.HiddenIf(noSecret),
		traymenu.OnClick(func() { ctx.Copy("API secret", p.config.Agent.APISecret()) }),
	)
	menu.Add("Regenerate API Secret",
		traymenu.Tooltip("Generate a fresh secret; all phones must handshake again"),
		traymenu.HiddenIf(noSecret),
		traymenu.OnClick(func() { p.rotateSecret(ctx) }),
	)

	p.showSecret()
	p.showAddresses(nil)
	return nil
}

// rotateSecret issues a fresh secret. The agent restarts its listeners for it,
// which brings this plugin back through Stop and Start, so the address rows are
// redrawn by that rather than here.
//
// It runs off the menu goroutine: the restart waits on listeners, and a handler
// that blocks holds up every other menu item.
func (p *Plugin) rotateSecret(ctx *plugin.Context) {
	go func() {
		fresh, err := p.config.Agent.RotateAPISecret()
		if err != nil {
			ctx.Logf("could not rotate the API secret: %v", err)
			return
		}

		p.showSecret()
		ctx.Logf("API secret rotated to %s…; every phone must handshake again", preview(fresh))
	}()
}

// Start binds the listener and publishes what it is answering on.
//
// It is called again after every restart — a rotated secret, a reissued
// certificate, a port that moved — and builds the whole stack afresh each time,
// reading the agent as it stands rather than as it was.
func (p *Plugin) Start(ctx *plugin.Context) error {
	agent := p.config.Agent

	reader := agent.Reader()
	if reader == nil {
		return errors.New("there is no reader to serve")
	}

	bridge := server.NewServerBridge()

	device := deviceserver.New(deviceserver.Config{
		Reader:           reader,
		DeviceManager:    agent.RemoteDevices(),
		APISecret:        agent.APISecret(),
		AllowedCardTypes: agent.AllowedCardTypes(),
		AllowedOrigins:   agent.AllowedOrigins(),
		OriginPolicy:     agent.OriginPolicy(),
		PublicKeyPin:     agent.PublicKeyPin(),
		TokenVerifier:    agent.TokenVerifier(),

		RequirePairedDevice: agent.RequirePairedDevice(),
	}, bridge)

	client := clientserver.New(clientserver.Config{
		APISecret:      agent.APISecret(),
		AllowedOrigins: agent.AllowedOrigins(),
		OriginPolicy:   agent.OriginPolicy(),
		TokenVerifier:  agent.TokenVerifier(),
		OnChange:       agent.ClientsChanged,
	}, bridge)

	certFile, keyFile := agent.Certificates()
	port := agent.Port()

	routes := p.collectRoutes(ctx)
	unified := unifiedserver.New(unifiedserver.Config{
		Port:     port,
		CertFile: certFile,
		KeyFile:  keyFile,
		Mounts:   mounts(routes),
		Logf:     p.logf,
	}, device, client)

	p.mu.Lock()
	p.bridge, p.device, p.client, p.unified = bridge, device, client, unified
	p.mounted = routes
	p.mu.Unlock()

	go func() {
		if err := unified.Start(); err != nil {
			ctx.Logf("the listener stopped: %v", err)
		}
	}()

	p.showAddresses(routes)
	p.showSecret()
	ctx.Logf("serving devices and clients on port %d", port)
	return nil
}

// Stop takes the listener down and marks every address on the menu as no longer
// answering — the two endpoints and the pages mounted on it alike. They keep
// their place: a server that is down is worth reading as down, and it comes
// back to where it was.
func (p *Plugin) Stop(ctx *plugin.Context) error {
	p.mu.Lock()
	bridge, device, client, unified := p.bridge, p.device, p.client, p.unified
	mounted := p.mounted
	p.bridge, p.device, p.client, p.unified = nil, nil, nil, nil
	p.mu.Unlock()

	if unified != nil {
		unified.Stop()
	}
	if client != nil {
		client.Stop()
	}
	if device != nil {
		device.Stop()
	}
	if bridge != nil {
		bridge.Close()
	}

	p.showAddresses(mounted)
	return nil
}

// collectRoutes asks every plugin with a page what to mount, in the order they
// were registered. The runtime is not asked: it knows nothing about HTTP, and
// this is the only thing here that does.
func (p *Plugin) collectRoutes(ctx *plugin.Context) []Route {
	var routes []Route
	for _, peer := range ctx.Host().Plugins() {
		provider, ok := peer.(RouteProvider)
		if !ok {
			continue
		}

		owner := peer.Describe().ID
		for _, route := range provider.Routes() {
			if route.Pattern == "" || route.Handler == nil {
				ctx.Logf("%s asked for a route with no %s", owner, missingPart(route))
				continue
			}
			route.Owner = owner
			routes = append(routes, route)
		}
	}
	return routes
}

func missingPart(route Route) string {
	if route.Pattern == "" {
		return "pattern"
	}
	return "handler"
}

// ---- what the agent's surfaces ask of whatever is serving ----

// Serving reports whether the listener is up.
func (p *Plugin) Serving() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.unified != nil
}

// Port is the port bound, which is not always the one configured: a port
// changed in the settings takes effect on the next listener.
func (p *Plugin) Port() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.unified == nil {
		return 0
	}
	return p.unified.Port()
}

// LastCard is the tag the client endpoint last saw, nil when there is none.
func (p *Plugin) LastCard() *nfc.Card {
	p.mu.Lock()
	client := p.client
	p.mu.Unlock()

	if client == nil {
		return nil
	}
	return client.GetLastCard()
}

// ClientCount reports how many client applications are connected.
func (p *Plugin) ClientCount() int {
	p.mu.Lock()
	client := p.client
	p.mu.Unlock()

	if client == nil {
		return 0
	}
	return client.ClientCount()
}

// Clients lists them.
func (p *Plugin) Clients() []clientserver.ClientInfo {
	p.mu.Lock()
	client := p.client
	p.mu.Unlock()

	if client == nil {
		return nil
	}
	return client.Clients()
}

// Disconnect drops one, and reports whether it was there.
func (p *Plugin) Disconnect(id string) bool {
	p.mu.Lock()
	client := p.client
	p.mu.Unlock()

	if client == nil {
		return false
	}
	return client.Disconnect(id)
}

// SetRequirePairedDevice changes the requirement on the running device
// endpoint, so the policy can be tried against a real device without a restart.
func (p *Plugin) SetRequirePairedDevice(on bool) {
	p.mu.Lock()
	device := p.device
	p.mu.Unlock()

	if device != nil {
		device.SetRequirePairedDevice(on)
	}
}

// URLs are the two addresses the listener answers on. Devices and clients share
// the port; a device asks for the device role with ?mode=device, a client opens
// plain /ws.
func (p *Plugin) URLs() (client, device string) {
	port := p.Port()
	if port == 0 {
		return "", ""
	}

	scheme := "ws"
	if certFile, keyFile := p.config.Agent.Certificates(); certFile != "" && keyFile != "" {
		scheme = "wss"
	}

	client = scheme + "://" + netaddr.HostPort(netaddr.Host(), port) + "/ws"
	return client, client + "?mode=device"
}

// showAddresses redraws the menu: the two endpoints this server answers on,
// then a row for every mounted page that asked to be listed.
//
// The addresses name the port being served rather than the one configured —
// they are pasted into a device, and one naming an unbound port is worse than
// none — and a stopped server keeps its rows and says so, rather than leaving
// an empty menu behind.
func (p *Plugin) showAddresses(routes []Route) {
	client, device := p.URLs()

	rows := []traymenu.Row[address]{
		p.row(address{label: "Device", url: device}, "Where a phone or a networked reader connects"),
		p.row(address{label: "Client", url: client}, "Where a web page connects"),
	}
	for _, route := range routes {
		if route.Label == "" {
			continue
		}
		tooltip := route.Tooltip
		if tooltip == "" {
			tooltip = "Served by " + route.Owner
		}
		rows = append(rows, p.row(address{label: route.Label, url: p.PageURL(route.Pattern)}, tooltip))
	}

	if dropped := p.addresses.Set(rows); dropped > 0 {
		p.logf("wsserver: %d more addresses are served than the menu can show", dropped)
	}
}

// row is one address as the menu reads it. A row is its own copy entry, so what
// is copied cannot drift from what is read.
func (p *Plugin) row(addr address, tooltip string) traymenu.Row[address] {
	if addr.url == "" {
		return traymenu.Row[address]{
			Value:   addr,
			Title:   addr.label + ": Not running",
			Tooltip: "Nothing is serving this address",
		}
	}
	return traymenu.Row[address]{
		Value:   addr,
		Title:   addr.label + ": " + addr.url,
		Tooltip: tooltip + ". Click to copy",
	}
}

// showSecret puts a redacted view of the secret on the menu. The whole of it is
// a click away; a tray is read over someone's shoulder.
func (p *Plugin) showSecret() {
	if p.secret == nil {
		return
	}

	secret := p.config.Agent.APISecret()
	if secret == "" {
		p.secret.SetTitle("API Secret: not set")
		return
	}
	p.secret.SetTitle("API Secret: " + preview(secret))
}

// preview is the first and last few characters, enough to tell one secret from
// the one that replaced it.
func preview(secret string) string {
	if len(secret) <= 12 {
		return secret
	}
	return secret[:4] + "…" + secret[len(secret)-4:]
}

// PageURL is where a path mounted on this listener can be reached.
func (p *Plugin) PageURL(pattern string) string {
	port := p.Port()
	if port == 0 {
		return ""
	}

	scheme := "http"
	if certFile, keyFile := p.config.Agent.Certificates(); certFile != "" && keyFile != "" {
		scheme = "https"
	}
	return scheme + "://" + netaddr.HostPort(netaddr.Host(), port) + pattern
}

func (p *Plugin) logf(format string, args ...any) {
	if p.config.Logf != nil {
		p.config.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// mounts turns the plugins' routes into the listener's.
func mounts(routes []Route) []unifiedserver.Mount {
	if len(routes) == 0 {
		return nil
	}

	list := make([]unifiedserver.Mount, 0, len(routes))
	for _, route := range routes {
		list = append(list, unifiedserver.Mount{
			Pattern: route.Pattern,
			Handler: route.Handler,
			Owner:   route.Owner,
		})
	}
	return list
}

// The phases this plugin takes part in.
var (
	_ plugin.Initer  = (*Plugin)(nil)
	_ plugin.Starter = (*Plugin)(nil)
	_ plugin.Stopper = (*Plugin)(nil)
)
