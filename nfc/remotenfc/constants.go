package remotenfc

import "time"

// Device timing constants.
//
// DeviceTimeout allows three missed heartbeats at HeartbeatInterval. They were
// previously both 30s, so a device that only heartbeats sat exactly on the
// sweep boundary and could be dropped mid-interval.
const (
	HeartbeatInterval = 30 * time.Second       // Expected heartbeat frequency
	DeviceTimeout     = 90 * time.Second       // Silence after which a device is dropped
	CleanupInterval   = 15 * time.Second       // How often silence is checked
	GetTagsTimeout    = 500 * time.Millisecond // GetTags blocking timeout
)

// MaxDeviceMessageSize caps an inbound frame on the device endpoint.
//
// The largest legitimate frame is a write carrying an NDEF message twice over:
// the parsed records plus the base64 `ndefBytes` of the same message. A Type 4
// tag's NDEF file runs to tens of kilobytes, so the ceiling is set well above
// that rather than at any one tag's capacity — the point is to bound the
// allocation a peer can provoke, not to enforce a tag limit the reader already
// enforces. Anything larger is a bug or an attack, and a device that trips it
// loses its session.
//
// The console endpoint sets 4 KB, which is right for a link that carries no
// tag payloads and wrong here.
const MaxDeviceMessageSize = 256 << 10

// Scan publishing.
//
// ScanQueueDepth buffers scans and removals between the sessions reporting them
// and the goroutine broadcasting them. It absorbs a burst; it does not absorb a
// subscriber that is permanently behind.
//
// ScanPublishTimeout is how long a session waits for room in that queue before
// giving up. Waiting applies backpressure to the one device whose scan it is,
// which is why it can be this long: the other sessions are unaffected.
const (
	ScanQueueDepth     = 256
	ScanPublishTimeout = 2 * time.Second
)
