package agent

import "github.com/dotside-studios/davi-nfc-agent/logbuf"

// What this package reports when there is no agent to report through: while
// Setup resolves a configuration, and where a helper was given no logger. The
// name is the agent's own, so these read as the agent's lines. See
// [logbuf.Channel].
var (
	agentLog  = logbuf.Channel("agent", logbuf.LevelInfo)
	agentWarn = logbuf.Channel("agent", logbuf.LevelWarn)
)
