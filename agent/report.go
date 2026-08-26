package agent

import (
	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// What the agent reports about what it has seen, answered whether or not it is
// running. The readers a run opens come and go; the last card presented to them
// does not.

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

// OnlineDevices lists the devices connected right now that report their own
// scans, rather than readers the agent opened, by the identity each holds. A
// paired device absent from this list is not connected.
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
