package tagrouter

import (
	"context"
	"fmt"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

var _ server.TagOps = (*Router)(nil)

// requestID labels a request to the device, which correlates its reply by it.
// A caller no longer supplies one, since nothing correlates on this side any
// more: the call returns the answer.
func (s *Router) requestID(op string) string {
	return fmt.Sprintf("%s-%d", op, s.seq.Add(1))
}

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
func (s *Router) writeViaDevice(req server.WriteOp, msg *nfc.NDEFMessage, active remotenfc.ActiveTagInfo) (*nfc.WriteResult, error) {
	ndefBytes, err := msg.Encode()
	if err != nil {
		return nil, protocol.WrapError(protocol.ErrCodeInvalidRequest, err, "could not encode the message")
	}

	resp, err := s.remote.WriteToDevice(active.DeviceID, remotenfc.DeviceWriteRequest{
		RequestID:      s.requestID("write"),
		TagUID:         active.UID,
		NDEFMessage:    server.BuildNDEFInput(req.Request),
		NDEFBytes:      ndefBytes,
		Lock:           req.Request.Lock,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		return nil, protocol.WrapError(operationErrorCode(err, protocol.ErrCodeDeviceGone), err,
			"device %s did not complete the write", active.DeviceID)
	}
	if !resp.Success {
		return nil, protocol.Errorf(orCode(resp.ErrorCode, protocol.ErrCodeWriteFailed), "%s", resp.Error)
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

func (s *Router) lockViaDevice(req server.LockOp, active remotenfc.ActiveTagInfo) (*nfc.LockResult, error) {
	resp, err := s.remote.WriteToDevice(active.DeviceID, remotenfc.DeviceWriteRequest{
		RequestID:      s.requestID("lock"),
		TagUID:         active.UID,
		Lock:           true,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		return nil, protocol.WrapError(operationErrorCode(err, protocol.ErrCodeDeviceGone), err,
			"device %s did not complete the lock", active.DeviceID)
	}
	if !resp.Success {
		return nil, protocol.Errorf(orCode(resp.ErrorCode, protocol.ErrCodeLockFailed), "%s", resp.Error)
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

func (s *Router) transceiveViaDevice(req server.TransceiveOp, active remotenfc.ActiveTagInfo) ([]byte, error) {
	resp, err := s.remote.TransceiveWithDevice(active.DeviceID, remotenfc.DeviceTransceiveRequest{
		RequestID: s.requestID("transceive"),
		DeviceID:  active.DeviceID,
		TagUID:    active.UID,
		Data:      req.Data,
		Raw:       req.Raw,
	})
	if err != nil {
		return nil, protocol.WrapError(protocol.ErrCodeTransceiveFailed, err,
			"device %s did not complete the exchange", active.DeviceID)
	}
	if !resp.Success {
		return nil, protocol.Errorf(orCode(resp.ErrorCode, protocol.ErrCodeTransceiveFailed), "%s", resp.Error)
	}
	return resp.Data, nil
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

// orCode prefers a code the device supplied over the operation's own label.
func orCode(code, fallback protocol.ErrorCode) protocol.ErrorCode {
	if code == "" {
		return fallback
	}
	return code
}

// tagType names the tag's type when one is known.
func tagType(tag nfc.Tag) string {
	if tag == nil {
		return ""
	}
	return tag.Type()
}
