package main

import (
	"fmt"
	"log"
	"sync"

	"github.com/dotside-studios/davi-nfc-agent/surface"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// trayHost is the tray's side of [surface.Host]: one per attached plugin, so
// that the menu and the log line are the plugin's own while the state, the
// addresses and the clipboard are the agent's.
//
// It is what keeps a plugin off the tray library. Everything a feature needs to
// appear on the menu arrives through this interface, and nothing here hands out
// the tray itself.
type trayHost struct {
	app  *SystrayApp
	info surface.Info

	mu   sync.Mutex
	item *traymenu.Item
}

// Menu is the plugin's own menu, taken on first use.
//
// A plugin that only publishes an address never asks for one, and never gets an
// empty menu on the tray reading as a feature that does nothing.
func (h *trayHost) Menu() traymenu.Container {
	h.mu.Lock()
	defer h.mu.Unlock()

	if h.item == nil {
		h.item = h.app.takePluginSlot(h.info)
	}
	return h.item
}

func (h *trayHost) Endpoints() *surface.Endpoints { return h.app.agent.Endpoints() }

func (h *trayHost) State() surface.State { return h.app.State() }

func (h *trayHost) Watch(fn func(surface.State)) *traymenu.Connection {
	return h.app.stateChanged.Connect(fn)
}

func (h *trayHost) Copy(what, value string) { h.app.copyValue(what, value) }

func (h *trayHost) Open(target string) error { return openBrowser(target) }

// Logf tags the line with the plugin's ID, so a line in the agent's log says
// which feature wrote it. The plugin's own format string is expanded first: it
// is data here, not a format for this call.
func (h *trayHost) Logf(format string, args ...any) {
	log.Printf("[plugin:%s] %s", h.info.ID, fmt.Sprintf(format, args...))
}
