package clientserver

import (
	"context"
	"errors"
	"fmt"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

// tagOps performs a client's tag operations by resolving the tag the request
// names against whatever is holding one, and reporting the outcome in the codes
// a client understands.
//
// It is what the server does when it was given tags rather than an operation
// layer of its own. Neither the resolution nor the wire vocabulary belongs to a
// source: a reader and a phone both answer for a tag, and neither knows what a
// client calls a failure.
type tagOps struct {
	// tags answers for every tag the agent can reach, which is the supervisor:
	// the readers it polls and the devices that report their own scans.
	tags nfc.TagHolder

	// allowModification reports whether writes, locks and raw exchanges are
	// allowed, which is the agent's mode rather than any one source's. Nil
	// allows them, and the source enforces its own policy either way.
	allowModification func() bool

	// allowRawTransceive reports whether the raw APDU channel is open, gating
	// raw exchanges on their own beyond the mode above. Nil leaves the mode as
	// the only gate. See [Config.AllowRawTransceive].
	allowRawTransceive func() bool
}

var _ server.TagOps = (*tagOps)(nil)

// modificationAllowed reports whether the agent permits a write, a lock or a
// raw exchange. It governs tags held by devices as well as those on a reader:
// the mode is the agent's.
func (s *tagOps) modificationAllowed() bool {
	return s.allowModification == nil || s.allowModification()
}

// rawTransceiveAllowed reports whether the raw APDU channel is open. It is a
// second gate on a raw exchange, on top of the mode: a writable agent still
// refuses one until the operator opens the channel. Nil leaves the mode as the
// only gate.
func (s *tagOps) rawTransceiveAllowed() bool {
	return s.allowRawTransceive == nil || s.allowRawTransceive()
}

// readOnlyModeMessage explains a mode refusal for the named operation.
func readOnlyModeMessage(operations string) string {
	return fmt.Sprintf("Agent is in read-only mode; %s are refused", operations)
}

// operationErrorCode classifies a source failure, falling back to the
// operation's own label when the error carries no code of its own.
func operationErrorCode(err error, fallback protocol.ErrorCode) protocol.ErrorCode {
	if payload := protocol.ErrorPayloadFor(err); payload.Code != protocol.ErrCodeUnknownError {
		return payload.Code
	}
	return fallback
}

// Write encodes a message onto the tag the request names.
func (s *tagOps) Write(ctx context.Context, req server.WriteOp) (*nfc.WriteResult, error) {
	// The mode gates every route to a tag, not just the one on a reader. What
	// performs the write enforces it too, which a write routed to a device
	// never reaches.
	if !s.modificationAllowed() {
		return nil, protocol.Errorf(protocol.ErrCodeReadOnly, "%s", readOnlyModeMessage("writes"))
	}

	rt, err := s.resolveRoute(req.TagUID, req.DeviceID, req.AllowUntargeted)
	if err != nil {
		return nil, err
	}

	msg, err := server.BuildNDEFMessage(req.Request)
	if err != nil {
		return nil, protocol.WrapError(protocol.ErrCodeInvalidRequest, err, "invalid write request")
	}

	result, err := rt.holder.WriteTag(ctx, rt.device, rt.uid, msg, req.Request.Lock, req.IdempotencyKey)
	if err != nil {
		return nil, sourceFailure(err, rt.device, "write", protocol.ErrCodeWriteFailed)
	}
	return result, nil
}

// Lock makes the named tag permanently read-only.
func (s *tagOps) Lock(ctx context.Context, req server.LockOp) (*nfc.LockResult, error) {
	if !s.modificationAllowed() {
		return nil, protocol.Errorf(protocol.ErrCodeReadOnly, "%s", readOnlyModeMessage("locks"))
	}

	rt, err := s.resolveRoute(req.TagUID, req.DeviceID, req.AllowUntargeted)
	if err != nil {
		return nil, err
	}

	// Named, so whatever holds it refuses if the tag present is not that one. A
	// lock cannot be undone.
	result, err := rt.holder.LockTag(ctx, rt.device, rt.uid, req.IdempotencyKey)
	if err != nil {
		return nil, sourceFailure(err, rt.device, "lock", protocol.ErrCodeLockFailed)
	}
	return result, nil
}

// Transceive exchanges raw bytes with the named tag.
func (s *tagOps) Transceive(ctx context.Context, req server.TransceiveOp) ([]byte, error) {
	// A raw exchange cannot be assumed harmless: the same call carries a SELECT
	// and a write to a configuration page, so the mode treats it as a write.
	if !s.modificationAllowed() {
		return nil, protocol.Errorf(protocol.ErrCodeReadOnly,
			"Reader is in read-only mode; raw exchanges are refused because they can write")
	}

	// The channel is gated on its own beyond the mode: a raw command reaches the
	// tag unmodified and can lock or brick it in ways nothing here can undo, so a
	// writable agent still refuses one until the operator opens the channel.
	if !s.rawTransceiveAllowed() {
		return nil, protocol.Errorf(protocol.ErrCodeRawChannelDisabled,
			"Raw APDU channel is disabled; enable it to send raw exchanges")
	}

	rt, err := s.resolveRoute(req.TagUID, req.DeviceID, req.AllowUntargeted)
	if err != nil {
		return nil, err
	}

	data, err := rt.holder.TransceiveTag(ctx, rt.device, rt.uid, req.Data, req.Raw)
	if err != nil {
		return nil, sourceFailure(err, rt.device, "exchange", protocol.ErrCodeTransceiveFailed)
	}
	return data, nil
}

// Capabilities reports what the named tag supports.
func (s *tagOps) Capabilities(ctx context.Context, req server.CapabilitiesOp) (*nfc.TagCapabilities, error) {
	rt, err := s.resolveRoute(req.TagUID, req.DeviceID, req.AllowUntargeted)
	if err != nil {
		return nil, err
	}

	caps, err := rt.holder.TagCapabilities(ctx, rt.device, rt.uid)
	if err != nil {
		return nil, sourceFailure(err, rt.device, "capability report", protocol.ErrCodeCapabilitiesFailed)
	}
	return caps, nil
}

// sourceFailure reports a failure from whatever was holding the tag.
//
// A failure the source itself reported already says what went wrong and carries
// its own code, so it passes through: prefixing it buries the useful half. One
// that says nothing is labelled with the source and reported as the operation
// failing, which is all the router knows: a reader and a phone fail in their
// own ways and neither is the other.
func sourceFailure(err error, device, op string, failed protocol.ErrorCode) error {
	var coded *protocol.CodedError
	if errors.As(err, &coded) {
		return err
	}
	return protocol.WrapError(operationErrorCode(err, failed), err,
		"device %s did not complete the %s", device, op)
}

// newTagOps is what the server performs a client's operations with when it was
// given tags rather than an operation layer of its own.
func newTagOps(config Config) *tagOps {
	return &tagOps{
		tags:               config.Tags,
		allowModification:  config.AllowTagModification,
		allowRawTransceive: config.AllowRawTransceive,
	}
}
