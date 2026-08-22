//go:build !nowebui

package console

import (
	"log"

	"github.com/dotside-studios/davi-nfc-agent/plugin"
	"github.com/dotside-studios/davi-nfc-agent/plugins/wsserver"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// consolePlugin is the control center as the agent sees it: two paths to serve
// and a menu to open itself from.
//
// It has no listener of its own and never had — it is served from the agent's
// port, so a browser that can reach the agent can reach the console at the same
// address under the same certificate. What changed is how it gets there: it
// asks for two paths like any other plugin, rather than the listener being
// built with a control handler it knows by name.
type consolePlugin struct {
	console *Console
	ctx     *plugin.Context
}

func (c *consolePlugin) Describe() plugin.Info {
	return plugin.Info{
		ID:      "console",
		Title:   "Control Center",
		Tooltip: "Manage this agent in a browser",
	}
}

// Init puts the console on the tray. There is nothing to start: it is served by
// whatever serves the agent.
func (c *consolePlugin) Init(ctx *plugin.Context) error {
	c.ctx = ctx

	menu := ctx.Menu()
	menu.Add("Open in Browser",
		traymenu.Tooltip("Open the control center, signed in"),
		traymenu.OnClick(c.open),
	)
	menu.Add("Copy Link",
		traymenu.Tooltip("Copy a single-use link to the control center; it expires shortly"),
		traymenu.OnClick(func() { ctx.Copy("control center link", c.url()) }),
	)

	// Anything that changes the agent is published, and the console redraws for
	// it: a mode switched from the tray, a device paired, a client connecting.
	ctx.Watch(func(plugin.State) { c.console.NotifyChange() })
	return nil
}

// Routes are the privileged API and the console itself.
//
// Neither is wrapped in CORS by whatever mounts them, which is the point: the
// API administers the agent rather than serving applications, and the console
// is a page. Nothing else should be fetching either and reading the reply.
//
// Neither carries a Label: the console is opened with a token from the menu
// above, so an address on its own is no use to anyone.
func (c *consolePlugin) Routes() []wsserver.Route {
	var routes []wsserver.Route

	if api := c.routes(); api != nil {
		routes = append(routes, wsserver.Route{Pattern: "/control/", Handler: api})
	}
	if assets := c.assets(); assets != nil {
		// The root, which the agent's banner stands down for.
		routes = append(routes, wsserver.Route{Pattern: "/", Handler: assets})
	}
	return routes
}

// url mints a single-use link, or reports why it could not.
func (c *consolePlugin) url() string {
	url, err := c.console.ConsoleURL()
	if err != nil {
		log.Printf("Failed to prepare control center URL: %v", err)
		return ""
	}
	return url
}

// open shows the console here, signed in.
func (c *consolePlugin) open() {
	url := c.url()
	if url == "" {
		return
	}

	if err := c.ctx.Open(url); err != nil {
		// Falling back to the clipboard keeps this usable on a machine with no
		// registered browser handler, which is common on minimal Linux desktops.
		c.ctx.Logf("could not open a browser (%v); copying the link instead", err)
		c.ctx.Copy("control center link", url)
	}
}

var (
	_ plugin.Plugin          = (*consolePlugin)(nil)
	_ plugin.Initer          = (*consolePlugin)(nil)
	_ wsserver.RouteProvider = (*consolePlugin)(nil)
)
