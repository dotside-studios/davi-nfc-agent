package server

import "github.com/dotside-studios/davi-nfc-agent/logbuf"

// What this package reports about the device connections it serves. See
// [logbuf.Channel].
var deviceLog = logbuf.Channel("device", logbuf.LevelInfo)
