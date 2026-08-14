package protocol

import "time"

// DeviceCapabilities defines the capabilities of a connected NFC device.
//
// The first three fields are the original v0 declaration. Everything below is
// additive: a v0 device omits them and reads as all-false, which is what it
// could actually do anyway.
type DeviceCapabilities struct {
	CanRead  bool   `json:"canRead"`
	CanWrite bool   `json:"canWrite"`
	NFCType  string `json:"nfcType"` // "nfca", "nfcb", "nfcf", "nfcv", "isodep", etc.

	// CanTransceive is APDU-level exchange (Android IsoDep.transceive, iOS
	// sendCommand, PN532 InDataExchange). CanTransceiveRaw is framing-level
	// exchange (Android NfcA.transceive, PN532 InCommunicateThru) — a strictly
	// rarer capability, which is why it is a separate bit.
	CanTransceive    bool `json:"canTransceive,omitempty"`
	CanTransceiveRaw bool `json:"canTransceiveRaw,omitempty"`
	CanLock          bool `json:"canLock,omitempty"`

	SupportedTagTypes []string `json:"supportedTagTypes,omitempty"` // e.g. ["MIFARE Classic", "NTAG"]
	DeviceType        string   `json:"deviceType,omitempty"`        // e.g. "smartphone", "pn532-serial"
	MaxBaudRate       int      `json:"maxBaudRate,omitempty"`
}

// DeviceRegistrationRequest is sent by a device to register with the server.
type DeviceRegistrationRequest struct {
	DeviceName   string             `json:"deviceName"`   // e.g., "John's iPhone 12"
	Platform     string             `json:"platform"`     // "ios" or "android"
	AppVersion   string             `json:"appVersion"`   // e.g., "1.0.0"
	Capabilities DeviceCapabilities `json:"capabilities"` // Device capabilities
	Metadata     map[string]string  `json:"metadata"`     // Optional metadata
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

// DeviceTagData is sent by a device when a tag is scanned.
type DeviceTagData struct {
	DeviceID    string            `json:"deviceID"`    // Device that scanned the tag
	UID         string            `json:"uid"`         // Tag UID (hex format)
	Technology  string            `json:"technology"`  // "ISO14443A", "ISO14443B", etc.
	Type        string            `json:"type"`        // "MIFARE Classic 1K", "Type4", etc.
	ATR         string            `json:"atr"`         // Answer to Reset (if applicable)
	ScannedAt   time.Time         `json:"scannedAt"`   // Timestamp of scan
	NDEFMessage *NDEFMessageInput `json:"ndefMessage"` // Parsed NDEF data (if available)
	RawData     []byte            `json:"rawData"`     // Raw tag data (base64 encoded)

	// Capabilities is what the device determined about this specific tag. When
	// omitted the agent infers them from Type, which is all a v0 device allows.
	Capabilities *TagCapabilities `json:"capabilities,omitempty"`
}

// DeviceHeartbeat is sent by a device periodically.
type DeviceHeartbeat struct {
	DeviceID  string    `json:"deviceID"`
	Timestamp time.Time `json:"timestamp"`
}

// DeviceTagRemovedData is sent by a device when a tag leaves the NFC field.
type DeviceTagRemovedData struct {
	DeviceID  string    `json:"deviceID"`
	UID       string    `json:"uid"`       // UID of the removed tag
	RemovedAt time.Time `json:"removedAt"` // Timestamp of removal
}

// DeviceWriteRequest is sent by server to a device for writing (future feature).
type DeviceWriteRequest struct {
	RequestID   string            `json:"requestID"`   // Unique request ID for correlation
	DeviceID    string            `json:"deviceID"`    // Target device
	NDEFMessage *NDEFMessageInput `json:"ndefMessage"` // Data to write
}

// DeviceWriteResponse is sent by a device after a write operation (future feature).
type DeviceWriteResponse struct {
	RequestID string `json:"requestID"`
	Success   bool   `json:"success"`
	Error     string `json:"error,omitempty"`
}
