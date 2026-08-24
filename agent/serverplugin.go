package agent

import (
	"fmt"
	"net/http"

	"github.com/dotside-studios/davi-nfc-agent/server/unifiedserver"
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

	// Menu, when set, adds the endpoint's tray entries. It is handed the
	// section the plugin groups its endpoints under, created only when an
	// endpoint asks for it.
	Menu func(traymenu.Container)
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
// serves by what it lists:
//
//	a.Plugins.Add(&agent.ServerPlugin{
//		Config: unifiedserver.Config{Port: 9470, CertFile: cert, KeyFile: key},
//		Endpoints: []agent.Endpoint{
//			{Name: "pairing", Component: pairing},
//			{Name: "control center", Pattern: "/control/", Handler: console.Routes()},
//			{Name: "console", Pattern: "/", Handler: console.Assets()},
//		},
//	})
//
// There is one of these per agent. A second has no listener to publish and says
// so rather than quietly serving nothing.
type ServerPlugin struct {
	// Config builds the listener, and is read only when Server is nil.
	Config unifiedserver.Config

	// Server is a listener built elsewhere, for a program that mounts on it
	// before handing it over. Nil builds one from Config.
	Server *unifiedserver.Server

	// Endpoints are served in order: each is mounted, its component
	// registered, and its menu entries added. See [ServerPlugin.Add].
	Endpoints []Endpoint

	// MenuTitle names the tray submenu the endpoints' entries are grouped
	// under. Blank uses "Servers".
	MenuTitle string
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

// Activate publishes the listener and puts the endpoints on it.
//
// It stops at the first endpoint it cannot register, failing the agent's start.
// A control center missing its API is worse than one that is not there.
func (p *ServerPlugin) Activate(ctx AgentContext) error {
	if p.Server == nil {
		p.Server = unifiedserver.New(p.Config)
	}
	if err := ctx.Serve(p.Server); err != nil {
		return err
	}

	// Built on first use, so endpoints that show nothing leave no empty
	// submenu behind.
	var section traymenu.Container
	menu := func() traymenu.Container {
		if section == nil {
			section = ctx.Systray.Section(p.menuTitle())
		}
		return section
	}

	for _, endpoint := range p.Endpoints {
		if err := p.register(ctx, endpoint, menu); err != nil {
			return err
		}
	}
	return nil
}

// register wires one endpoint: its route, its lifetime, its menu.
func (p *ServerPlugin) register(ctx AgentContext, endpoint Endpoint, menu func() traymenu.Container) error {
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

	if endpoint.Menu != nil {
		endpoint.Menu(menu())
	}
	return nil
}

func (p *ServerPlugin) menuTitle() string {
	if p.MenuTitle != "" {
		return p.MenuTitle
	}
	return "Servers"
}
