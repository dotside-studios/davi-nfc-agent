package remotenfc

import (
	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// DeviceRegistrationRequest is sent by mobile app to register as an NFC device.
type DeviceRegistrationRequest struct {
	DeviceName      string `json:"deviceName"`      // e.g., "John's iPhone 12"
	Platform        string `json:"platform"`        // "ios" or "android"
	AppVersion      string `json:"appVersion"`      // e.g., "1.0.0"
	ProtocolVersion int    `json:"protocolVersion"` // Negotiated bridge protocol version
	// Capabilities is what the device says it can do. A pointer so that saying
	// nothing is distinguishable from declaring every field false: the two
	// arrived alike while this was a value, and silence was read as refusal.
	// Mirrors DeviceTagData.Capabilities, which has answered this way since a
	// tag that declared nothing stopped meaning a tag that can do nothing.
	Capabilities *DeviceCapabilities `json:"capabilities,omitempty"`
	Metadata     map[string]string   `json:"metadata"` // Optional metadata
}

// TagData is what a device reports when it scans a tag.
//
// This and the two below are aliases rather than copies. They duplicated the
// wire shapes field for field and JSON tag for JSON tag, which bought a
// hand-written translation step in the device server and nothing else: two
// names for one thing drift, and these had already begun to.
type TagData = DeviceTagData

// TagRemovedData is what a device reports when a tag leaves its field.
type TagRemovedData = DeviceTagRemovedData

// ConvertTagData converts a device's tag report to an nfc.Tag. The result is
// read-only, having no route back to the device that scanned it; a tag the
// manager builds gets one.
func ConvertTagData(data TagData) (nfc.Tag, error) {
	return convertTagData(data, nil)
}

// convertTagData converts a device's tag report, wiring writes, locks and raw
// exchanges back to the device that scanned it.
func convertTagData(data TagData, route tagRoute) (nfc.Tag, error) {
	// Malformed input, reported as InvalidData: resending the same payload
	// cannot fix it.
	const op = "ConvertTagData"

	if data.UID == "" {
		return nil, nfc.Errorf(nfc.ErrCodeInvalidData, op, "tag UID is required")
	}
	if data.Technology == "" {
		return nil, nfc.Errorf(nfc.ErrCodeInvalidData, op, "tag technology is required")
	}

	// Normalize UID format
	uid, err := ParseUID(data.UID)
	if err != nil {
		return nil, nfc.WrapError(nfc.ErrCodeInvalidData, op, "invalid UID format", err)
	}

	// Parse NDEF message if present
	var ndefMsg *nfc.NDEFMessage
	var ndefData []byte
	if data.NDEFMessage != nil {
		ndefMsg, err = ConvertNDEFInput(data.NDEFMessage)
		if err != nil {
			return nil, wrapTagDataError(op, uid, "failed to parse NDEF message", err)
		}
		// Encode NDEF message to bytes
		ndefData, err = ndefMsg.Encode()
		if err != nil {
			return nil, wrapTagDataError(op, uid, "failed to encode NDEF message", err)
		}
	}

	// Create Tag instance
	tag := &Tag{
		uid:          uid,
		tagType:      data.Type,
		technology:   data.Technology,
		ndefData:     ndefData,
		ndefMsg:      ndefMsg,
		rawData:      data.RawData,
		scannedAt:    data.ScannedAt,
		sourceDevice: data.DeviceID,
		declaredCaps: data.Capabilities,
		route:        route,
	}

	return tag, nil
}

// wrapTagDataError preserves an underlying NFCError's code where there is one,
// so a genuine encoding fault is not relabelled as bad device input.
func wrapTagDataError(op, tagUID, message string, cause error) error {
	code := nfc.GetErrorCode(cause)
	if code == 0 {
		code = nfc.ErrCodeInvalidData
	}

	return &nfc.NFCError{
		Code:    code,
		Op:      op,
		TagUID:  tagUID,
		Message: message,
		Cause:   cause,
	}
}
