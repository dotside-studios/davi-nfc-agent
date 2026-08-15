//go:build !nowebui

package main

import (
	"net/http"

	"github.com/dotside-studios/davi-nfc-agent/buildinfo"
	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/settings"
	"github.com/dotside-studios/davi-nfc-agent/webui"
)

// Console is the agent's view of the control center. A build with -tags nowebui
// gets the stub in webui_disabled.go, and every caller tolerates a nil one.
type Console = webui.Server

func setupConsole(agent *Agent, store *settings.Store, logs *logbuf.Ring) *Console {
	host := &webuiHost{agent: agent, settings: store}
	console := webui.New(webui.Config{
		Host:    host,
		Logs:    logs,
		Name:    buildinfo.Name,
		Version: buildinfo.FullVersion(),
		Dev:     buildinfo.IsDev(),
	})

	agent.Console = console
	agent.consoleHost = host
	return console
}

// consoleRoutes and consoleAssets are what the unified server mounts.
func consoleRoutes(console *Console) http.Handler {
	if console == nil {
		return nil
	}
	return console.Handler()
}

func consoleAssets() http.Handler { return webui.Console() }
