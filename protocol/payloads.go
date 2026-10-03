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
//
// Data is the card's whole reply, status word included. SW and Body split it
// for an APDU-level exchange that returned at least a status word.
type TransceiveResponsePayload struct {
	Data string `json:"data"`

	// SW is the reply's trailing status word, four uppercase hex characters.
	SW string `json:"sw,omitempty"`

	// Body is the reply without its status word, base64.
	Body string `json:"body,omitempty"`
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

	// SessionID sends the exchange inside a raw session begun with
	// rawSessionBeginRequest. The session already names the tag, so the target
	// fields are not needed.
	SessionID string `json:"sessionId,omitempty"`

	// AutoGetResponse has the agent follow a 61xx reply with GET RESPONSE
	// until the card stops, and retry a 6Cxx reply once with the corrected Le,
	// returning the concatenated body with the final status word. Omitted
	// returns the card's first reply unchanged. Not valid with Raw.
	AutoGetResponse bool `json:"autoGetResponse,omitempty"`
}

// RawSessionBeginRequestPayload leases the reader holding the tag it names, so
// a multi-step exchange such as an authentication is not reset by polling or
// by another operation.
type RawSessionBeginRequestPayload struct {
	TagTarget

	// TTLMs is how long the session lives without an exchange. Omitted selects
	// five seconds; the most is thirty.
	TTLMs int `json:"ttlMs,omitempty"`
}

// RawSessionBeginResponsePayload answers a rawSessionBeginRequest.
type RawSessionBeginResponsePayload struct {
	SessionID string `json:"sessionId"`

	// ExpiresInMs is the time to live granted, renewed by each exchange.
	ExpiresInMs int `json:"expiresInMs"`
}

// RawSessionEndRequestPayload ends a raw session.
type RawSessionEndRequestPayload struct {
	SessionID string `json:"sessionId"`
}

// RawSessionEndResponsePayload answers a rawSessionEndRequest.
type RawSessionEndResponsePayload struct {
	SessionID string `json:"sessionId"`
}

// TransceiveStep is one command of a transceiveSequenceRequest.
type TransceiveStep struct {
	// Data is the command, base64.
	Data string `json:"data"`

	// ExpectSW ends the run after this step unless the reply's status word is
	// one of these, each four hex characters. Omitted expects anything.
	ExpectSW []string `json:"expectSW,omitempty"`

	// StopOnSW ends the run after this step when the reply's status word is one
	// of these, each four hex characters.
	StopOnSW []string `json:"stopOnSW,omitempty"`

	// AutoGetResponse chains this step's reply as it does on a
	// transceiveRequest. ExpectSW and StopOnSW then see the final status word.
	AutoGetResponse bool `json:"autoGetResponse,omitempty"`
}

// TransceiveSequenceRequestPayload runs several APDU exchanges with the tag it
// names under one tag operation, so nothing else reaches the card between
// them. At most 32 steps.
type TransceiveSequenceRequestPayload struct {
	TagTarget

	Steps []TransceiveStep `json:"steps"`

	// SessionID runs the sequence inside a raw session begun with
	// rawSessionBeginRequest, which already names the tag.
	SessionID string `json:"sessionId,omitempty"`
}

// TransceiveSequenceResponsePayload answers a transceiveSequenceRequest.
type TransceiveSequenceResponsePayload struct {
	// Results holds one entry per step that ran, in order.
	Results []TransceiveResponsePayload `json:"results"`

	// StoppedAt is the index of the step whose reply ended the run early, or -1
	// when every step ran.
	StoppedAt int `json:"stoppedAt"`
}

// NTAG424SDMOptions are the key numbers and access rights an SDM URL is planned
// with. Each is a key number 0 to 4, 14 for free access or 15 for never. An
// omitted one takes its default: read is free, counterRet is never, and the
// rest are key 0. No key material appears here.
type NTAG424SDMOptions struct {
	MetaRead   *int `json:"metaRead,omitempty"`
	FileRead   *int `json:"fileRead,omitempty"`
	CounterRet *int `json:"counterRet,omitempty"`
	Change     *int `json:"change,omitempty"`
	Read       *int `json:"read,omitempty"`
	Write      *int `json:"write,omitempty"`
	ReadWrite  *int `json:"readWrite,omitempty"`

	// EncLength is the width of {enc} in mirrored characters, a multiple of 32.
	// Omitted means 32.
	EncLength int `json:"encLength,omitempty"`
}

// NTAG424RequestPayload is an operation on an NTAG 424 DNA the agent holds
// keys for. Op selects it: getFileSettings, configureSDM, changeKey,
// getCardUID, getKeyVersion, readSig or lock. configureSDM, changeKey and lock
// change the tag and are refused in read-only mode; changeKey and lock are
// irreversible and also need Confirm.
type NTAG424RequestPayload struct {
	TagTarget

	Op string `json:"op"`

	// FileNo is the file getFileSettings reads. Omitted means the NDEF file, 2.
	FileNo int `json:"fileNo,omitempty"`

	// URLTemplate and SDM plan configureSDM. The template holds {picc} or {uid}
	// and {ctr}, optionally {enc}, and {mac}.
	URLTemplate string             `json:"urlTemplate,omitempty"`
	SDM         *NTAG424SDMOptions `json:"sdm,omitempty"`

	// KeyNo is the key changeKey replaces and getKeyVersion reads. AuthKeyNo is
	// the key the session for changeKey authenticates with. Version is the new
	// key's version byte.
	KeyNo     int `json:"keyNo,omitempty"`
	AuthKeyNo int `json:"authKeyNo,omitempty"`
	Version   int `json:"version,omitempty"`

	// NewKeySource is "configured", the key the agent holds for KeyNo, or
	// "explicit", the 32 hex characters in NewKey. The key is never echoed or
	// logged.
	NewKeySource string `json:"newKeySource,omitempty"`
	NewKey       string `json:"newKey,omitempty"`

	// Confirm acknowledges an irreversible operation.
	Confirm bool `json:"confirm,omitempty"`
}

// NTAG424FileSettings is a file's settings as the card reports them. The
// access rights are key numbers 0 to 4, 14 for free or 15 for never.
type NTAG424FileSettings struct {
	FileType int    `json:"fileType"`
	FileSize uint32 `json:"fileSize"`

	// CommMode is "plain", "mac" or "full".
	CommMode string `json:"commMode"`

	ReadWrite int `json:"readWrite"`
	Change    int `json:"change"`
	Read      int `json:"read"`
	Write     int `json:"write"`

	SDMEnabled        bool `json:"sdmEnabled"`
	MirrorUID         bool `json:"mirrorUID,omitempty"`
	MirrorReadCounter bool `json:"mirrorReadCounter,omitempty"`
	ReadCounterLimit  bool `json:"readCounterLimit,omitempty"`
	EncryptFileData   bool `json:"encryptFileData,omitempty"`
	ASCIIEncoding     bool `json:"asciiEncoding,omitempty"`

	SDMMetaRead   int `json:"sdmMetaRead,omitempty"`
	SDMFileRead   int `json:"sdmFileRead,omitempty"`
	SDMCounterRet int `json:"sdmCounterRet,omitempty"`

	UIDOffset         uint32 `json:"uidOffset,omitempty"`
	ReadCounterOffset uint32 `json:"readCounterOffset,omitempty"`
	PICCDataOffset    uint32 `json:"piccDataOffset,omitempty"`
	MACInputOffset    uint32 `json:"macInputOffset,omitempty"`
	MACOffset         uint32 `json:"macOffset,omitempty"`
	ENCOffset         uint32 `json:"encOffset,omitempty"`
	ENCLength         uint32 `json:"encLength,omitempty"`
}

// NTAG424SDMResult is what configureSDM reports: the URL the card mirrored when
// read back, and whether it verified under the keys the agent holds.
type NTAG424SDMResult struct {
	URL string `json:"url"`

	// Verified is true when the read-back URL's MAC verified. The settings are
	// applied either way; VerifyError says why a verification failed.
	Verified    bool   `json:"verified"`
	VerifyError string `json:"verifyError,omitempty"`

	// UID and Counter are the verified tap's, when it verified. The read-back
	// counts as a tap.
	UID     string `json:"uid,omitempty"`
	Counter uint32 `json:"counter,omitempty"`
}

// NTAG424ResponsePayload answers an ntag424Request. Op echoes the request, and
// only the fields of that operation are set.
type NTAG424ResponsePayload struct {
	Op string `json:"op"`

	// FileSettings answers getFileSettings.
	FileSettings *NTAG424FileSettings `json:"fileSettings,omitempty"`

	// SDM answers configureSDM.
	SDM *NTAG424SDMResult `json:"sdm,omitempty"`

	// UID answers getCardUID: the card's real UID, uppercase hex.
	UID string `json:"uid,omitempty"`

	// KeyNo and KeyVersion answer getKeyVersion.
	KeyNo      *int `json:"keyNo,omitempty"`
	KeyVersion *int `json:"keyVersion,omitempty"`

	// Signature answers readSig: the 56-byte originality signature, base64.
	// Genuine is whether it verifies over the card's real UID under NXP's
	// public key.
	Signature string `json:"signature,omitempty"`
	Genuine   *bool  `json:"genuine,omitempty"`

	// Locked answers lock and Changed answers changeKey.
	Locked  bool `json:"locked,omitempty"`
	Changed bool `json:"changed,omitempty"`
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
