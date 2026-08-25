package tray

import (
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// setupFeedbackMenu adds the reader feedback toggle, beside the mode menu.
func (s *App) setupFeedbackMenu() {
	s.mReaderFeedback = s.menu.AddCheckbox(
		"Flash and Beep on Scan",
		s.agent.ReaderFeedback(),
		traymenu.Tooltip("Flash the reader's LED and sound its buzzer when a tag is read or written"),
		traymenu.OnClick(s.handleReaderFeedback),
	)
}

// handleReaderFeedback toggles the reader's LED and buzzer feedback, live and
// on disk.
func (s *App) handleReaderFeedback() {
	s.agent.SetReaderFeedback(!s.mReaderFeedback.Checked())
	s.SyncPreferencesToMenu(s.agent.Preferences())
}
