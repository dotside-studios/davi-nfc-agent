package nfc

// TagHolder is optionally implemented by a Manager whose devices hold tags of
// their own: which device is holding which tag, and how to ask one to act on
// the tag it holds.
//
// Declared in the terms the tag router needs rather than a driver's own, so the
// router names no driver and a driver imports nothing to satisfy this. A second
// kind of remote device implements the same four methods.
type TagHolder interface {
	// TagOn reports the tag a device is holding. An empty deviceID asks for the
	// most recent scan across all devices.
	TagOn(deviceID string) (deviceHolding string, tag Tag, ok bool)

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

// TagsHeldBy returns the manager's holder of tags, or nil for one whose devices
// hold none, which is every manager whose devices are polled through a reader.
func TagsHeldBy(m Manager) TagHolder {
	holder, ok := m.(TagHolder)
	if !ok {
		return nil
	}
	return holder
}
