//go:build nowebui

package console

import (
	"errors"
	"net/http"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/logbuf"
)

// Stubs for -tags nowebui, which omits the control center: no /control routes,
// no privileged API, no tray entry, and no frontend in the binary. New returns
// nil and every method tolerates a nil receiver, so nothing else needs a build
// tag of its own.

// Server is the absent control center.
type Server struct{}

// Config is what a console would report on; this build has none.
type Config struct {
	Agent   *agent.Agent
	Logs    *logbuf.Ring
	Servers *agent.ServerPlugin
	Pairing *agent.PairingPlugin
	Trust   *agent.TrustPlugin
	Quit    func()
}

// New reports that there is no console in this build.
func New(Config) *Server { return nil }

func (s *Server) Routes() http.Handler { return nil }

// Endpoints reports that this build serves no console.
func (s *Server) Endpoints() []agent.Endpoint { return nil }
func (s *Server) Assets() http.Handler        { return nil }

// NotifyChange is what the agent's changes call to wake an open page.
func (s *Server) NotifyChange() {}

// ConsoleURL exists so the tray compiles; this build has no console to open.
func (s *Server) ConsoleURL() (string, error) {
	return "", errors.New("console: built with -tags nowebui")
}
