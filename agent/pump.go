package agent

import (
	"fmt"

	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// forwardScan applies the agent's filters and reports what passes them. What
// serves clients subscribes to that, so every consumer sees the same stream.
func (a *Agent) forwardScan(data nfc.NFCData) {
	if !a.pinAdmits(data.Device) {
		return
	}

	if data.Err != nil {
		a.LoggerAt(logbuf.LevelError).Printf("A reader reported an error: %v", data.Err)
		a.reportTag(data)
		return
	}

	if data.Card == nil {
		return
	}

	if !a.cardTypes.isAllowed(data.Card.Type) {
		a.logger.Printf("Card type '%s' not in allowed list, ignoring", data.Card.Type)
		a.reportTag(nfc.NFCData{
			Device: data.Device,
			Err:    fmt.Errorf("card type '%s' not allowed by filter", data.Card.Type),
		})
		return
	}

	// Reading the message here also caches it on the card, so a client asking
	// for the same scan does not go back to the tag for it.
	if text := decodedText(data.Card); text != "" {
		a.logger.Printf("Scanned %s (%s): %s", data.Card.UID, data.Card.Type, text)
	} else {
		a.logger.Printf("Scanned %s (%s)", data.Card.UID, data.Card.Type)
	}

	a.reportTag(data)
}

// decodedText is what a card says, for the line the agent logs about it. Empty
// for a card carrying nothing readable, which is not an error: a tag can be
// blank, and one holding a record this build does not decode still scans.
func decodedText(card *nfc.Card) string {
	msg, err := card.ReadMessage()
	if err != nil {
		return ""
	}

	switch m := msg.(type) {
	case *nfc.NDEFMessage:
		if text, err := m.GetText(); err == nil && text != "" {
			return text
		}
		uri, _ := m.GetURI()
		return uri
	case *nfc.TextMessage:
		return m.Text
	}
	return ""
}
