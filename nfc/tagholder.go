package nfc

// TagHolder is what the tag router asks: which source is holding which tag, and
// how to act on the tag one of them holds.
//
// Both kinds of source satisfy it. A reader the agent opened holds the tag on
// it, and a phone holds the tag its user tapped, so a caller routing an
// operation asks the same questions of either and never learns which it
// reached.
//
// Declared in the terms the router needs rather than a driver's own, so the
// router names no driver and a driver imports nothing to satisfy this.
type TagHolder interface {
	// TagOn reports the tag a device is holding, by UID. An empty deviceID asks
	// for the most recent scan across the devices this holds.
	//
	// The answer selects a route rather than authorising an operation: whatever
	// performs one re-checks the tag it has against the UID it was given.
	TagOn(deviceID string) (deviceHolding, tagUID string, ok bool)

	// DevicesHoldingTags lists the devices currently holding one, most recent
	// first.
	DevicesHoldingTags() []string

	// WriteTag encodes msg onto the tag the named device is holding, locking it
	// afterwards when lock is set. idempotencyKey identifies the logical write,
	// so a source that already applied it reports the previous outcome rather
	// than writing twice.
	//
	// The result describes what was written and whether it could be confirmed,
	// which differs by source: a reader reads the tag back, a phone answers
	// from what it did.
	WriteTag(deviceID, tagUID string, msg *NDEFMessage, lock bool, idempotencyKey string) (*WriteResult, error)

	// LockTag makes the tag the named device is holding permanently read-only.
	LockTag(deviceID, tagUID, idempotencyKey string) (*LockResult, error)

	// TransceiveTag exchanges raw bytes with the tag.
	TransceiveTag(deviceID, tagUID string, data []byte, raw bool) ([]byte, error)

	// TagCapabilities reports what the tag the named device is holding
	// supports.
	TagCapabilities(deviceID, tagUID string) (*TagCapabilities, error)
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
