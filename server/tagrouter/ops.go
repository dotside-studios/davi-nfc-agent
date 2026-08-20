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
	// The mode gates every route to a tag, not just the hardware one. The
	// reader enforces it inside prepareCardForWrite, which a write routed to a
	// device never reaches.
	if !modeAllowsTagModification(s.config.Reader) {
		return nil, protocol.Errorf(protocol.ErrCodeReadOnly, "%s", readOnlyModeMessage("writes"))
	}

	rt, err := s.resolveRoute(req.TagUID, req.DeviceID, req.AllowUntargeted)
	if err != nil {
		return nil, err
	}

	ndefMsg, err := server.BuildNDEFMessage(req.Request)
	if err != nil {
		return nil, protocol.WrapError(protocol.ErrCodeInvalidRequest, err, "invalid write request")
	}

	if !rt.reader {
		return s.writeViaDevice(req, ndefMsg, rt.device)
	}

	// WriteMessageWithResult performs a capacity check, retries on transient
	// failures, and verifies the write by reading it back.
	return s.config.Reader.WriteMessageWithResult(ndefMsg, nfc.WriteOptions{
		Overwrite: true,
		Index:     -1,
		Lock:      req.Request.Lock,
		ExpectUID: req.TagUID,
	})
}

// writeViaDevice asks the device holding the tag to perform the write.
//
// It reports what the agent knows rather than what it checked: the device
// answers success or failure and nothing else, so the UID and the lock are
// filled in from the request and Verified stays false. It used to return a bare
// map here, which the client server could not read as a result at all, so a
// write to a phone reported success with none of the fields the protocol
// documents.
func (s *Router) writeViaDevice(req server.WriteOp, msg *nfc.NDEFMessage, active deviceTag) (*nfc.WriteResult, error) {
	ndefBytes, err := msg.Encode()
	if err != nil {
		return nil, protocol.WrapError(protocol.ErrCodeInvalidRequest, err, "could not encode the message")
	}

	err = s.devices.WriteTag(active.DeviceID, active.UID, ndefBytes, req.Request.Lock, req.IdempotencyKey)
	if err != nil {
		return nil, deviceFailure(err, active.DeviceID, "write")
	}

	return &nfc.WriteResult{
		UID:          active.UID,
		TagType:      tagType(active.Tag),
		BytesWritten: len(ndefBytes),
		Verified:     false,
		Attempts:     1,
		Locked:       req.Request.Lock,
	}, nil
}

// Lock makes the named tag permanently read-only.
func (s *Router) Lock(ctx context.Context, req server.LockOp) (*nfc.LockResult, error) {
	if !modeAllowsTagModification(s.config.Reader) {
		return nil, protocol.Errorf(protocol.ErrCodeReadOnly, "%s", readOnlyModeMessage("locks"))
	}

	rt, err := s.resolveRoute(req.TagUID, req.DeviceID, req.AllowUntargeted)
	if err != nil {
		return nil, err
	}

	if !rt.reader {
		return s.lockViaDevice(req, rt.device)
	}

	// Named, so the reader refuses if the tag present is not that one. A lock
	// cannot be undone.
	return s.config.Reader.LockCardExpecting(req.TagUID)
}

func (s *Router) lockViaDevice(req server.LockOp, active deviceTag) (*nfc.LockResult, error) {
	// A lock travels as a write with no message: the device protocol has one
	// tag-modifying frame, not two.
	if err := s.devices.WriteTag(active.DeviceID, active.UID, nil, true, req.IdempotencyKey); err != nil {
		return nil, deviceFailure(err, active.DeviceID, "lock")
	}

	// The device reports the outcome but not the tag type.
	return &nfc.LockResult{UID: active.UID, Locked: true}, nil
}

// Transceive exchanges raw bytes with the named tag.
func (s *Router) Transceive(ctx context.Context, req server.TransceiveOp) ([]byte, error) {
	// A raw exchange cannot be assumed harmless: the same call carries a SELECT
	// and a write to a configuration page, so the mode treats it as a write.
	if !modeAllowsTagModification(s.config.Reader) {
		return nil, protocol.Errorf(protocol.ErrCodeReadOnly,
			"Reader is in read-only mode; raw exchanges are refused because they can write")
	}

	rt, err := s.resolveRoute(req.TagUID, req.DeviceID, req.AllowUntargeted)
	if err != nil {
		return nil, err
	}

	if !rt.reader {
		return s.transceiveViaDevice(req, rt.device)
	}
	return s.config.Reader.TransceiveExpecting(req.Data, req.TagUID)
}

func (s *Router) transceiveViaDevice(req server.TransceiveOp, active deviceTag) ([]byte, error) {
	data, err := s.devices.TransceiveTag(active.DeviceID, active.UID, req.Data, req.Raw)
	if err != nil {
		return nil, deviceFailure(err, active.DeviceID, "exchange")
	}
	return data, nil
}

// deviceFailure reports a driver error.
//
// A failure the device itself reported already says what went wrong and carries
// its own code, so it passes through: prefixing it buries the useful half. Only
// a transport failure, which says nothing about the tag, is labelled with the
// device and operation.
func deviceFailure(err error, deviceID, op string) error {
	var coded *protocol.CodedError
	if errors.As(err, &coded) {
		return err
	}
	return protocol.WrapError(operationErrorCode(err, protocol.ErrCodeDeviceGone), err,
		"device %s did not complete the %s", deviceID, op)
}

// Capabilities reports what the named tag supports.
func (s *Router) Capabilities(ctx context.Context, req server.CapabilitiesOp) (*nfc.TagCapabilities, error) {
	rt, err := s.resolveRoute(req.TagUID, req.DeviceID, req.AllowUntargeted)
	if err != nil {
		return nil, err
	}

	if !rt.reader {
		// Answered from what the device declared at the scan, with no round
		// trip, so it costs nothing to ask.
		caps := nfc.GetTagCapabilities(rt.device.Tag)
		return &caps, nil
	}
	return s.config.Reader.GetCapabilitiesExpecting(req.TagUID)
}

// tagType names the tag's type when one is known.
func tagType(tag nfc.Tag) string {
	if tag == nil {
		return ""
	}
	return tag.Type()
}
