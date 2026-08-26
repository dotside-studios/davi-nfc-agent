package listener

import "github.com/dotside-studios/davi-nfc-agent/logbuf"

// What this package reports about the port it binds and the service it
// advertises. See [logbuf.Channel].
var (
	listenerLog  = logbuf.Channel("listener", logbuf.LevelInfo)
	listenerWarn = logbuf.Channel("listener", logbuf.LevelWarn)
	listenerFail = logbuf.Channel("listener", logbuf.LevelError)
)
