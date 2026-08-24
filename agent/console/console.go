//go:build !nowebui

package console

import (
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"runtime"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/clipboard"
	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/settings"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
	"github.com/dotside-studios/davi-nfc-agent/webui"
)

// Server is the control center bound to one agent. It embeds the webui server,
// so NotifyChange and ConsoleURL come through unchanged.
type Server struct {
	*webui.Server
	host *host
}

// New builds the console for a, and follows what changes it: an origin allowed,
// a device revoked or a client connecting all redraw an open page. The caller
// serves it with Endpoints; a build that wants no control center serves none.
//
// pairing is the pairing plugin this build runs, or nil for a build that pairs
// no devices. The agent does not hold one, so whoever built it hands it to
// whatever displays the PIN.
func New(a *agent.Agent, store *settings.Store, logs *logbuf.Ring, pairing *agent.PairingPlugin) *Server {
	h := &host{agent: a, settings: store, pairing: pairing}
	info := a.Info()
	s := &Server{
		Server: webui.New(webui.Config{
			Host:    h,
			Logs:    logs,
			Name:    info.Name,
			Version: info.FullVersion(),
			Dev:     info.IsDev(),
		}),
		host: h,
	}

	// Both stores are optional configuration, so an agent built without them
	// still gets a console; it just has less to follow.
	if origins := a.Origins(); origins != nil {
		origins.OnChange(s.NotifyChange)
	}
	if devices := a.Devices(); devices != nil {
		devices.OnChange(s.NotifyChange)
	}
	a.OnClientsChange(s.NotifyChange)

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
func (s *Server) Endpoints() []agent.Endpoint {
	if s == nil {
		return nil
	}

	return []agent.Endpoint{
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
							log.Printf("Failed to copy the control center URL: %v", err)
							return
						}
						log.Printf("Copied the control center URL to the clipboard")
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
	return webui.Console()
}

// AttachTray gives the console a tray to act through, so a change made in the
// console moves the tray's menu state too. Without one the console drives the
// agent directly, which is what a headless run wants.
func (s *Server) AttachTray(t Tray) {
	if s == nil || s.host == nil {
		return
	}
	s.host.app = t
}
