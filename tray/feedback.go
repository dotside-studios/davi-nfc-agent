package tray

import (
	"github.com/dotside-studios/davi-nfc-agent/settings"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// AttachSettings gives the tray the settings store, so a preference toggled
// from a menu outlives the session that toggled it. Without one the toggles
// still work, they are just forgotten at exit.
func (s *App) AttachSettings(store *settings.Store) {
	s.settings = store
}

// setupFeedbackMenu adds the reader feedback toggle, beside the mode menu.
func (s *App) setupFeedbackMenu() {
	s.mReaderFeedback = s.menu.AddCheckbox(
		"Flash and Beep on Scan",
		s.agent.ReaderFeedbackEnabled(),
		traymenu.Tooltip("Flash the reader's LED and sound its buzzer when a tag is read or written"),
		traymenu.OnClick(s.handleReaderFeedback),
	)
}

// handleReaderFeedback toggles the reader's LED and buzzer feedback, live and
// on disk.
func (s *App) handleReaderFeedback() {
	s.agent.SetReaderFeedback(!s.mReaderFeedback.Checked())
	s.persist()
	s.SyncSettings(s.agent.Settings())
}
