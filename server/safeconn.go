// Package server provides shared server utilities.
package server

import "github.com/dotside-studios/davi-nfc-agent/wsconn"

// SafeConn is the shared write-safe WebSocket wrapper. It lives in wsconn so
// packages outside server/ can use it without depending on the bridge.
type SafeConn = wsconn.SafeConn

// NewSafeConn wraps a connection so its writes are serialized.
var NewSafeConn = wsconn.NewSafeConn
