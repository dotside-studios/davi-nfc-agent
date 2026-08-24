//go:build !nowebui

package tray

import "github.com/dotside-studios/davi-nfc-agent/agent/console"

// AttachConsole gives the tray the console it acts through, and gives the
// console the tray, so an action taken in either runs through the same code.
//
// The entry that opens the console is the console plugin's, not the tray's.
// This is the half a plugin cannot do: the console needs the tray itself, and
// an activating plugin is handed a menu rather than whatever draws it.
func (s *App) AttachConsole(c *console.Server) {
	s.console = c
	c.AttachTray(s)
}
