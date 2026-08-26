package remotenfc

import "github.com/dotside-studios/davi-nfc-agent/logbuf"

// What this package reports. The manager's channel carries what the paired
// devices do; the endpoint's carries what arrives on the wire. See
// [logbuf.Channel].
var (
	managerLog  = logbuf.Channel("smartphone", logbuf.LevelInfo)
	managerWarn = logbuf.Channel("smartphone", logbuf.LevelWarn)
	managerFail = logbuf.Channel("smartphone", logbuf.LevelError)

	deviceLog  = logbuf.Channel("device", logbuf.LevelInfo)
	deviceWarn = logbuf.Channel("device", logbuf.LevelWarn)
	deviceFail = logbuf.Channel("device", logbuf.LevelError)
)
