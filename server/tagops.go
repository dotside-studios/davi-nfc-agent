package server

import (
	"context"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

// TagOps performs an operation on the tag a request names, wherever it is: the
// agent's own reader, or a paired device holding it.
//
// Declared here because both sides of the call live below this package: the
// client server asks, and the tag router answers.
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

	// SessionID sends the exchange inside a raw session, which already names
	// the tag. Empty is an ordinary exchange.
	SessionID string
}

// RawSessionOps is what TagOps offers when it can lease the reader for a
// multi-step raw exchange. It is separate so an operation layer that cannot is
// not obliged to stub it: the server answers such a request as not supported.
type RawSessionOps interface {
	BeginRawSession(ctx context.Context, req RawSessionBeginOp) (RawSessionLease, error)
	EndRawSession(ctx context.Context, sessionID string) error
}

// RawSessionBeginOp leases the reader holding the named tag.
type RawSessionBeginOp struct {
	Target

	// TTL is how long the session lives without an exchange. Zero selects the
	// default.
	TTL time.Duration
}

// RawSessionLease is a granted raw session.
type RawSessionLease struct {
	SessionID string

	// TTL is the time to live granted, renewed by each exchange.
	TTL time.Duration
}

// SequenceOps is what TagOps offers when it can run several raw exchanges under
// one tag operation. Separate for the same reason as RawSessionOps.
type SequenceOps interface {
	TransceiveSequence(ctx context.Context, req SequenceOp) (*nfc.SequenceResult, error)
}

// SequenceOp runs steps against the named tag, or inside a raw session.
type SequenceOp struct {
	Target

	Steps []nfc.SequenceStep

	// SessionID runs the steps inside a raw session, which already names the
	// tag. Empty is an ordinary run.
	SessionID string
}

// NTAG424Ops is what TagOps offers when it can run NTAG 424 DNA operations.
// Separate so an operation layer that cannot is not obliged to stub it.
type NTAG424Ops interface {
	NTAG424(ctx context.Context, req NTAG424Op) (*protocol.NTAG424ResponsePayload, error)
}

// NTAG424Op is an NTAG 424 DNA operation on the named tag. Request carries the
// operation and its arguments as the client sent them.
type NTAG424Op struct {
	Target

	Request protocol.NTAG424RequestPayload
}

// CapabilitiesOp asks what the named tag supports.
type CapabilitiesOp struct {
	Target
}
