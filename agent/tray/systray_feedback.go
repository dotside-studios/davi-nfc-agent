package tray

import (
	"log"

	"fyne.io/systray"

	"github.com/dotside-studios/davi-nfc-agent/settings"
)

// setupFeedbackMenu adds the reader feedback toggle, beside the mode menu.
func (s *App) setupFeedbackMenu() {
	s.mReaderFeedback = systray.AddMenuItemCheckbox(
		"Flash and Beep on Scan",
		"Flash the reader's LED and sound its buzzer when a tag is read or written",
		s.agent.ReaderFeedback(),
	)
}

// handleReaderFeedback toggles the reader's LED and buzzer feedback, live and
// on disk. A failed save leaves the toggle in effect for this session rather
// than undoing what the menu already shows.
func (s *App) handleReaderFeedback() {
	on := !s.mReaderFeedback.Checked()

	s.agent.SetReaderFeedback(on)
	if on {
		s.mReaderFeedback.Check()
	} else {
		s.mReaderFeedback.Uncheck()
	}

	if s.settings == nil {
		return
	}
	if _, err := s.settings.Update(func(next *settings.Settings) { next.ReaderFeedback = on }); err != nil {
		log.Printf("[systray] Reader feedback is on for this session only: it could not be saved: %v", err)
	}
}
