package tray

import (
	"log"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// subscribe follows the agent, which is what keeps the menu in step with a
// change made anywhere else: the console, another plugin, or the reader itself.
func (s *App) subscribe() {
	events := s.agent.Events()

	events.State.Connect(s.showState)
	events.Preferences.Connect(s.SyncPreferencesToMenu)
	events.Readers.Connect(s.applyReaders)
	events.Tag.Connect(s.showCard)
	events.Reader.Connect(s.showReaderStatus)
	events.Devices.Connect(func([]agent.PairedDevice) { s.refreshDevicesMenu() })
	events.Origins.Connect(func([]string) { s.refreshOriginsMenu() })
	events.Blocked.Connect(func(origin string) {
		log.Printf("[systray] Blocked connection from %s: allow it under Allowed Origins to let it use the reader", origin)
		s.refreshOriginsMenu()
	})
}

// showState follows the agent's lifecycle, including a stop the tray did not
// ask for. A start that failed is reported by whoever called Start, which knows
// it was a start rather than a stop.
func (s *App) showState(state agent.State) {
	switch state {
	case agent.StateRunning:
		s.showRunning()
		// The reader may be a different one: a device picked in the console
		// restarts the agent rather than telling the tray about it.
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
