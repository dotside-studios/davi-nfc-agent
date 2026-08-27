package serverplugin

import "github.com/dotside-studios/davi-nfc-agent/logbuf"

// authWarn reports connections the device endpoint refused. It keeps the
// "device" channel the console filters device activity by, rather than the
// plugin's own name: what an operator is looking for is why a phone was turned
// away.
var authWarn = logbuf.Channel("device", logbuf.LevelWarn)
