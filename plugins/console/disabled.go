//go:build nowebui

package console

import (
	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/plugin"
	"github.com/dotside-studios/davi-nfc-agent/settings"
)

// The stub for -tags nowebui, which omits the control center: no /control
// routes, no privileged API, no tray entry, and no frontend in the binary.
//
// A build tag rather than a Use line the command line leaves out, because the
// console carries an embedded frontend: leaving it unregistered would keep it
// in the binary.

// New reports that this build has no control center to serve.
func New(*agent.Agent, *settings.Store, *logbuf.Ring) plugin.Plugin { return nil }
