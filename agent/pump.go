package agent

import (
	"context"
	"fmt"
	"log"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

// pumpReader forwards what the hardware reader scans onto the bridge, where the
// client server picks it up. It returns once ctx is done.
func (a *Agent) pumpReader(ctx context.Context, reader *nfc.NFCReader, bridge *server.ServerBridge) {
	for {
		select {
		case <-ctx.Done():
			return
		case data := <-reader.Data():
			a.forwardScan(data, bridge)
		case status := <-reader.StatusUpdates():
			if !bridge.SendDeviceStatus(status) {
				a.logger.Println("Warning: failed to send device status to bridge")
			}
		}
	}
}

// forwardScan applies the card-type filter and puts the scan on the bridge.
func (a *Agent) forwardScan(data nfc.NFCData, bridge *server.ServerBridge) {
	if data.Err != nil {
		log.Printf("Error: %v", data.Err)
		a.sendScan(data, bridge)
		return
	}

	if data.Card == nil {
		return
	}

	if !a.cardTypes.isAllowed(data.Card.Type) {
		log.Printf("Card type '%s' not in allowed list, ignoring", data.Card.Type)
		a.sendScan(nfc.NFCData{
			Err: fmt.Errorf("card type '%s' not allowed by filter", data.Card.Type),
		}, bridge)
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

	a.sendScan(data, bridge)
}

func (a *Agent) sendScan(data nfc.NFCData, bridge *server.ServerBridge) {
	if !bridge.SendTagData(data) {
		a.logger.Println("Warning: failed to send tag data to bridge")
	}
}
