package tray

import (
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// setupRawAPDUMenu adds the raw APDU channel toggle, beside the mode menu. Off
// by default: a raw exchange reaches the tag unmodified and can lock or brick
// it, so the channel that carries one stays closed until an operator opens it.
func (s *App) setupRawAPDUMenu() {
	s.mRawAPDU = s.menu.AddCheckbox(
		"Allow Raw APDU Channel",
		s.agent.RawAPDUAllowed(),
		traymenu.Tooltip("Let clients send raw exchanges that reach the tag unmodified. A raw command can lock or brick a tag in ways the agent cannot undo"),
		traymenu.OnClick(s.handleRawAPDU),
	)
}

// handleRawAPDU opens or closes the raw APDU channel, live: whatever gates a raw
// exchange reads it per request, so connections already open follow it.
func (s *App) handleRawAPDU() {
	s.agent.SetRawAPDUEnabled(!s.mRawAPDU.Checked())
	s.SyncPreferencesToMenu(s.agent.Preferences())
}
