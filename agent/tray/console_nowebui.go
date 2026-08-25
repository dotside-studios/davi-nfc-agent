//go:build nowebui

package tray

import "github.com/dotside-studios/davi-nfc-agent/agent/console"

// Tray stub for -tags nowebui: there is no console to attach. The console
// package's own stub covers the rest.

func (s *App) AttachConsole(*console.Server) {}
