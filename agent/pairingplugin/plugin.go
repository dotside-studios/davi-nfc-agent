package pairingplugin

import (
	"fmt"
	"log"
	"net/url"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/clipboard"
	"github.com/dotside-studios/davi-nfc-agent/secure/pairing"
	"github.com/dotside-studios/davi-nfc-agent/server/netinfo"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// Plugin runs the pairing server and puts its entries on the tray: the
// pairing page's address, the PIN a phone must present, and the actions that
// copy or replace them.
//
// It is a wrapper around [Server], which is the component that binds the
// listener. Registering the plugin is what a build does to pair devices:
//
//	pairing := pairingplugin.New(paired.PairingServer(), 9472)
//	rt.Agent.Plugins.Add(pairing)
//
// A build wanting the listener without the menu registers the component on its
// own instead, through [agent.AgentContext.Use] or a [serverplugin.Endpoint].
type Plugin struct {
	// Server is the pairing server it runs. Required; New builds
	// one, and a caller with its own passes it here.
	Server *Server

	// MenuTitle names the submenu its entries go under. Blank uses "Pairing".
	MenuTitle string

	// The entries whose labels follow the PIN and the address.
	address *traymenu.Item
	pin     *traymenu.Item
	logger  *log.Logger
}

var _ agent.Plugin = (*Plugin)(nil)

// New runs server's listener on port and puts its entries on the tray. server
// is the pairing machinery, which belongs to whatever owns the credentials; the
// paired-device manager builds one.
//
// A zero port, or no server, returns nil, and every method tolerates a nil
// plugin.
func New(server *pairing.Server, port int) *Plugin {
	if port <= 0 || server == nil {
		return nil
	}
	return &Plugin{Server: NewServer(server, port)}
}

// Name identifies the plugin.
func (p *Plugin) Name() string { return "pairing" }

// Port reports the port the pairing server listens on, 0 when there is none.
func (p *Plugin) Port() int {
	if p == nil {
		return 0
	}
	return p.Server.Port()
}

// PIN is the code a phone must present to pair, empty when there is no pairing
// server.
func (p *Plugin) PIN() string {
	if p == nil {
		return ""
	}
	return p.Server.PIN()
}

// RotatePIN issues a fresh PIN, invalidating the pairing URLs carrying the old
// one, and relabels the menu entries that show it. Safe to call from anywhere
// that offers the action, the control center included.
func (p *Plugin) RotatePIN() string {
	if p == nil {
		return ""
	}
	fresh := p.Server.RotatePIN()
	p.refresh()
	return fresh
}

// URL is the pairing page, carrying the PIN so a link clicked from a chat goes
// straight through. Always HTTP: a phone reaches this before it trusts the
// agent's certificate, which it comes here to collect.
func (p *Plugin) URL() string {
	port := p.Port()
	if port == 0 {
		return ""
	}

	return "http://" + netinfo.ServiceAddress(port) + "/?pin=" + url.QueryEscape(p.PIN())
}

// Activate registers the pairing server and adds the plugin's menu entries.
func (p *Plugin) Activate(ctx agent.AgentContext) error {
	if p == nil {
		return nil
	}
	if p.Server == nil {
		return fmt.Errorf("no pairing server to run")
	}
	if err := ctx.Use(p.Server); err != nil {
		return err
	}
	p.logger = ctx.Logger()

	section := ctx.Systray.Section(p.menuTitle(), traymenu.Tooltip("Pair a phone with this agent"))
	p.address = section.Set("address", "Pair Phone: --", traymenu.Disabled())
	section.Set("copy-address", "Copy Pairing URL",
		traymenu.Tooltip("Copy the phone-pairing URL to the clipboard"),
		traymenu.OnClick(func() { clipboard.CopyValue(p.logger, "phone-pairing URL", p.URL()) }),
	)
	p.pin = section.Set("pin", "Pairing PIN: --", traymenu.Disabled())
	section.Set("copy-pin", "Copy Pairing PIN",
		traymenu.Tooltip("Copy the 6-digit pairing PIN to the clipboard"),
		traymenu.OnClick(func() { clipboard.CopyValue(p.logger, "pairing PIN", p.PIN()) }),
	)
	section.Set("rotate", "Regenerate Pairing PIN",
		traymenu.Tooltip("Generate a fresh PIN; existing pairing URLs become invalid"),
		traymenu.OnClick(func() {
			fresh := p.RotatePIN()
			p.logf("Pairing PIN rotated to %s", fresh)
		}),
	)

	// The address follows the machine's own, so it is redrawn whenever a
	// listener binds again as well as when the agent starts and stops.
	p.refresh()
	ctx.Events.State.Connect(func(agent.State) { p.refresh() })
	ctx.Events.Servers.Connect(func(int) { p.refresh() })
	return nil
}

// refresh brings the labels back in step with the server. Safe from any
// goroutine, as the hooks calling it need.
func (p *Plugin) refresh() {
	if p.address != nil {
		p.address.SetTitle("Pair Phone: " + p.URL())
	}
	if p.pin != nil {
		p.pin.SetTitle("Pairing PIN: " + p.PIN())
	}
}

func (p *Plugin) logf(format string, args ...any) {
	if p.logger != nil {
		p.logger.Printf(format, args...)
	}
}

func (p *Plugin) menuTitle() string {
	if p.MenuTitle != "" {
		return p.MenuTitle
	}
	return "Pairing"
}
