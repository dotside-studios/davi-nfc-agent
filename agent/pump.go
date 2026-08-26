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

	var text string
	if msg, err := data.Card.ReadMessage(); err == nil {
		if ndefMsg, ok := msg.(*nfc.NDEFMessage); ok {
			text, _ = ndefMsg.GetText()
			if text == "" {
				text, _ = ndefMsg.GetURI()
			}
		} else if textMsg, ok := msg.(*nfc.TextMessage); ok {
			text = textMsg.Text
		}
	}
	fmt.Printf("UID: %s\nDecoded text: %s\n", data.Card.UID, text)

	a.reportTag(data)
}
