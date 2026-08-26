package agent

import (
	"fmt"
	"log"
	"net/url"

	tlspkg "github.com/dotside-studios/davi-nfc-agent/tls"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// PairingPlugin runs the pairing server and puts its entries on the tray: the
// pairing page's address, the PIN a phone must present, and the actions that
// copy or replace them.
//
// It is a wrapper around [PairingServer], which is the component that binds the
// listener. Registering the plugin is what a build does to pair devices:
//
//	pairing := agent.NewPairingPlugin(rt.Agent, 9472)
//	rt.Agent.Plugins.Add(pairing)
//
// A build wanting the listener without the menu registers the component on its
// own instead, through [AgentContext.Use] or an [Endpoint].
type PairingPlugin struct {
	// Server is the pairing server it runs. Required; NewPairingPlugin builds
	// one, and a caller with its own passes it here.
	Server *PairingServer

	// MenuTitle names the submenu its entries go under. Blank uses "Pairing".
	MenuTitle string

	// The entries whose labels follow the PIN and the address.
	address *traymenu.Item
	pin     *traymenu.Item
	logger  *log.Logger
}

var _ Plugin = (*PairingPlugin)(nil)

// NewPairingPlugin builds the pairing server for a, listening on port and
// handing out ca to a device that pairs, and the plugin that runs it. Pass
// Runtime.Certificates, or nil for a build with no authority to give. See
// [PairingFor].
//
// A zero port is a build that pairs no devices: it returns nil, and every
// method tolerates one.
func NewPairingPlugin(a *Agent, port int, ca tlspkg.CertificateAuthority) *PairingPlugin {
	if port <= 0 {
		return nil
	}
	return &PairingPlugin{Server: PairingFor(a, port, ca)}
}

// Name identifies the plugin.
func (p *PairingPlugin) Name() string { return "pairing" }

// Port reports the port the pairing server listens on, 0 when there is none.
func (p *PairingPlugin) Port() int {
	if p == nil {
		return 0
	}
	return p.Server.Port()
}

// PIN is the code a phone must present to pair, empty when there is no pairing
// server.
func (p *PairingPlugin) PIN() string {
	if p == nil {
		return ""
	}
	return p.Server.PIN()
}

// RotatePIN issues a fresh PIN, invalidating the pairing URLs carrying the old
// one, and relabels the menu entries that show it. Safe to call from anywhere
// that offers the action, the control center included.
func (p *PairingPlugin) RotatePIN() string {
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
func (p *PairingPlugin) URL() string {
	port := p.Port()
	if port == 0 {
		return ""
	}

	return "http://" + serviceAddress(serviceHost(), port) + "/?pin=" + url.QueryEscape(p.PIN())
}

// Activate registers the pairing server and adds the plugin's menu entries.
func (p *PairingPlugin) Activate(ctx AgentContext) error {
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
		traymenu.OnClick(func() { copyValue(p.logger, "phone-pairing URL", p.URL()) }),
	)
	p.pin = section.Set("pin", "Pairing PIN: --", traymenu.Disabled())
	section.Set("copy-pin", "Copy Pairing PIN",
		traymenu.Tooltip("Copy the 6-digit pairing PIN to the clipboard"),
		traymenu.OnClick(func() { copyValue(p.logger, "pairing PIN", p.PIN()) }),
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
	ctx.Events.State.Connect(func(State) { p.refresh() })
	ctx.Events.Servers.Connect(func(int) { p.refresh() })
	return nil
}

// refresh brings the labels back in step with the server. Safe from any
// goroutine, as the hooks calling it need.
func (p *PairingPlugin) refresh() {
	if p.address != nil {
		p.address.SetTitle("Pair Phone: " + p.URL())
	}
	if p.pin != nil {
		p.pin.SetTitle("Pairing PIN: " + p.PIN())
	}
}

func (p *PairingPlugin) logf(format string, args ...any) {
	if p.logger != nil {
		p.logger.Printf(format, args...)
	}
}

func (p *PairingPlugin) menuTitle() string {
	if p.MenuTitle != "" {
		return p.MenuTitle
	}
	return "Pairing"
}
