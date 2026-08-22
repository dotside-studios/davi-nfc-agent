package tray

import (
	"log"

	"github.com/dotside-studios/davi-nfc-agent/settings"
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
// on disk. A failed save leaves the toggle in effect for this session rather
// than undoing what the menu already shows.
func (s *App) handleReaderFeedback() {
	on := s.mReaderFeedback.Toggle()
	s.agent.SetReaderFeedback(on)

	if s.settings == nil {
		return
	}
	if _, err := s.settings.Update(func(next *settings.Settings) { next.ReaderFeedback = on }); err != nil {
		log.Printf("[systray] Reader feedback is on for this session only: it could not be saved: %v", err)
	}
}
