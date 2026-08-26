package pcsc

import "github.com/dotside-studios/davi-nfc-agent/logbuf"

// What this package reports about the PC/SC readers it drives. See
// [logbuf.Channel].
var (
	pcscLog  = logbuf.Channel("pcsc", logbuf.LevelInfo)
	pcscWarn = logbuf.Channel("pcsc", logbuf.LevelWarn)
)
