package server

import "github.com/dotside-studios/davi-nfc-agent/logbuf"

// What this package reports about the device connections it admits or turns
// away. See [logbuf.Channel].
var (
	deviceWarn = logbuf.Channel("device", logbuf.LevelWarn)
	deviceLog  = logbuf.Channel("device", logbuf.LevelInfo)
)
