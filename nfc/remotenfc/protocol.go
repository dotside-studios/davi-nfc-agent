package remotenfc

import (
	"fmt"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

// DeviceCapabilities defines the capabilities of a smartphone NFC device.
type DeviceCapabilities struct {
	CanRead  bool   `json:"canRead"`
	CanWrite bool   `json:"canWrite"`
	NFCType  string `json:"nfcType"` // "nfca", "nfcb", "nfcf", "nfcv", "isodep", etc.
}

// DeviceRegistrationRequest is sent by mobile app to register as an NFC device.
type DeviceRegistrationRequest struct {
	DeviceName      string             `json:"deviceName"`      // e.g., "John's iPhone 12"
	Platform        string             `json:"platform"`        // "ios" or "android"
	AppVersion      string             `json:"appVersion"`      // e.g., "1.0.0"
	ProtocolVersion int                `json:"protocolVersion"` // Negotiated bridge protocol version
	Capabilities    DeviceCapabilities `json:"capabilities"`    // Device capabilities
	Metadata        map[string]string  `json:"metadata"`        // Optional metadata
}

// DeviceRegistrationResponse is sent by server after successful registration.
type DeviceRegistrationResponse struct {
	DeviceID     string     `json:"deviceID"`     // Unique device identifier (UUID)
	SessionToken string     `json:"sessionToken"` // Authentication token (optional future use)
	ServerInfo   ServerInfo `json:"serverInfo"`
}

// ServerInfo contains information about the server.
type ServerInfo struct {
	Version      string   `json:"version"`
	SupportedNFC []string `json:"supportedNFC"` // ["mifare", "desfire", etc.]
}

// TagData is sent by mobile app when a tag is scanned.
type TagData struct {
	DeviceID    string                     `json:"deviceID"`    // Device that scanned the tag
	UID         string                     `json:"uid"`         // Tag UID (hex format)
	Technology  string                     `json:"technology"`  // "ISO14443A", "ISO14443B", etc.
	Type        string                     `json:"type"`        // "MIFARE Classic 1K", "Type4", etc.
	ATR         string                     `json:"atr"`         // Answer to Reset (if applicable)
	ScannedAt   time.Time                  `json:"scannedAt"`   // Timestamp of scan
	NDEFMessage *protocol.NDEFMessageInput `json:"ndefMessage"` // Parsed NDEF data (if available)
	RawData     []byte                     `json:"rawData"`     // Raw tag data (base64 encoded)
}

// DeviceHeartbeat is sent by mobile app periodically.
type DeviceHeartbeat struct {
	DeviceID  string    `json:"deviceID"`
	Timestamp time.Time `json:"timestamp"`
}

// TagRemovedData is sent by mobile app when a tag leaves the NFC field.
type TagRemovedData struct {
	DeviceID  string    `json:"deviceID"`
	UID       string    `json:"uid"`       // UID of the removed tag
	RemovedAt time.Time `json:"removedAt"` // Timestamp of removal
}

// DeviceWriteRequest is sent by server to mobile app (future feature).
type DeviceWriteRequest struct {
	RequestID   string                     `json:"requestID"`   // Unique request ID for correlation
	DeviceID    string                     `json:"deviceID"`    // Target device
	NDEFMessage *protocol.NDEFMessageInput `json:"ndefMessage"` // Data to write
	Options     nfc.WriteOptions           `json:"options"`     // Write options
}

// DeviceWriteResponse is sent by mobile app to server (future feature).
type DeviceWriteResponse struct {
	RequestID string `json:"requestID"`
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
}

// ConvertTagData converts mobile app tag data to internal nfc.Tag.
func ConvertTagData(data TagData) (nfc.Tag, error) {
	// Validate required fields
	if data.UID == "" {
		return nil, fmt.Errorf("tag UID is required")
	}
	if data.Technology == "" {
		return nil, fmt.Errorf("tag technology is required")
	}

	// Normalize UID format
	uid, err := protocol.ParseUID(data.UID)
	if err != nil {
		return nil, fmt.Errorf("invalid UID format: %w", err)
	}

	// Parse NDEF message if present
	var ndefMsg *nfc.NDEFMessage
	var ndefData []byte
	if data.NDEFMessage != nil {
		ndefMsg, err = nfc.ConvertNDEFInput(data.NDEFMessage)
		if err != nil {
			return nil, fmt.Errorf("failed to parse NDEF message: %w", err)
		}
		// Encode NDEF message to bytes
		ndefData, err = ndefMsg.Encode()
		if err != nil {
			return nil, fmt.Errorf("failed to encode NDEF message: %w", err)
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
	}

	return tag, nil
}
