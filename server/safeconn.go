// Package server provides shared server utilities.
package server

import "github.com/dotside-studios/davi-nfc-agent/server/wsconn"

// SafeConn is the shared write-safe WebSocket wrapper. It lives in
// server/wsconn, a leaf package, so nfc/remotenfc can use it without depending
// on the bridge.
type SafeConn = wsconn.SafeConn

// NewSafeConn wraps a connection so its writes are serialized.
var NewSafeConn = wsconn.NewSafeConn
