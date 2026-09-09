package protocol

import "github.com/dotside-studios/davi-nfc-agent/nfc"

// The payloads the client protocol carries. Each one is the JSON a client
// actually receives, so the json tags here are the field names it reads and
// server/clientserver/testdata holds a fixture per shape.

// TagMessagePayload is what was read off a tag, inside a tagData broadcast.
// Two shapes share it: an NDEF message carries Records, and anything else
// carries Data, the bytes as they were read.
type TagMessagePayload struct {
	// Type is "ndef" or "raw", and says which of the two below is set.
	Type string `json:"type"`

	Records []nfc.NDEFRecordPayload `json:"records,omitempty"`
	Data    []byte                  `json:"data,omitempty"`
}

// TagDataPayload is the payload of a tagData broadcast reporting a tag in the
// field. A tag leaving is reported as [TagRemovedPayload] on the same message
// type.
type TagDataPayload struct {
	UID        string `json:"uid"`
	Type       string `json:"type"`
	Technology string `json:"technology"`

	// ScannedAt is when the tag was read, RFC3339.
	ScannedAt string `json:"scannedAt"`

	// Capabilities is what can be done to this tag. A client checks it rather
	// than inferring from Type: a tag a phone holds is writable only while that
	// device is connected and said so.
	Capabilities nfc.TagCapabilities `json:"capabilities"`

	// DeviceID names the device that scanned it. Absent for the agent's own
	// reader, which is the only source deviceStatus describes.
	DeviceID string `json:"deviceID,omitempty"`

	// Message is what the tag holds, absent when it could not be read.
	Message *TagMessagePayload `json:"message,omitempty"`

	// Text is the first text record, for a client that wants nothing else.
	Text string `json:"text"`

	// Err is what went wrong reading the tag, null when nothing did. Always
	// present: a client reads it to tell a failed scan from a clean one.
	Err *string `json:"err"`
}

// TagRemovedPayload is the payload of a tagData broadcast reporting that no tag
// is in the field. An empty UID is how a client recognises it. It also carries
// a scan that failed before any tag was read, in Err.
type TagRemovedPayload struct {
	UID  string  `json:"uid"`
	Text string  `json:"text"`
	Err  *string `json:"err"`
}

// WriteResponsePayload answers a writeRequest that landed, with what reached
// the tag: whether the agent read the data back and it matched, how many
// attempts it took, and whether the tag ended up locked.
type WriteResponsePayload struct {
	Message      string `json:"message"`
	UID          string `json:"uid"`
	TagType      string `json:"tagType"`
	BytesWritten int    `json:"bytesWritten"`
	Verified     bool   `json:"verified"`
	Attempts     int    `json:"attempts"`
	Locked       bool   `json:"locked"`
}

// WriteAcknowledgement answers a writeRequest that landed and reported no
// result: the write succeeded and there is nothing to say about the tag. Only a
// build supplying its own tag operations produces it.
type WriteAcknowledgement struct {
	Message string `json:"message"`
}

// LockResponsePayload answers a lockRequest that landed. It carries no message:
// a lock reports the tag it reached and that it is now read-only, and nothing
// else. Defined in nfc beside the operation that produces it.
type LockResponsePayload = nfc.LockResult

// TransceiveResponsePayload answers a transceiveRequest with the tag's reply,
// base64 as raw bytes are in both directions.
type TransceiveResponsePayload struct {
	Data string `json:"data"`
}

// TransceiveRequestPayload is a raw exchange with the tag it names.
//
// It embeds [TagTarget] for the naming, so IdempotencyKey parses here as it
// does elsewhere, but a raw exchange is not deduplicated: a retry is a second
// exchange with the tag.
type TransceiveRequestPayload struct {
	TagTarget

	// Data is the command, base64.
	Data string `json:"data"`

	// Raw exchanges at the framing level rather than wrapping the bytes as an
	// APDU. A framing-level reply carries no ISO 7816 status word.
	Raw bool `json:"raw"`
}

// HealthPayload is the body of /health and /api/v1/health, which is what a
// client library polls to find the agent.
type HealthPayload struct {
	Status string `json:"status"`

	// Type is always "agent", telling it apart from anything else on the port.
	Type string `json:"type"`

	// Timestamp is when the agent answered, RFC3339.
	Timestamp string `json:"timestamp"`

	// Clients is how many are connected right now.
	Clients int `json:"clients"`
}
