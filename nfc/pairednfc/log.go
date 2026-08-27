package pairednfc

import "github.com/dotside-studios/davi-nfc-agent/logbuf"

// What this package reports: which devices were admitted, refused, and
// disconnected. See [logbuf.Channel].
var (
	pairedLog  = logbuf.Channel("paired", logbuf.LevelInfo)
	pairedWarn = logbuf.Channel("paired", logbuf.LevelWarn)
)
