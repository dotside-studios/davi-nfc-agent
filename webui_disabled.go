//go:build nowebui

package main

import (
	"net/http"

	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/settings"
)

// Stubs for -tags nowebui, which omits the control center: no /control routes,
// no privileged API, no tray entry, and no frontend in the binary. Callers hold
// a nil *Console and every method tolerates that, so no shared file needs a
// build tag of its own.

type Console struct{}

func setupConsole(*Agent, *settings.Store, *logbuf.Ring) *Console { return nil }

// NotifyChange is what the origin and device stores call on every change.
func (c *Console) NotifyChange() {}

func consoleRoutes(*Console) http.Handler { return nil }
func consoleAssets() http.Handler         { return nil }

func (s *SystrayApp) setupConsoleMenu()      {}
func (s *SystrayApp) handleOpenConsole()     {}
func (s *SystrayApp) AttachConsole(*Console) {}
