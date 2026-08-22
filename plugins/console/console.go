//go:build !nowebui

package console

import (
	"net/http"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/buildinfo"
	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/plugin"
	"github.com/dotside-studios/davi-nfc-agent/settings"
	"github.com/dotside-studios/davi-nfc-agent/webui"
)

// Console is the control center itself. A build with -tags nowebui has none,
// and New returns nil there.
type Console = webui.Server

// New returns the plugin that serves the control center and puts it on the
// tray, or nil in a build without one.
//
// logs is the ring the console tails, and store is where a preference set in it
// is written. Everything else it needs, it asks the agent.
func New(a *agent.Agent, store *settings.Store, logs *logbuf.Ring) plugin.Plugin {
	host := &webuiHost{agent: a, settings: store}

	return &consolePlugin{console: webui.New(webui.Config{
		Host:    host,
		Logs:    logs,
		Name:    buildinfo.Name,
		Version: buildinfo.FullVersion(),
		Dev:     buildinfo.IsDev(),
	})}
}

// routes and assets are what the listener mounts.
func (c *consolePlugin) routes() http.Handler {
	if c.console == nil {
		return nil
	}
	return c.console.Handler()
}

func (c *consolePlugin) assets() http.Handler { return webui.Console() }
