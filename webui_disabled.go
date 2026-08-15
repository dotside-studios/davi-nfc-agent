//go:build nowebui

package main

import (
	"net/http"

	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/settings"
)

// Stubs for a build with -tags nowebui, which omits the control center and its
// console entirely: no /control routes, no privileged API, no tray entry, and
// no frontend in the binary.
//
// The agent's own protocol is untouched. Everything the console was the only
// caller of — settings persistence, the log ring, raw tag exchanges — remains,
// because each is reachable another way.

// Console is the disabled-build placeholder. Callers hold a nil pointer and
// every method tolerates that, so no call site needs a build tag of its own.
type Console struct{}

func setupConsole(*Agent, *settings.Store, *logbuf.Ring) *Console { return nil }

// NotifyChange is what the origin and device stores call on every change.
func (c *Console) NotifyChange() {}

// No routes, so the unified server mounts nothing under /control and the path
// 404s like any other unknown one.
func consoleRoutes(*Console) http.Handler { return nil }

// No assets, so the root stays the plain-text banner.
func consoleAssets() http.Handler { return nil }

// The tray entry that opens the console is omitted along with it.
func (s *SystrayApp) setupConsoleMenu()      {}
func (s *SystrayApp) handleOpenConsole()     {}
func (s *SystrayApp) AttachConsole(*Console) {}
