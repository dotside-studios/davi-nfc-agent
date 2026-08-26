//go:build !nowebui

package console

import "github.com/dotside-studios/davi-nfc-agent/logbuf"

// What the console reports about what an operator did through it, and what it
// refused. See [logbuf.Channel].
var (
	consoleLog  = logbuf.Channel("console", logbuf.LevelInfo)
	consoleWarn = logbuf.Channel("console", logbuf.LevelWarn)
	consoleFail = logbuf.Channel("console", logbuf.LevelError)
)
