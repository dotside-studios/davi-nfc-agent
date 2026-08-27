package remotenfc

import (
	"context"
	"strconv"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

// The methods below present this driver in the terms a consumer needs, without
// its wire types. Nothing here imports the package that declares the interface:
// a Go interface is satisfied by shape, so the driver stays free of its
// consumers, and a reader answers the same questions about the tag on it.

// TagOn reports the tag a device is holding, by UID. An empty deviceID asks for
// the most recent scan across all devices.
func (m *Manager) TagOn(deviceID string) (string, string, bool) {
	info, ok := m.ActiveTag(deviceID)
	if !ok {
		return "", "", false
	}
	return info.DeviceID, info.Tag.UID(), true
}

// DevicesHoldingTags lists the devices currently holding one, most recent
// first.
func (m *Manager) DevicesHoldingTags() []string { return m.ActiveTagDevices() }

// WriteTag asks the device to encode msg onto the tag it is holding.
//
// What comes back reports what the agent knows rather than what it checked.
// Whether the write could be confirmed is the tag's answer, not this driver's:
// a tag whose reads are a snapshot cannot confirm one, which is the same fact
// a reader's pipeline consults.
func (m *Manager) WriteTag(ctx context.Context, deviceID, tagUID string, msg *nfc.NDEFMessage, lock bool, idempotencyKey string) (*nfc.WriteResult, error) {
	ndef, err := msg.Encode()
	if err != nil {
		return nil, protocol.WrapError(protocol.ErrCodeInvalidRequest, err, "could not encode the message")
	}

	if err := m.writeToTag(ctx, deviceID, tagUID, ndef, lock, idempotencyKey); err != nil {
		return nil, err
	}

	tag := m.tagOn(deviceID)
	return &nfc.WriteResult{
		UID:          tagUID,
		TagType:      tagTypeOf(tag),
		BytesWritten: len(ndef),
		Verified:     confirmable(tag),
		Attempts:     1,
		Locked:       lock,
	}, nil
}

// LockTag makes the tag the device is holding permanently read-only. It travels
// as a write with no message: the device protocol has one tag-modifying frame,
// not two.
func (m *Manager) LockTag(ctx context.Context, deviceID, tagUID, idempotencyKey string) (*nfc.LockResult, error) {
	if err := m.writeToTag(ctx, deviceID, tagUID, nil, true, idempotencyKey); err != nil {
		return nil, err
	}

	// The device reports the outcome but not the tag type.
	return &nfc.LockResult{UID: tagUID, Locked: true}, nil
}

// TagCapabilities answers from what the device declared at the scan, with no
// round trip, so it costs nothing to ask.
func (m *Manager) TagCapabilities(_ context.Context, deviceID, tagUID string) (*nfc.TagCapabilities, error) {
	tag := m.tagOn(deviceID)
	if tag == nil {
		return nil, protocol.Errorf(protocol.ErrCodeNoCard, "device %s is not holding a tag", deviceID)
	}

	caps := nfc.GetTagCapabilities(tag)
	return &caps, nil
}

// tagOn is the tag itself, which this driver needs where the router only needs
// its UID.
func (m *Manager) tagOn(deviceID string) nfc.Tag {
	info, ok := m.ActiveTag(deviceID)
	if !ok {
		return nil
	}
	return info.Tag
}

// writeToTag is the one tag-modifying frame the device protocol has.
func (m *Manager) writeToTag(ctx context.Context, deviceID, tagUID string, ndef []byte, lock bool, idempotencyKey string) error {
	resp, err := m.WriteToDevice(ctx, deviceID, DeviceWriteRequest{
		RequestID:      m.nextRequestID("write"),
		TagUID:         tagUID,
		NDEFBytes:      ndef,
		Lock:           lock,
		IdempotencyKey: idempotencyKey,
	})
	if err != nil {
		return err
	}
	if !resp.Success {
		return protocol.Errorf(codeOr(resp.ErrorCode, protocol.ErrCodeWriteFailed), "%s", resp.Error)
	}
	return nil
}

// tagTypeOf names the tag's type when one is known.
func tagTypeOf(tag nfc.Tag) string {
	if tag == nil {
		return ""
	}
	return tag.Type()
}

// confirmable reports whether a write to this tag could have been confirmed by
// reading it back. Asked of the tag rather than assumed from where it is, so
// the answer comes from the same capability a reader's pipeline consults.
func confirmable(tag nfc.Tag) bool {
	if tag == nil {
		return false
	}
	return !tag.Capabilities().ReadsAreSnapshot
}

// TransceiveTag asks the device to exchange raw bytes with the tag.
func (m *Manager) TransceiveTag(ctx context.Context, deviceID, tagUID string, data []byte, raw bool) ([]byte, error) {
	resp, err := m.TransceiveWithDevice(ctx, deviceID, DeviceTransceiveRequest{
		RequestID: m.nextRequestID("transceive"),
		DeviceID:  deviceID,
		TagUID:    tagUID,
		Data:      data,
		Raw:       raw,
	})
	if err != nil {
		return nil, err
	}
	if !resp.Success {
		return nil, protocol.Errorf(codeOr(resp.ErrorCode, protocol.ErrCodeTransceiveFailed), "%s", resp.Error)
	}
	return resp.Data, nil
}

// nextRequestID labels a request to a device, which correlates its reply by it.
func (m *Manager) nextRequestID(op string) string {
	return op + "-" + strconv.FormatUint(m.reqSeq.Add(1), 10)
}

// codeOr prefers a code the device supplied over the operation's own label.
func codeOr(code, fallback protocol.ErrorCode) protocol.ErrorCode {
	if code == "" {
		return fallback
	}
	return code
}
