package trustplugin

import (
	"io"
	"log"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// quietAgent is an agent with nothing behind it: no listener, no reader worth
// the name, and a log that goes nowhere. Enough to activate plugins against.
func quietAgent(t *testing.T, plugins ...agent.Plugin) *agent.Agent {
	t.Helper()

	return agent.New(agent.Config{
		Manager: nfc.NewMockManager(),
		Logger:  log.New(io.Discard, "", 0),
		Plugins: plugins,
	})
}
