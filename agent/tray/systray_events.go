package tray

import (
	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// subscribe follows the agent, which is what keeps the menu in step with a
// change made anywhere else: the console, another plugin, or the reader itself.
//
// The state-carrying signals replay on connect, so this is also what fills the
// menu in: it runs after setupUI has declared the entries, and each handler
// draws what the agent is set to, which is not always the default.
func (s *App) subscribe() {
	events := s.agent.Events()

	// Without the replay: the menu says "Starting..." until the agent settles,
	// and the agent is stopped until autoStartAgent has run.
	events.State.Signal.Connect(s.showState)

	events.Preferences.Connect(s.SyncPreferencesToMenu)
	events.Readers.Connect(s.applyReaders)
	events.Tag.Connect(s.showCard)
	events.Reader.Connect(s.showReaderStatus)
}

// showState follows the agent's lifecycle, including a stop the tray did not
// ask for. A start that failed is reported by whoever called Start, which knows
// it was a start rather than a stop.
func (s *App) showState(state agent.State) {
	switch state {
	case agent.StateRunning:
		s.showRunning()
		// The pinned device may be a different one: it is a preference like any
		// other, and the console changes it without telling the tray.
		s.markCurrentReader()
	case agent.StateStopped:
		s.showStopped("Stopped")
		s.showNoCard()
	}
}

// showCard names the card that was just scanned.
func (s *App) showCard(data nfc.NFCData) {
	if data.Card == nil {
		return
	}
	s.updateCardUID(data.Card.UID)
	s.updateCardType(data.Card.Type)
}

// showReaderStatus clears the card lines once the card is off the reader.
func (s *App) showReaderStatus(status nfc.DeviceStatus) {
	if !status.CardPresent {
		s.showNoCard()
	}
}

func (s *App) showNoCard() {
	s.updateCardUID("")
	s.updateCardType("")
}
