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

// MaxDeviceMessageSize caps an inbound frame on the device endpoint. A device
// that exceeds it loses its session.
//
// The largest legitimate frame is a write carrying an NDEF message twice over:
// the parsed records plus the base64 ndefBytes of the same message. A Type 4
// tag's NDEF file runs to tens of kilobytes, so the limit is set well above
// that. It bounds the allocation a peer can provoke; the reader enforces the
// tag's own capacity.
const MaxDeviceMessageSize = 256 << 10

// Scan publishing.
//
// ScanQueueDepth buffers scans and removals between the sessions reporting them
// and the goroutine broadcasting them. It absorbs a burst, not a subscriber
// that is permanently behind.
//
// ScanPublishTimeout is how long a session waits for room before giving up.
// Waiting blocks only the session whose scan it is, so it can be this long.
const (
	ScanQueueDepth     = 256
	ScanPublishTimeout = 2 * time.Second
)
