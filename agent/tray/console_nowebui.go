//go:build nowebui

package tray

import "github.com/dotside-studios/davi-nfc-agent/agent/console"

// Tray stubs for -tags nowebui: no "Open Control Center" entry, and nothing to
// attach. The console package's own stub covers the rest.

func (s *App) setupConsoleMenu()             {}
func (s *App) handleOpenConsole()            {}
func (s *App) AttachConsole(*console.Server) {}
