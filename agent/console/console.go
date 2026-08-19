//go:build !nowebui

package console

import (
	"net/http"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/settings"
	"github.com/dotside-studios/davi-nfc-agent/webui"
)

// Server is the control center bound to one agent. It embeds the webui server,
// so NotifyChange and ConsoleURL come through unchanged.
type Server struct {
	*webui.Server
	host *host
}

// New builds the console for a. The caller mounts its routes; a build that
// wants no control center mounts none.
func New(a *agent.Agent, store *settings.Store, logs *logbuf.Ring) *Server {
	h := &host{agent: a, settings: store}
	info := a.Info()
	return &Server{
		Server: webui.New(webui.Config{
			Host:    h,
			Logs:    logs,
			Name:    info.Name,
			Version: info.FullVersion(),
			Dev:     info.IsDev(),
		}),
		host: h,
	}
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
