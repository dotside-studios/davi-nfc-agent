package multimanager

import "github.com/dotside-studios/davi-nfc-agent/logbuf"

// What this package reports: which managers it registered, and which one stopped
// answering. See [logbuf.Channel].
var (
	multiLog  = logbuf.Channel("multi", logbuf.LevelInfo)
	multiWarn = logbuf.Channel("multi", logbuf.LevelWarn)
)
