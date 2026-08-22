// Package pairing is the phone-pairing server as a plugin: the page a phone
// opens, the PIN it is asked for, and the tray menu an operator works both
// from.
//
// It runs a listener of its own rather than mounting on the agent's, and
// deliberately over plain HTTP: a phone that has not installed this agent's
// certificate authority yet is exactly who the page is for, so it cannot be
// behind the certificate the phone does not have.
package pairing

import (
	"errors"
	"net/url"
	"sync"

	"github.com/dotside-studios/davi-nfc-agent/netaddr"
	"github.com/dotside-studios/davi-nfc-agent/plugin"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// ID is what this plugin is registered under.
const ID = "pairing"

// Server is what this plugin needs from the pairing server it runs: the PIN it
// is asking for, a fresh one on demand, and its own start and stop.
//
// tls.BootstrapServer satisfies it. Stating it here keeps the plugin testable
// without a listener, and keeps the package from depending on how the agent
// hands out its certificate authority.
type Server interface {
	PIN() string
	RotatePIN() string
	Start() error
	Stop()
}

// Config assembles the plugin.
type Config struct {
	// Server is the pairing server this plugin runs. Required.
	Server Server

	// Port is the port it listens on, which is the port its page is served
	// from. Required.
	Port int
}

// Plugin is the pairing server and its menu.
type Plugin struct {
	config Config

	ctx  *plugin.Context
	page *traymenu.Item
	pin  *traymenu.Item

	// mu guards whether the listener is up. It is set as the plugin starts and
	// stops, and read when the menu redraws — which happens on the tray's own
	// goroutine, from a click that can land mid-stop.
	mu      sync.Mutex
	serving bool
}

// New returns the plugin, with nothing listening yet.
func New(config Config) *Plugin { return &Plugin{config: config} }

func (p *Plugin) Describe() plugin.Info {
	return plugin.Info{
		ID:      ID,
		Title:   "Pair a Phone",
		Tooltip: "Hand a phone the page and the PIN it needs to pair with this agent",
	}
}

// Init builds the menu. The PIN is what an operator reads off it, and every
// entry here is a way of getting that PIN, or the page carrying it, to a phone.
func (p *Plugin) Init(ctx *plugin.Context) error {
	if p.config.Server == nil {
		return errors.New("there is no pairing server to run")
	}
	if p.config.Port <= 0 {
		return errors.New("pairing is disabled: there is no port to serve the page on")
	}
	p.ctx = ctx

	menu := ctx.Menu()

	p.page = menu.Add("Page: Not running",
		traymenu.Tooltip("Where a phone goes to pair. Click to copy"),
		traymenu.OnClick(func() { ctx.Copy("phone-pairing URL", p.URL()) }),
	)
	p.pin = menu.Add("Pairing PIN: --",
		traymenu.Tooltip("The PIN a phone is asked for"),
		traymenu.Disabled(),
	)
	menu.Add("Open Pairing Page",
		traymenu.Tooltip("Open the pairing page here, to pair a phone from the code on this screen"),
		traymenu.OnClick(p.open),
	)
	menu.Add("Copy Pairing PIN",
		traymenu.Tooltip("Copy the 6-digit pairing PIN"),
		traymenu.OnClick(func() { ctx.Copy("pairing PIN", p.config.Server.PIN()) }),
	)
	menu.AddSeparator()
	menu.Add("Regenerate Pairing PIN",
		traymenu.Tooltip("Generate a fresh PIN; the URLs carrying the old one stop working"),
		traymenu.OnClick(p.Rotate),
	)

	p.show()
	return nil
}

// Start brings the pairing server up and publishes its page.
func (p *Plugin) Start(ctx *plugin.Context) error {
	if err := p.config.Server.Start(); err != nil {
		return err
	}
	p.setServing(true)

	p.show()
	ctx.Logf("pairing on port %d, PIN %s", p.config.Port, p.config.Server.PIN())
	return nil
}

// Stop takes it down. The menu stays, saying so: a phone cannot pair against a
// listener that is not there, and an entry that vanishes reads as a feature the
// build does not have.
func (p *Plugin) Stop(*plugin.Context) error {
	p.config.Server.Stop()
	p.setServing(false)
	return nil
}

// URL is the pairing page, carrying the PIN so a link pasted into a chat goes
// straight through.
func (p *Plugin) URL() string {
	return "http://" + netaddr.HostPort(netaddr.Host(), p.config.Port) +
		"/?pin=" + url.QueryEscape(p.config.Server.PIN())
}

// Rotate issues a fresh PIN, which invalidates every URL carrying the old one,
// and puts the new one everywhere the old one was showing.
func (p *Plugin) Rotate() {
	fresh := p.config.Server.RotatePIN()
	p.show()
	p.ctx.Logf("PIN rotated to %s; the URLs carrying the old one no longer work", fresh)
}

// open shows the pairing page here, for pairing a phone by scanning the code on
// the operator's own screen.
func (p *Plugin) open() {
	target := p.URL()
	if err := p.ctx.Open(target); err != nil {
		p.ctx.Logf("could not open a browser (%v); copying the address instead", err)
		p.ctx.Copy("phone-pairing URL", target)
	}
}

// setServing records whether the listener is up and redraws the menu for it.
func (p *Plugin) setServing(on bool) {
	p.mu.Lock()
	p.serving = on
	p.mu.Unlock()

	p.show()
}

func (p *Plugin) isServing() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.serving
}

// show redraws the menu from the pairing server as it stands: the page it is
// answering on, and the PIN it is asking for.
func (p *Plugin) show() {
	if p.pin == nil {
		return
	}

	p.pin.SetTitle("Pairing PIN: " + p.config.Server.PIN())
	if p.isServing() {
		p.page.SetTitle("Page: " + p.URL())
		p.page.Enable()
		return
	}
	p.page.SetTitle("Page: Not running")
	p.page.Disable()
}

// The phases this plugin takes part in.
var (
	_ plugin.Initer  = (*Plugin)(nil)
	_ plugin.Starter = (*Plugin)(nil)
	_ plugin.Stopper = (*Plugin)(nil)
)
