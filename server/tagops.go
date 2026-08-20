package server

import (
	"context"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// TagOps performs an operation on the tag a request names, wherever it is: the
// agent's own reader, or a paired device holding it.
//
// Declared here because both sides of the call live below this package: the
// client server asks, and the tag router answers. These used to be six channels
// and a response-channel-per-request, which made an ordinary call look like a
// message bus and gave two objects background goroutines they only had in order
// to drain each other.
type TagOps interface {
	Write(ctx context.Context, req WriteOp) (*nfc.WriteResult, error)
	Lock(ctx context.Context, req LockOp) (*nfc.LockResult, error)
	Transceive(ctx context.Context, req TransceiveOp) ([]byte, error)
	Capabilities(ctx context.Context, req CapabilitiesOp) (*nfc.TagCapabilities, error)
}

// Target names the tag an operation applies to. Every operation carries one.
type Target struct {
	// TagUID names the tag. The operation is refused unless the tag it resolves
	// to carries this UID, so a card lifted since the scan cannot receive an
	// operation meant for another.
	TagUID string

	// DeviceID names the device holding it. Empty finds the tag by UID.
	DeviceID string

	// AllowUntargeted serves a request naming neither by guessing which tag it
	// meant. Asked for per request, so a client that cannot name its tag does
	// not weaken the guarantee for the others.
	AllowUntargeted bool
}

// WriteOp encodes a message onto the named tag.
type WriteOp struct {
	Target

	// Request carries the records to write and whether to lock afterwards.
	Request WriteRequest

	// IdempotencyKey identifies the logical write, so a device that already
	// applied it reports the previous outcome instead of writing twice.
	IdempotencyKey string
}

// LockOp makes the named tag permanently read-only.
type LockOp struct {
	Target

	// IdempotencyKey identifies the logical lock.
	IdempotencyKey string
}

// TransceiveOp exchanges raw bytes with the named tag.
type TransceiveOp struct {
	Target

	// Data is the command to send.
	Data []byte

	// Raw selects framing-level exchange over APDU-level.
	Raw bool
}

// CapabilitiesOp asks what the named tag supports.
type CapabilitiesOp struct {
	Target
}
