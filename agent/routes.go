package agent

import (
	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// TagModificationAllowed reports whether the agent's mode currently permits
// writes, locks and raw exchanges.
//
// Read when the operation happens rather than when the endpoint was built: the
// endpoint outlives any particular reader, and the mode changes while running.
func (a *Agent) TagModificationAllowed() bool {
	readers := a.supervisor.Load()
	if readers == nil {
		return true
	}
	return readers.Mode() != nfc.ModeReadOnly
}

// RawAPDUAllowed reports whether the raw APDU channel is open, which is a gate
// of its own on a raw exchange, on top of the mode: a raw command reaches the
// tag unmodified and can lock or brick it, so it stays refused until an operator
// opens the channel even where a write would be allowed.
//
// Read when the operation happens rather than when the endpoint was built, so a
// change through SetRawAPDUEnabled reaches the connections already open.
func (a *Agent) RawAPDUAllowed() bool {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.allowRawAPDU
}
