//go:build nowebui

package console

import (
	"errors"
	"net/http"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/settings"
)

// Stubs for -tags nowebui, which omits the control center: no /control routes,
// no privileged API, no tray entry, and no frontend in the binary. New returns
// nil and every method tolerates a nil receiver, so nothing else needs a build
// tag of its own.

// Server is the absent control center.
type Server struct{}

// New reports that there is no console in this build.
func New(*agent.Agent, *settings.Store, *logbuf.Ring, *agent.PairingPlugin) *Server { return nil }

func (s *Server) Routes() http.Handler { return nil }
func (s *Server) Assets() http.Handler { return nil }

// NotifyChange is what the origin and device stores call on every change.
func (s *Server) NotifyChange() {}

// ConsoleURL exists so the tray compiles; this build has no console to open.
func (s *Server) ConsoleURL() (string, error) {
	return "", errors.New("console: built with -tags nowebui")
}

func (s *Server) AttachTray(Tray) {}

// Plugin is the absent control center's plugin: registering it costs nothing
// and serves nothing, so a program need not build-tag its own wiring.
type Plugin struct{}

// NewPlugin reports that there is no console in this build.
func NewPlugin(*agent.Agent, *settings.Store, *logbuf.Ring, *agent.PairingPlugin) *Plugin {
	return nil
}

func (p *Plugin) Name() string                      { return "control center" }
func (p *Plugin) Server() *Server                   { return nil }
func (p *Plugin) Activate(agent.AgentContext) error { return nil }
