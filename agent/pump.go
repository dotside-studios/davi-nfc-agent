package agent

import (
	"context"
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

// pumpReader forwards what the hardware reader scans to the sink. It returns
// once ctx is done.
func (a *Agent) pumpReader(ctx context.Context, reader *nfc.NFCReader, sink tagSink) {
	for {
		select {
		case <-ctx.Done():
			return
		case data := <-reader.Data():
			a.forwardScan(data, sink)
		case status := <-reader.StatusUpdates():
			sink.BroadcastDeviceStatus(status)
		}
	}
}

// pumpDevices forwards what paired devices scan. Their scans carry no reader
// status and are not filtered by card type: the filter is the agent's policy
// for its own reader, and a device reports what its user tapped.
func pumpDevices(ctx context.Context, src <-chan nfc.NFCData, sink tagSink) {
	for {
		select {
		case <-ctx.Done():
			return
		case data, ok := <-src:
			if !ok {
				return
			}
			sink.Broadcast(data)
		}
	}
}

// forwardScan applies the card-type filter and hands the scan on.
func (a *Agent) forwardScan(data nfc.NFCData, sink tagSink) {
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
			Err: fmt.Errorf("card type '%s' not allowed by filter", data.Card.Type),
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
