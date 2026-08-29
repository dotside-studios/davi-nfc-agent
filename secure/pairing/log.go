package pairing

import "github.com/dotside-studios/davi-nfc-agent/logbuf"

// What this package reports: which devices were admitted, refused, and
// disconnected. See [logbuf.Channel].
var (
	admitLog  = logbuf.Channel("pairing", logbuf.LevelInfo)
	admitWarn = logbuf.Channel("pairing", logbuf.LevelWarn)
)
