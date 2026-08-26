package traymenu

import "github.com/dotside-studios/davi-nfc-agent/event"

// Signal is [event.Signal], aliased so a menu's callbacks are registered
// without naming a second package.
type Signal[T any] = event.Signal[T]

// Connection is [event.Connection], the handle returned by Connect.
type Connection = event.Connection
