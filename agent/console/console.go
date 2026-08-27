//go:build !nowebui

package console

import (
	"fmt"
	"net/http"
	"os/exec"
	"runtime"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/agent/pairingplugin"
	"github.com/dotside-studios/davi-nfc-agent/agent/serverplugin"
	"github.com/dotside-studios/davi-nfc-agent/agent/trustplugin"
	"github.com/dotside-studios/davi-nfc-agent/clipboard"
	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// Config is what the console reports on. The agent is required; the rest are
// what this build happens to run, and a nil one is reported as absent rather
// than being an error.
type Config struct {
	// Agent is the agent the console administers.
	Agent *agent.Agent

	Logs *logbuf.Ring

	// Servers is what the agent is served from, for the port and address the
	// console hands out. Pairing is what issues pairing PINs, and Trust what
	// installs the local authority and reports on it. The agent holds none of
	// them, so whoever built them passes them here.
	Servers *serverplugin.Plugin
	Pairing *pairingplugin.Plugin
	Trust   *trustplugin.Plugin

	// Quit ends the program the agent runs in, for the console's own quit
	// control. Nil stops the agent and leaves the program running, which is
	// what a service with no way out of its own wants.
	Quit func()
}

// New builds the console from cfg, and follows what changes it: an origin
// allowed, a device revoked or a client connecting all redraw an open page. The
// caller serves it with Endpoints; a build that wants no control center serves
// none.
func New(cfg Config) *Server {
	a := cfg.Agent
	h := &host{
		agent:   a,
		servers: cfg.Servers,
		pairing: cfg.Pairing,
		trust:   cfg.Trust,
		quit:    cfg.Quit,
	}
	info := a.Info()
	s := newServer(serverConfig{
		Host:    h,
		Logs:    cfg.Logs,
		Name:    info.Name,
		Version: info.FullVersion(),
		Dev:     info.IsDev(),
	})

	// Every change the agent reports, whatever made it: an open page shows
	// what the agent is set to rather than what it was set to when the page
	// loaded, including changes made from the tray.
	a.Events().Any.Connect(func(agent.Change) { s.NotifyChange() })

	// The allowlist and the connected clients are the server's rather than the
	// agent's, so they are followed separately: an origin allowed from the
	// tray, a page refused, or a client connecting reaches an open page the
	// same way.
	if cfg.Servers != nil {
		cfg.Servers.Events().Origins.Connect(func(serverplugin.OriginState) { s.NotifyChange() })
		cfg.Servers.Events().Clients.Connect(func(int) { s.NotifyChange() })
	}

	return s
}

// Endpoints are what a server plugin mounts to serve the console: the
// privileged control API, and the page itself.
//
//	servers.Add(c.Endpoints()...)
//
// Neither is wrapped in CORS: one administers the agent and the other is a
// page, so no other origin has business calling them. The page carries the tray
// entries that show its address and open it; the API carries none, being a
// route nobody opens by hand.
func (s *Server) Endpoints() []serverplugin.Endpoint {
	if s == nil {
		return nil
	}

	return []serverplugin.Endpoint{
		{Name: "control API", Pattern: "/control/", Handler: s.Routes()},
		{
			Name:    "control center",
			Pattern: "/",
			Handler: s.Assets(),
			Menu: func(menu traymenu.Container, url string) {
				menu.Add("Control Center: "+url,
					traymenu.Tooltip("Manage this agent in a browser"),
					traymenu.Disabled(),
				)
				menu.Add("  Copy Control Center URL",
					traymenu.OnClick(func() {
						if err := clipboard.Copy(url); err != nil {
							consoleFail.Printf("Failed to copy the control center URL: %v", err)
							return
						}
						consoleLog.Printf("Copied the control center URL to the clipboard")
					}),
				)
				menu.Add("  Open Control Center",
					traymenu.Tooltip("Open it with a single-use token"),
					traymenu.OnClick(s.open),
				)
			},
		},
	}
}

// open mints a single-use token and opens the console.
func (s *Server) open() {
	url, err := s.ConsoleURL()
	if err != nil {
		consoleFail.Printf("Failed to prepare control center URL: %v", err)
		return
	}

	if err := openBrowser(url); err != nil {
		// Falling back to the clipboard keeps this usable on a machine with no
		// registered browser handler, which is common on minimal Linux desktops.
		consoleWarn.Printf("Failed to open a browser: %v", err)
		if copyErr := clipboard.Copy(url); copyErr != nil {
			consoleLog.Printf("Control center URL (expires shortly): %s", url)
			return
		}
		consoleLog.Printf("Control center URL copied to clipboard; it expires shortly")
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

// Routes serves the privileged control API.
func (s *Server) Routes() http.Handler {
	if s == nil {
		return nil
	}
	return s.Handler()
}

// Assets serves the embedded console frontend.
func (s *Server) Assets() http.Handler {
	if s == nil {
		return nil
	}
	return frontendHandler()
}
