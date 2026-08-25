package remotenfc

import (
	"strconv"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

// The methods below present this driver in the terms a consumer needs, without
// its wire types. Nothing here imports the package that declares the interface:
// a Go interface is satisfied by shape, so the driver stays free of its
// consumers and a second kind of remote device implements the same four
// methods.

// TagOn reports the tag a device is holding. An empty deviceID asks for the
// most recent scan across all devices.
func (m *Manager) TagOn(deviceID string) (string, nfc.Tag, bool) {
	info, ok := m.ActiveTag(deviceID)
	if !ok {
		return "", nil, false
	}
	return info.DeviceID, info.Tag, true
}

// DevicesHoldingTags lists the devices currently holding one, most recent
// first.
func (m *Manager) DevicesHoldingTags() []string { return m.ActiveTagDevices() }

// WriteTag asks the device to encode ndef onto the tag it is holding.
func (m *Manager) WriteTag(deviceID, tagUID string, ndef []byte, lock bool, idempotencyKey string) error {
	resp, err := m.WriteToDevice(deviceID, DeviceWriteRequest{
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

// TransceiveTag asks the device to exchange raw bytes with the tag.
func (m *Manager) TransceiveTag(deviceID, tagUID string, data []byte, raw bool) ([]byte, error) {
	resp, err := m.TransceiveWithDevice(deviceID, DeviceTransceiveRequest{
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
