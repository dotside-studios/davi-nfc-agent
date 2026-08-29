package serverplugin

import (
	"context"
	"io"
	"log"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// testOptions returns options that keep Setup off the network and out of the
// user's real config directory.
func testOptions(t *testing.T) *agent.Options {
	t.Helper()

	opts := agent.DefaultOptions()
	opts.ConfigDir = t.TempDir()
	opts.AutoTLS = false
	opts.BootstrapPort = 0
	return opts
}

// counter is a component that says how many times it was started and stopped.
type counter struct {
	name    string
	started int
	stopped int
}

func (c *counter) Name() string                { return c.name }
func (c *counter) Start(context.Context) error { c.started++; return nil }
func (c *counter) Stop() error                 { c.stopped++; return nil }

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

// pluginFunc is a plugin written as one function, which is all most of them
// are.
type pluginFunc func(agent.AgentContext) error

func (f pluginFunc) Activate(ctx agent.AgentContext) error { return f(ctx) }
