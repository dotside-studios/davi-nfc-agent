package tray

import (
	"log"

	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// setupTrustMenu adds the entry that makes browsers accept this agent.
//
// It sits beside the origin allowlist because the two are the pair of things a
// browser needs: the allowlist decides who may connect, this decides whether the
// connection can be opened at all.
func (s *App) setupTrustMenu() {
	s.mTrustBrowsers = s.menu.Add(
		"Trust This Agent in Browsers",
		traymenu.Tooltip("Install a local certificate authority so web pages on this machine can reach the reader"),
		traymenu.OnClick(s.handleTrustBrowsers),
	)
	s.RefreshTrustMenu()
}

// RefreshTrustMenu hides the entry once there is nothing left for it to do.
func (s *App) RefreshTrustMenu() {
	if s.mTrustBrowsers == nil {
		return
	}
	s.mTrustBrowsers.SetVisible(s.agent.TLSManager() != nil && !s.agent.TLSManager().CAInstalled())
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
		// Installing reissues the certificate, and whatever serves it binds
		// again on its own: see tls.CertificateWatcher.
		log.Printf("[systray] Browsers on this machine now trust this agent; reload any page that uses the reader")
		s.RefreshTrustMenu()
		if s.console != nil {
			s.console.NotifyChange()
		}
	}()
}
