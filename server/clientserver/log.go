package clientserver

import "github.com/dotside-studios/davi-nfc-agent/logbuf"

// What this package reports about the clients it serves: who connected, and
// what could not be sent to them. See [logbuf.Channel].
var (
	clientLog  = logbuf.Channel("client", logbuf.LevelInfo)
	clientWarn = logbuf.Channel("client", logbuf.LevelWarn)
	clientFail = logbuf.Channel("client", logbuf.LevelError)
)
