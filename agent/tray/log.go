package tray

import "github.com/dotside-studios/davi-nfc-agent/logbuf"

// What the tray reports about what an operator did with it. See
// [logbuf.Channel].
var (
	trayLog  = logbuf.Channel("systray", logbuf.LevelInfo)
	trayWarn = logbuf.Channel("systray", logbuf.LevelWarn)
	trayFail = logbuf.Channel("systray", logbuf.LevelError)
)
