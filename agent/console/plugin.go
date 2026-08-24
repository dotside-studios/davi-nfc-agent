//go:build !nowebui

package console

import (
	"fmt"
	"log"
	"os/exec"
	"runtime"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/clipboard"
	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/settings"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// Plugin serves the control center from the agent's listener and puts the entry
// that opens it on the tray.
//
// Registering it is what a build does to have one:
//
//	rt.Agent.Plugins.Add(servers, console.NewPlugin(rt.Agent, rt.Settings, rt.Logs, pairing))
//
// It mounts on the listener some earlier plugin published, so it goes on after
// the one that serves it. A build with -tags nowebui gets a plugin that
// registers nothing, since there is no console compiled in to serve.
type Plugin struct {
	server *Server
}

var _ agent.Plugin = (*Plugin)(nil)

// NewPlugin builds the console and the plugin that serves it. See [New] for the
// arguments, pairing included.
//
// The console is built here rather than at activation because the tray needs it
// before then, to act through: see [Server.AttachTray].
func NewPlugin(a *agent.Agent, store *settings.Store, logs *logbuf.Ring, pairing *agent.PairingPlugin) *Plugin {
	return &Plugin{server: New(a, store, logs, pairing)}
}

// Name identifies the plugin.
func (p *Plugin) Name() string { return "control center" }

// Server is the console it serves, for a tray to open and act through.
func (p *Plugin) Server() *Server {
	if p == nil {
		return nil
	}
	return p.server
}

// Activate mounts the console's routes and follows what changes it.
func (p *Plugin) Activate(ctx agent.AgentContext) error {
	if p == nil || p.server == nil {
		return nil
	}

	// Neither is wrapped in CORS: one administers the agent and the other is a
	// page, so no other origin has business calling them.
	if err := ctx.Mount("/control/", p.server.Routes()); err != nil {
		return err
	}
	if err := ctx.Mount("/", p.server.Assets()); err != nil {
		return err
	}

	// Redraw whenever something changes the agent from elsewhere. Both stores
	// are optional configuration, so an agent built without them still gets a
	// console; it just has less to follow.
	if origins := ctx.Agent.Origins(); origins != nil {
		origins.OnChange(p.server.NotifyChange)
	}
	if devices := ctx.Agent.Devices(); devices != nil {
		devices.OnChange(p.server.NotifyChange)
	}
	ctx.Agent.OnClientsChange(p.server.NotifyChange)

	ctx.Systray.Add("Open Control Center",
		traymenu.Tooltip("Manage this agent in a browser"),
		traymenu.OnClick(p.open),
	)
	return nil
}

// open mints a single-use token and opens the console.
func (p *Plugin) open() {
	url, err := p.server.ConsoleURL()
	if err != nil {
		log.Printf("Failed to prepare control center URL: %v", err)
		return
	}

	if err := openBrowser(url); err != nil {
		// Falling back to the clipboard keeps this usable on a machine with no
		// registered browser handler, which is common on minimal Linux desktops.
		log.Printf("Failed to open a browser: %v", err)
		if copyErr := clipboard.Copy(url); copyErr != nil {
			log.Printf("Control center URL (expires shortly): %s", url)
			return
		}
		log.Printf("Control center URL copied to clipboard; it expires shortly")
	}
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
	return cmd.Start()
}
