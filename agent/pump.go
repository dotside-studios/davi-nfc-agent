package agent

import (
	"fmt"
	"log"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// tagSink receives scans. The client server is the one the agent has; the
// interface is here so the pumps can be tested without one.
type tagSink interface {
	Broadcast(nfc.NFCData)
	BroadcastDeviceStatus(nfc.DeviceStatus)
}

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

// forwardScan applies the agent's filters and hands the scan on.
func (a *Agent) forwardScan(data nfc.NFCData, sink tagSink) {
	if !a.readerSelected(data.Device) {
		return
	}

	if data.Err != nil {
		log.Printf("Error: %v", data.Err)
		sink.Broadcast(data)
		return
	}

	if data.Card == nil {
		return
	}

	if !a.cardTypes.isAllowed(data.Card.Type) {
		log.Printf("Card type '%s' not in allowed list, ignoring", data.Card.Type)
		sink.Broadcast(nfc.NFCData{
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

	sink.Broadcast(data)
}
