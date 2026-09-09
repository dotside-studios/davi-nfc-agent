package protocol

import "github.com/dotside-studios/davi-nfc-agent/nfc"

// NDEFMessageInput represents an NDEF message for input.
type NDEFMessageInput struct {
	Records []NDEFRecordInput `json:"records"`
}

// NDEFRecordInput represents a single NDEF record for input.
// Supports both high-level (type+content) and low-level (TNF+payload) formats.
type NDEFRecordInput struct {
	// High-level format (preferred for simple records)
	RecordType string `json:"recordType,omitempty"` // "text", "uri", "mime", "external"
	Content    string `json:"content,omitempty"`    // Text content or URI
	Language   string `json:"language,omitempty"`   // Language code for text (default: "en")
	MimeType   string `json:"mimeType,omitempty"`   // MIME type for mime records

	// Low-level format (for advanced use cases)
	TNF     *uint8 `json:"tnf,omitempty"`     // Type Name Format (0x00-0x07)
	Type    []byte `json:"type,omitempty"`    // NDEF record type bytes
	ID      []byte `json:"id,omitempty"`      // Optional record ID
	Payload []byte `json:"payload,omitempty"` // Raw payload bytes (base64 in JSON)
}

// NDEFRecordPayload and NDEFMessagePayload are the JSON-friendly form of an
// NDEF message, carried inside a tagData broadcast. Defined in the nfc package
// beside the message they are made from.
type NDEFRecordPayload = nfc.NDEFRecordPayload

type NDEFMessagePayload = nfc.NDEFMessagePayload
