package server

import "github.com/dotside-studios/davi-nfc-agent/nfc"

// DeviceOps is what a driver of remote devices offers the tag router: which
// device is holding which tag, and how to ask one to act on the tag it holds.
//
// Declared in the terms the router needs rather than the driver's own, so the
// router names no driver and the driver imports nothing to satisfy this. A
// second kind of remote device would implement the same four methods.
type DeviceOps interface {
	// TagOn reports the tag a device is holding. An empty deviceID asks for the
	// most recent scan across all devices.
	TagOn(deviceID string) (deviceHolding string, tag nfc.Tag, ok bool)

	// DevicesHoldingTags lists the devices currently holding one, most recent
	// first.
	DevicesHoldingTags() []string

	// WriteTag asks the device to encode ndef onto the tag it is holding, and
	// to lock it afterwards when lock is set. idempotencyKey identifies the
	// logical write, so a device that already applied it reports the previous
	// outcome instead of writing twice.
	WriteTag(deviceID, tagUID string, ndef []byte, lock bool, idempotencyKey string) error

	// TransceiveTag asks the device to exchange raw bytes with the tag.
	TransceiveTag(deviceID, tagUID string, data []byte, raw bool) ([]byte, error)
}
