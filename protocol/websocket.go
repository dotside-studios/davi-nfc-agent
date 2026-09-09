package protocol

import "github.com/dotside-studios/davi-nfc-agent/nfc"

// MessageType names a message. An alias rather than a named type: the envelope
// below is shared with the device protocol, which speaks its own vocabulary, so
// the set of values is not closed.
type MessageType = string

// The client protocol's messages, and the whole of it: a type not listed here
// is answered with ErrCodeUnknownType. Requests are sent by a client, responses
// answer one by ID, and tagData and deviceStatus are broadcast unsolicited.
const (
	WSTypeTagData      MessageType = "tagData"
	WSTypeDeviceStatus MessageType = "deviceStatus"

	WSTypeWriteRequest  MessageType = "writeRequest"
	WSTypeWriteResponse MessageType = "writeResponse"

	WSTypeLockRequest  MessageType = "lockRequest"
	WSTypeLockResponse MessageType = "lockResponse"

	WSTypeCapabilitiesRequest  MessageType = "capabilitiesRequest"
	WSTypeCapabilitiesResponse MessageType = "capabilitiesResponse"

	WSTypeTransceiveRequest  MessageType = "transceiveRequest"
	WSTypeTransceiveResponse MessageType = "transceiveResponse"

	WSTypeError MessageType = "error"
)

// WebSocketMessage is the generic message envelope for WebSocket communication.
type WebSocketMessage struct {
	ID      string      `json:"id,omitempty"`
	Type    MessageType `json:"type"`
	Payload any         `json:"payload"`
}

// WebSocketRequest is for incoming requests from WebSocket clients.
type WebSocketRequest struct {
	ID      string         `json:"id,omitempty"`
	Type    MessageType    `json:"type"`
	Payload map[string]any `json:"payload,omitempty"`
}

// WebSocketResponse is for responses to WebSocket requests.
type WebSocketResponse struct {
	ID      string      `json:"id,omitempty"`
	Type    MessageType `json:"type"`
	Success bool        `json:"success"`
	Payload any         `json:"payload,omitempty"`
	Error   string      `json:"error,omitempty"`
}

// DeviceStatusPayload is the payload of a deviceStatus message: what the
// agent's own reader is doing. Defined in the nfc package beside the reader
// that reports it.
type DeviceStatusPayload = nfc.DeviceStatus

// TagTarget names the tag an operation applies to. Every tag request carries
// it, so a request reaches the tag the client meant rather than whichever one
// is in the field when it arrives.
type TagTarget struct {
	// DeviceID names the device holding the tag. Empty means the tag is found
	// by UID instead. Clients learn the value from a tagData broadcast.
	DeviceID string `json:"deviceID,omitempty"`

	// UID names the tag itself. The operation is refused unless the tag it
	// reaches carries it.
	UID string `json:"uid,omitempty"`

	// AllowUntargeted opts into the agent guessing which tag was meant when
	// neither UID nor DeviceID is given, instead of refusing the request.
	AllowUntargeted bool `json:"allowUntargeted,omitempty"`

	// IdempotencyKey identifies the logical operation. A client that retries
	// after a lost response reuses it, so the operation is not applied twice.
	// Read on writes and locks; a raw exchange cannot be deduplicated.
	IdempotencyKey string `json:"idempotencyKey,omitempty"`
}

// WriteRecord is a single NDEF record in a write request. The Type field
// selects how the remaining fields are interpreted.
type WriteRecord struct {
	// Type selects the record kind. Supported values:
	//   "text"                              - Content (+ optional Language)
	//   "uri" / "url"                       - Content
	//   "mailto"/"email", "tel", "sms", "geo" - Content (scheme prepended if absent)
	//   "smartposter"                       - Content (URI) + optional Title/Language
	//   "mime"                              - MimeType + Payload (or Content)
	//   "vcard"                             - Content or Payload (vCard data)
	//   "external"                          - Content (domain:type) + optional Payload
	//   "aar"                               - Content (Android package name)
	//   "raw"                               - TNF + TypeBytes + optional ID + Payload
	// Empty Type defaults to "text".
	Type string `json:"type"`

	// Content carries the primary value: text, URI, domain, package name, etc.
	Content string `json:"content,omitempty"`

	// Language is the ISO language code for text records (default: "en").
	Language string `json:"language,omitempty"`

	// MimeType is the media type for "mime" records.
	MimeType string `json:"mimeType,omitempty"`

	// Title is the optional display title for "smartposter" records.
	Title string `json:"title,omitempty"`

	// Payload holds raw bytes for "mime", "vcard", "external", and "raw"
	// records (base64-encoded in JSON).
	Payload []byte `json:"payload,omitempty"`

	// TNF, TypeBytes, and ID are used only for "raw" records.
	TNF       *uint8 `json:"tnf,omitempty"`
	TypeBytes []byte `json:"typeBytes,omitempty"`
	ID        []byte `json:"id,omitempty"`
}

// WriteRequestPayload is the payload of a writeRequest: the complete NDEF
// message to encode onto the tag it names.
//
// The API overwrites rather than appends. A client that wants to add a record
// reads the tag, modifies what it read, and sends the whole message back.
type WriteRequestPayload struct {
	TagTarget

	// Records is the NDEF message to write, in order.
	Records []WriteRecord `json:"records"`

	// Lock, when true, makes the tag permanently read-only after a successful
	// write. Only tags that support locking honor this. WARNING: irreversible.
	Lock bool `json:"lock,omitempty"`
}
