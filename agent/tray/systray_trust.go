package tray

import (
	"log"

	"fyne.io/systray"
)

// setupTrustMenu adds the entry that makes browsers accept this agent.
//
// It sits beside the origin allowlist because the two are the pair of things a
// browser needs: the allowlist decides who may connect, this decides whether the
// connection can be opened at all.
func (s *App) setupTrustMenu() {
	s.mTrustBrowsers = systray.AddMenuItem(
		"Trust This Agent in Browsers",
		"Install a local certificate authority so web pages on this machine can reach the reader",
	)
	s.RefreshTrustMenu()
}

// RefreshTrustMenu hides the entry once there is nothing left for it to do.
func (s *App) RefreshTrustMenu() {
	if s.mTrustBrowsers == nil {
		return
	}
	if s.agent.TLSManager() == nil || s.agent.TLSManager().CAInstalled() {
		s.mTrustBrowsers.Hide()
		return
	}
	s.mTrustBrowsers.Show()
}

// handleTrustBrowsers installs the local CA and restarts the listeners so the
// reissued certificate is the one served.
//
// The OS prompts for a password, which blocks, so this runs off the menu
// goroutine — a stalled handler would freeze every other menu item.
func (s *App) handleTrustBrowsers() {
	if s.agent.TLSManager() == nil {
		log.Printf("[systray] Cannot trust this agent: it is not managing its own certificates")
		return
	}

	go func() {
		log.Printf("[systray] Installing a certificate authority so browsers trust this agent; your operating system may ask for a password")

		if err := s.agent.TLSManager().InstallCA(); err != nil {
			log.Printf("[systray] Could not trust this agent in browsers: %v", err)
			return
		}
		if err := s.agent.RestartServers(); err != nil {
			log.Printf("[systray] Certificate authority installed, but the listeners did not restart: %v", err)
			return
		}

		log.Printf("[systray] Browsers on this machine now trust this agent; reload any page that uses the reader")
		s.RefreshTrustMenu()
		if s.console != nil {
			s.console.NotifyChange()
		}
	}()
}
