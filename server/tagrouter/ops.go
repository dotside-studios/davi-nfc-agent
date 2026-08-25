package tagrouter

import (
	"context"
	"errors"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

var _ server.TagOps = (*Router)(nil)

// Write encodes a message onto the tag the request names.
func (s *Router) Write(ctx context.Context, req server.WriteOp) (*nfc.WriteResult, error) {
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

	result, err := rt.holder.WriteTag(rt.device, rt.uid, msg, req.Request.Lock, req.IdempotencyKey)
	if err != nil {
		return nil, sourceFailure(err, rt.device, "write", protocol.ErrCodeWriteFailed)
	}
	return result, nil
}

// Lock makes the named tag permanently read-only.
func (s *Router) Lock(ctx context.Context, req server.LockOp) (*nfc.LockResult, error) {
	if !s.modificationAllowed() {
		return nil, protocol.Errorf(protocol.ErrCodeReadOnly, "%s", readOnlyModeMessage("locks"))
	}

	rt, err := s.resolveRoute(req.TagUID, req.DeviceID, req.AllowUntargeted)
	if err != nil {
		return nil, err
	}

	// Named, so whatever holds it refuses if the tag present is not that one. A
	// lock cannot be undone.
	result, err := rt.holder.LockTag(rt.device, rt.uid, req.IdempotencyKey)
	if err != nil {
		return nil, sourceFailure(err, rt.device, "lock", protocol.ErrCodeLockFailed)
	}
	return result, nil
}

// Transceive exchanges raw bytes with the named tag.
func (s *Router) Transceive(ctx context.Context, req server.TransceiveOp) ([]byte, error) {
	// A raw exchange cannot be assumed harmless: the same call carries a SELECT
	// and a write to a configuration page, so the mode treats it as a write.
	if !s.modificationAllowed() {
		return nil, protocol.Errorf(protocol.ErrCodeReadOnly,
			"Reader is in read-only mode; raw exchanges are refused because they can write")
	}

	rt, err := s.resolveRoute(req.TagUID, req.DeviceID, req.AllowUntargeted)
	if err != nil {
		return nil, err
	}

	data, err := rt.holder.TransceiveTag(rt.device, rt.uid, req.Data, req.Raw)
	if err != nil {
		return nil, sourceFailure(err, rt.device, "exchange", protocol.ErrCodeTransceiveFailed)
	}
	return data, nil
}

// Capabilities reports what the named tag supports.
func (s *Router) Capabilities(ctx context.Context, req server.CapabilitiesOp) (*nfc.TagCapabilities, error) {
	rt, err := s.resolveRoute(req.TagUID, req.DeviceID, req.AllowUntargeted)
	if err != nil {
		return nil, err
	}

	caps, err := rt.holder.TagCapabilities(rt.device, rt.uid)
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
