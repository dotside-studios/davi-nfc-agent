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
