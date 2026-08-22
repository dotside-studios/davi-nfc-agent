package main

import (
	"errors"
	"net/url"

	"github.com/dotside-studios/davi-nfc-agent/surface"
	"github.com/dotside-studios/davi-nfc-agent/tls"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// pairingEndpoint is the ID the phone-pairing page is published under.
const pairingEndpoint = "pairing"

// pairingPlugin puts the phone-pairing server on the tray.
//
// It is the agent's own feature and could have been drawn by the tray, as it
// used to be. It goes through the plugin surface instead because it is the
// shape every other feature has: something that runs beside the agent, has an
// address to hand out and a control or two to offer, and has to keep both in
// step with itself. The tray no longer knows what pairing is, and a build with
// no pairing server registers nothing rather than hiding entries.
type pairingPlugin struct {
	server *tls.BootstrapServer
	port   int

	host surface.Host
	pin  *traymenu.Item
}

// newPairingPlugin returns the plugin for a running pairing server. port is the
// one it listens on, which is the port the page is served from.
func newPairingPlugin(server *tls.BootstrapServer, port int) *pairingPlugin {
	return &pairingPlugin{server: server, port: port}
}

func (p *pairingPlugin) Describe() surface.Info {
	return surface.Info{
		ID:      pairingEndpoint,
		Title:   "Pair a Phone",
		Tooltip: "Hand a phone the page and the PIN it needs to pair with this agent",
	}
}

func (p *pairingPlugin) Attach(host surface.Host) error {
	if p.server == nil || p.port <= 0 {
		return errors.New("the pairing server is not running")
	}
	p.host = host

	menu := host.Menu()

	p.pin = menu.Add("Pairing PIN: --",
		traymenu.Tooltip("The PIN a phone is asked for"),
		traymenu.Disabled(),
	)
	menu.Add("Open Pairing Page",
		traymenu.Tooltip("Open the pairing page in a browser on this machine"),
		traymenu.OnClick(p.open),
	)
	menu.Add("Copy Pairing URL",
		traymenu.Tooltip("Copy the pairing page's address, PIN and all"),
		traymenu.OnClick(func() { host.Copy("phone-pairing URL", p.url()) }),
	)
	menu.Add("Copy Pairing PIN",
		traymenu.Tooltip("Copy the 6-digit pairing PIN"),
		traymenu.OnClick(func() { host.Copy("pairing PIN", p.server.PIN()) }),
	)
	menu.AddSeparator()
	menu.Add("Regenerate Pairing PIN",
		traymenu.Tooltip("Generate a fresh PIN; the URLs carrying the old one stop working"),
		traymenu.OnClick(p.rotate),
	)

	p.publish()
	return nil
}

// url is the pairing page, carrying the PIN so a link pasted into a chat goes
// straight through. Always HTTP: a phone that has not installed the agent's
// certificate authority yet is exactly who this page is for.
func (p *pairingPlugin) url() string {
	return "http://" + hostPort(localHost(), p.port) + "/?pin=" + url.QueryEscape(p.server.PIN())
}

// publish puts the current address and PIN where they are read from: the
// address in the agent's endpoint register, which the tray draws, and the PIN
// on the plugin's own label.
func (p *pairingPlugin) publish() {
	p.host.Endpoints().Set(surface.Endpoint{
		ID:      pairingEndpoint,
		Label:   "Pair Phone",
		URL:     p.url(),
		Tooltip: "The page a phone opens to pair with this agent. Click to copy",
	})
	p.pin.SetTitle("Pairing PIN: " + p.server.PIN())
}

// rotate issues a fresh PIN, which invalidates every URL carrying the old one,
// and republishes both so nothing on the menu is still offering it.
func (p *pairingPlugin) rotate() {
	fresh := p.server.RotatePIN()
	p.publish()
	p.host.Logf("Pairing PIN rotated to %s; the URLs carrying the old one no longer work", fresh)
}

// open shows the pairing page here, for pairing a phone by scanning the QR code
// on the operator's own screen.
func (p *pairingPlugin) open() {
	target := p.url()
	if err := p.host.Open(target); err != nil {
		p.host.Logf("Could not open a browser (%v); copying the address instead", err)
		p.host.Copy("phone-pairing URL", target)
	}
}
