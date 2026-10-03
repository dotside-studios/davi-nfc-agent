package nfc

import (
	"context"
	"time"
)

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

	// The operations below take a context. Cancelling it abandons the wait and
	// returns ctx.Err(); it does not abort a transfer already on the wire, so
	// an operation may still be applied after the caller has given up. TagOn
	// and DevicesHoldingTags take none: both answer from memory.

	// WriteTag encodes msg onto the tag the named device is holding, locking it
	// afterwards when lock is set. idempotencyKey identifies the logical write,
	// so a source that already applied it reports the previous outcome rather
	// than writing twice.
	//
	// The result describes what was written and whether it could be confirmed,
	// which differs by source: a reader reads the tag back, a phone answers
	// from what it did.
	WriteTag(ctx context.Context, deviceID, tagUID string, msg *NDEFMessage, lock bool, idempotencyKey string) (*WriteResult, error)

	// LockTag makes the tag the named device is holding permanently read-only.
	LockTag(ctx context.Context, deviceID, tagUID, idempotencyKey string) (*LockResult, error)

	// TransceiveTag exchanges raw bytes with the tag.
	TransceiveTag(ctx context.Context, deviceID, tagUID string, data []byte, raw bool) ([]byte, error)

	// TagCapabilities reports what the tag the named device is holding
	// supports.
	TagCapabilities(ctx context.Context, deviceID, tagUID string) (*TagCapabilities, error)
}

// RawSessionHolder is what a TagHolder offers when it can lease the reader
// holding a tag for a multi-step raw exchange, such as an authentication whose
// state a poll or another operation would reset. It is separate from TagHolder
// because a phone cannot grant one, and a holder that cannot is simply not one.
type RawSessionHolder interface {
	// BeginRawSessionTag leases the reader holding the tag. A zero ttl selects
	// DefaultRawSessionTTL, and it is capped at MaxRawSessionTTL. Each exchange
	// renews the lease.
	BeginRawSessionTag(ctx context.Context, deviceID, tagUID string, ttl time.Duration) (leaseID string, err error)

	// EndRawSessionTag releases a lease. An unknown one is refused with a
	// raw-session-expired error.
	EndRawSessionTag(ctx context.Context, leaseID string) error

	// TransceiveInSessionTag exchanges raw bytes inside a lease.
	TransceiveInSessionTag(ctx context.Context, leaseID string, data []byte) ([]byte, error)
}

// SequenceHolder is what a TagHolder offers when it can run several raw
// exchanges under one tag operation. A holder that cannot is simply not one.
type SequenceHolder interface {
	// TransceiveSequenceTag runs the steps against the tag, with nothing else
	// reaching the card between them.
	TransceiveSequenceTag(ctx context.Context, deviceID, tagUID string, steps []SequenceStep) (*SequenceResult, error)

	// TransceiveSequenceInSessionTag runs the steps inside a lease.
	TransceiveSequenceInSessionTag(ctx context.Context, leaseID string, steps []SequenceStep) (*SequenceResult, error)
}

// NTAG424Holder is what a TagHolder offers when it can run NTAG 424 DNA
// operations on a tag it holds. A tag held by a phone cannot, so the holder
// answers it as not supported.
type NTAG424Holder interface {
	// WithNTAG424Tag runs fn under one tag operation on the tag, which must be
	// an NTAG 424 DNA.
	WithNTAG424Tag(ctx context.Context, deviceID, tagUID string, fn func(NTAG424Operator) error) error
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
