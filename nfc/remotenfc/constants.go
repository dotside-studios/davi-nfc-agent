package remotenfc

import "time"

// Device timing constants.
//
// HeartbeatInterval is declarative: nothing reads it, and no device is told
// what it is. The JS reference client defaults to 30s, which is DeviceTimeout
// exactly, so a device that only heartbeats can be swept mid-interval while its
// socket is still open. Reconciling the two is part of giving the manager and
// the device session one notion of presence.
const (
	DeviceTimeout     = 30 * time.Second       // Device inactivity timeout
	HeartbeatInterval = 10 * time.Second       // Expected heartbeat frequency
	GetTagsTimeout    = 500 * time.Millisecond // GetTags blocking timeout
	CleanupInterval   = 15 * time.Second       // Cleanup check interval
)
