package agent

import (
	"fmt"
	"log"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// readerSelected reports whether a scan's source is wanted. The pinned device
// is a filter rather than a lock: a scan from a reader the operator is not
// asking for is dropped here, wherever it was read.
//
// Only readers are filtered. A device that reports its own scans, such as a
// phone, is not one the agent chose to read from, so pinning a reader says
// nothing about it.
func (a *Agent) readerSelected(device string) bool {
	pinned := a.CurrentPinnedDevice()
	if pinned == "" || device == "" || device == pinned {
		return true
	}

	readers := a.supervisor.Load()
	return readers == nil || !readers.Operates(device)
}

// forwardScan applies the agent's filters and reports what passes them. What
// serves clients subscribes to that, so every consumer sees the same stream.
func (a *Agent) forwardScan(data nfc.NFCData) {
	if !a.readerSelected(data.Device) {
		return
	}

	if data.Err != nil {
		log.Printf("Error: %v", data.Err)
		a.reportTag(data)
		return
	}

	if data.Card == nil {
		return
	}

	if !a.cardTypes.isAllowed(data.Card.Type) {
		log.Printf("Card type '%s' not in allowed list, ignoring", data.Card.Type)
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
