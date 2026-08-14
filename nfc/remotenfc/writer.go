package remotenfc

import "github.com/dotside-studios/davi-nfc-agent/nfc"

// TagWriter performs write and lock operations on a tag a remote device is
// holding. The implementation lives in the device server, which owns the
// WebSocket sessions; this package only needs a way to reach it.
type TagWriter interface {
	// WriteTag writes the encoded NDEF message to the tag the device holds.
	WriteTag(deviceID, tagUID string, ndef []byte, opts nfc.WriteOptions) error

	// LockTag makes the tag the device holds permanently read-only.
	LockTag(deviceID, tagUID string) error

	// DeviceCanWrite reports whether the device declared write support and is
	// still connected. Tags consult it so their capabilities do not outlive the
	// session that backs them.
	DeviceCanWrite(deviceID string) bool

	// DeviceCanLock reports whether the device declared lock support and is
	// still connected.
	DeviceCanLock(deviceID string) bool
}
