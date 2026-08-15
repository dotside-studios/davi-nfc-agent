//go:build !nocontrol

package main

import (
	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/tls"
)

// setupControlCenter builds the console's API and attaches it to the agent.
//
// This is the whole entry point for the feature. A build with -tags nocontrol
// gets the stub in control_disabled.go instead, and everything downstream is
// written to tolerate a nil ControlServer.
func setupControlCenter(
	agent *Agent,
	settings *SettingsStore,
	logs *logbuf.Ring,
	bootstrap *tls.BootstrapServer,
	bootstrapPort int,
) *ControlServer {
	control := NewControlServer(agent, NewControlAuth(), settings, logs, bootstrap, bootstrapPort)
	agent.Control = control
	return control
}
