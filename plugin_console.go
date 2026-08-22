//go:build !nowebui

package main

import (
	"net/http"

	"github.com/dotside-studios/davi-nfc-agent/plugin"
	"github.com/dotside-studios/davi-nfc-agent/plugins/wsserver"
)

// consoleRoutePlugin puts the control center on whatever is serving the agent.
//
// The console has no listener of its own and never had: it is served from the
// agent's port, so that a browser reaching the agent reaches the console at the
// same address, under the same certificate. What changed is how it gets there —
// it asks for two paths like any other plugin, rather than the listener being
// built with a control handler it knows by name.
type consoleRoutePlugin struct {
	console *Console
}

// consolePlugin returns the plugin that serves the console, or nil when this
// build has none.
func consolePlugin(console *Console) plugin.Plugin {
	if console == nil {
		return nil
	}
	return &consoleRoutePlugin{console: console}
}

func (c *consoleRoutePlugin) Describe() plugin.Info {
	return plugin.Info{ID: "console", Title: "Control Center"}
}

// Routes are the privileged API and the console itself.
//
// Neither is wrapped in CORS by whatever mounts them, which is the point: the
// API administers the agent rather than serving applications, and the console
// is a page. Nothing else should be fetching either and reading the reply.
func (c *consoleRoutePlugin) Routes() []wsserver.Route {
	var routes []wsserver.Route

	if api := consoleRoutes(c.console); api != nil {
		routes = append(routes, wsserver.Route{Pattern: "/control/", Handler: api})
	}
	if assets := consoleAssets(); assets != nil {
		// The root, which the agent's banner stands down for.
		routes = append(routes, wsserver.Route{Pattern: "/", Handler: assets})
	}
	return routes
}

var (
	_ plugin.Plugin          = (*consoleRoutePlugin)(nil)
	_ wsserver.RouteProvider = (*consoleRoutePlugin)(nil)
	_ http.Handler           = http.HandlerFunc(nil)
)
