package listener

import "github.com/dotside-studios/davi-nfc-agent/logbuf"

// What this package reports about the port it binds and the service it
// advertises. See [logbuf.Channel].
var (
	listenerLog  = logbuf.Channel("unified", logbuf.LevelInfo)
	listenerWarn = logbuf.Channel("unified", logbuf.LevelWarn)
	listenerFail = logbuf.Channel("unified", logbuf.LevelError)
)
