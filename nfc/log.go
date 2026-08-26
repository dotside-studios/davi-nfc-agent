package nfc

import "github.com/dotside-studios/davi-nfc-agent/logbuf"

// What this package reports, and at what severity. The channel names what a
// line came from and the level says what it is, both stated here rather than
// written into each call. See [logbuf.Channel].
var (
	// readerLog and readerFail carry a reader's own doings: what it connected
	// to, what it scanned, and what went wrong with the hardware.
	readerLog  = logbuf.Channel("device", logbuf.LevelInfo)
	readerWarn = logbuf.Channel("device", logbuf.LevelWarn)
	readerFail = logbuf.Channel("device", logbuf.LevelError)

	// supervisorFail carries what operating the readers could not do: which it
	// could not list, and which it could not open.
	supervisorFail = logbuf.Channel("supervisor", logbuf.LevelError)
)
