//go:build !nowebui

package main

import (
	"log"

	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// AttachConsole gives the tray the console it opens, and gives the console's
// host the tray, so an action taken in either runs through the same code.
func (s *SystrayApp) AttachConsole(console *Console) {
	s.console = console
	if host, ok := s.agent.consoleHost.(*webuiHost); ok && host != nil {
		host.app = s
	}
}

func (s *SystrayApp) setupConsoleMenu() {
	s.menu.Add("Open Control Center",
		traymenu.Tooltip("Manage this agent in a browser"),
		traymenu.HiddenIf(s.console == nil),
		traymenu.OnClick(s.handleOpenConsole),
	)
}

// handleOpenConsole mints a single-use token and opens the console.
func (s *SystrayApp) handleOpenConsole() {
	if s.console == nil {
		return
	}

	url, err := s.console.ConsoleURL()
	if err != nil {
		log.Printf("Failed to prepare control center URL: %v", err)
		return
	}

	if err := openBrowser(url); err != nil {
		// Falling back to the clipboard keeps this usable on a machine with no
		// registered browser handler, which is common on minimal Linux desktops.
		log.Printf("Failed to open a browser: %v", err)
		if copyErr := copyToClipboard(url); copyErr != nil {
			log.Printf("Control center URL (expires shortly): %s", url)
			return
		}
		log.Printf("Control center URL copied to clipboard; it expires shortly")
	}
}
