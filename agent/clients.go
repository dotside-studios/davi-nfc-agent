package agent

import (
	"errors"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/server/clientserver"
)

// What the agent reports about the clients connected to it and the tags it has
// seen. A surface asks the agent rather than reaching for the servers it holds:
// those are nil until Start and are replaced by every restart.

// ClientCount is how many clients are connected.
func (a *Agent) ClientCount() int {
	if a.ClientServer == nil {
		return 0
	}
	return a.ClientServer.ClientCount()
}

// Clients lists the connected clients, most recently connected first.
func (a *Agent) Clients() []clientserver.ClientInfo {
	if a.ClientServer == nil {
		return nil
	}
	return a.ClientServer.Clients()
}

// DisconnectClient drops one client's connection. It reports an error for a
// client that is not connected, which includes one that just left.
func (a *Agent) DisconnectClient(id string) error {
	if a.ClientServer == nil {
		return errors.New("agent is not running")
	}
	if !a.ClientServer.Disconnect(id) {
		return errors.New("no such client: it may have already disconnected")
	}
	return nil
}

// LastCard is the most recent scan, from a reader or from a device. Nil before
// anything has been scanned.
func (a *Agent) LastCard() *nfc.Card { return a.lastCard.Load() }

// reportTag records a scan and reports it. Every scan a client receives passes
// through here, whichever source produced it, so this is where the agent learns
// what was last presented to it.
func (a *Agent) reportTag(data nfc.NFCData) {
	if data.Card != nil {
		a.lastCard.Store(data.Card)
	}
	a.events.Tag.Emit(data)
}

// Devices connected to the agent that report their own scans, rather than
// readers it opened. The manager describes each device it offers, so this is a
// question about capabilities and names no driver.

// OnlineDevices lists the devices connected right now, by the identity each
// holds: for one that paired, the identity it paired with. A paired device
// absent from this list is one that is not connected.
func (a *Agent) OnlineDevices() []string {
	listings, err := a.manager.Devices()
	if err != nil {
		a.logger.Printf("Listing devices failed: %v", err)
		return nil
	}

	var online []string
	for _, listing := range listings {
		if !listing.Capabilities.CanPoll {
			online = append(online, listing.ID)
		}
	}
	return online
}
