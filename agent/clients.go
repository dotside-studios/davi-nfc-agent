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

// deviceRoster is what a manager carrying devices of its own can report about
// them. Declared here in the terms the agent needs, so it names no driver.
type deviceRoster interface {
	GetDeviceCount() int
	GetActiveDeviceCount() int
	ListDevices() ([]string, error)
}

// RemoteDevices counts the devices registered with the agent and how many of
// them are active.
//
// It asks the manager, digging past an aggregate to the child holding devices.
// That dig is what a manager carrying devices of its own costs today, and it
// goes when the supervisor answers for every device the agent can see.
func (a *Agent) RemoteDevices() (total, active int) {
	roster := a.deviceRoster()
	if roster == nil {
		return 0, 0
	}
	return roster.GetDeviceCount(), roster.GetActiveDeviceCount()
}

func (a *Agent) deviceRoster() deviceRoster {
	if a.manager == nil {
		return nil
	}
	if roster, ok := a.manager.(deviceRoster); ok {
		return roster
	}

	aggregate, ok := a.manager.(interface {
		GetManager(string) (nfc.Manager, bool)
	})
	if !ok {
		return nil
	}
	child, exists := aggregate.GetManager(nfc.ManagerTypeSmartphone)
	if !exists {
		return nil
	}
	roster, _ := child.(deviceRoster)
	return roster
}

// OnlineDevices lists the devices connected to the agent right now, by ID. A
// paired device absent from this list is one that is not connected.
func (a *Agent) OnlineDevices() []string {
	roster := a.deviceRoster()
	if roster == nil {
		return nil
	}
	ids, err := roster.ListDevices()
	if err != nil {
		return nil
	}
	return ids
}

// IsReader reports whether a device path names something that can be the
// agent's reader. A phone reports its scans over the device bridge and is never
// opened as one.
func (a *Agent) IsReader(devicePath string) bool {
	return !nfc.IsRemoteDevice(a.manager, devicePath)
}
