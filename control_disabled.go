//go:build nocontrol

package main

import (
	"net/http"

	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/tls"
)

// Stubs for a build with -tags nocontrol, which omits the control center and
// its embedded console entirely: no /control routes, no privileged API, and no
// webui/dist in the binary.
//
// The agent's own protocol is untouched. Everything the console was the only
// caller of — settings persistence, the log ring, raw tag exchanges — remains,
// because each is reachable another way.

// ControlServer is the disabled-build placeholder. Callers hold a nil pointer
// and every method tolerates that, so no call site needs a build tag of its own.
type ControlServer struct{}

func setupControlCenter(*Agent, *SettingsStore, *logbuf.Ring, *tls.BootstrapServer, int) *ControlServer {
	return nil
}

// Handler returns no routes, so the unified server mounts nothing under
// /control and the path 404s like any other unknown one.
func (c *ControlServer) Handler() http.Handler { return nil }

// NotifyChange is what the origin and device stores call on every change.
func (c *ControlServer) NotifyChange() {}

// webUIHandler serves nothing, leaving the root as the plain-text banner.
func webUIHandler() http.Handler { return nil }

// The tray entry that opens the console is omitted along with it.
func (s *SystrayApp) setupControlMenu()        {}
func (s *SystrayApp) handleOpenControlCenter() {}

// AttachControl has nothing to attach.
func (s *SystrayApp) AttachControl(*ControlServer, *SettingsStore) {}
