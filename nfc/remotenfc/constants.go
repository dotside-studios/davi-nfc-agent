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
