package pairingplugin

import (
	"github.com/dotside-studios/davi-nfc-agent/agent"
)

// runs reports whether a component of that name is registered, and names lists
// them for a failure message. The plugin registers a listener of its own, so a
// count is no longer the thing to assert.
func runs(a *agent.Agent, name string) bool {
	for _, c := range a.Components() {
		if c.Name() == name {
			return true
		}
	}
	return false
}

func names(a *agent.Agent) []string {
	var out []string
	for _, c := range a.Components() {
		out = append(out, c.Name())
	}
	return out
}
