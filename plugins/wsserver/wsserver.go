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
)

// ID is what this plugin is registered under, and Endpoint... the addresses it
// publishes: a device connects to one, a web page to the other, and they are
// the same listener.
const (
	ID = "wsserver"

	EndpointDevice = "device"
	EndpointClient = "client"
)

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
	// there is none.
	APISecret() string

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

	mu      sync.Mutex
	bridge  *server.ServerBridge
	device  *deviceserver.Server
	client  *clientserver.Server
	unified *unifiedserver.Server
}

// New returns the plugin, with nothing serving yet.
func New(config Config) *Plugin { return &Plugin{config: config} }

func (p *Plugin) Describe() plugin.Info {
	return plugin.Info{ID: ID, Title: "Agent Server"}
}

// Init declares the addresses before anything is serving, so they hold their
// place on a menu that is drawn before the listener is up.
func (p *Plugin) Init(ctx *plugin.Context) error {
	if p.config.Agent == nil {
		return errors.New("there is no agent to serve")
	}

	ctx.Endpoints().Set(plugin.Endpoint{
		ID:      EndpointDevice,
		Label:   "Device",
		Tooltip: "Where a phone or a networked reader connects. Click to copy",
	})
	ctx.Endpoints().Set(plugin.Endpoint{
		ID:      EndpointClient,
		Label:   "Client",
		Tooltip: "Where a web page connects. Click to copy",
	})
	return nil
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

	routes := ctx.Routes()
	unified := unifiedserver.New(unifiedserver.Config{
		Port:     port,
		CertFile: certFile,
		KeyFile:  keyFile,
		Mounts:   mounts(routes),
		Logf:     p.logf,
	}, device, client)

	p.mu.Lock()
	p.bridge, p.device, p.client, p.unified = bridge, device, client, unified
	p.mu.Unlock()

	go func() {
		if err := unified.Start(); err != nil {
			ctx.Logf("the listener stopped: %v", err)
		}
	}()

	p.publish(ctx)
	p.publishRoutes(ctx, routes)
	ctx.Logf("serving devices and clients on port %d", port)
	return nil
}

// Stop takes the listener down and marks the addresses as no longer answering.
// They keep their place on the menu: a server that is down is worth reading as
// down, and it comes back to where it was.
func (p *Plugin) Stop(ctx *plugin.Context) error {
	p.mu.Lock()
	bridge, device, client, unified := p.bridge, p.device, p.client, p.unified
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

	ctx.Endpoints().SetURL(EndpointDevice, "")
	ctx.Endpoints().SetURL(EndpointClient, "")
	for _, route := range ctx.Routes() {
		if route.Label != "" {
			ctx.Endpoints().SetURL(route.EndpointID(), "")
		}
	}
	return nil
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

// publish puts the addresses where the agent's menus read them from. They name
// the port being served rather than the one configured: these are pasted into a
// device, and an address naming an unbound port is worse than none.
func (p *Plugin) publish(ctx *plugin.Context) {
	client, device := p.URLs()

	ctx.Endpoints().SetURL(EndpointDevice, device)
	ctx.Endpoints().SetURL(EndpointClient, client)
}

// publishRoutes hands out an address for every route that asked to be shown.
//
// The plugin that asked names it and this builds it, because this is what knows
// the scheme, the host and the port that was actually bound. A plugin building
// its own would be guessing at all three.
func (p *Plugin) publishRoutes(ctx *plugin.Context, routes []plugin.Route) {
	for _, route := range routes {
		if route.Label == "" {
			continue
		}
		ctx.Endpoints().Set(plugin.Endpoint{
			ID:      route.EndpointID(),
			Label:   route.Label,
			URL:     p.PageURL(route.Pattern),
			Tooltip: route.Tooltip,
		})
	}
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
func mounts(routes []plugin.Route) []unifiedserver.Mount {
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
