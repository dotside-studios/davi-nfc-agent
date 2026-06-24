package nfc

import (
	"fmt"
	"strings"
)

// Message represents data that can be written to/read from a card.
// Different implementations handle different encoding schemes.
type Message interface {
	// Encode converts the message to bytes for writing to card
	Encode() ([]byte, error)

	// Type returns the message type for debugging
	Type() string
}

// TextMessage represents raw bytes from cards that don't support NDEF.
// This is a fallback message type that stores both the raw data and decoded text.
type TextMessage struct {
	Data []byte // Raw bytes from the card
	Text string // Decoded text representation
}

// NewTextMessage creates a new text message from raw bytes.
// It automatically decodes the bytes to a string.
func NewTextMessage(data []byte) *TextMessage {
	return &TextMessage{
		Data: data,
		Text: string(data),
	}
}

// NewTextMessageFromString creates a new text message from a string.
func NewTextMessageFromString(text string) *TextMessage {
	return &TextMessage{
		Data: []byte(text),
		Text: text,
	}
}

// Encode returns the raw bytes as-is (no encoding).
func (t *TextMessage) Encode() ([]byte, error) {
	return t.Data, nil
}

// Type returns "raw" for debugging.
func (t *TextMessage) Type() string {
	return "raw"
}

// String returns the decoded text.
func (t *TextMessage) String() string {
	return t.Text
}

// Bytes returns the raw bytes.
func (t *TextMessage) Bytes() []byte {
	return t.Data
}

// NDEFMessage represents a structured NDEF message with multiple records.
// This allows complex messages with multiple record types (text, URI, MIME, etc.)
type NDEFMessage struct {
	records []NDEFRecord
}

// NDEFRecord represents a single NDEF record within a message.
type NDEFRecord struct {
	TNF     byte   // Type Name Format (0x00-0x07)
	Type    []byte // Record type (e.g., "T" for text, "U" for URI)
	ID      []byte // Optional record ID
	Payload []byte // Record payload data
}

// GetText extracts text from a Text Record (TNF=0x01, Type='T').
// Returns (text, true) if this is a text record, or ("", false) otherwise.
func (r *NDEFRecord) GetText() (string, bool) {
	if !r.IsTextRecord() {
		return "", false
	}
	text, err := parseTextRecordPayload(r.Payload)
	if err != nil {
		return "", false
	}
	return text, true
}

// GetURI extracts URI from a URI Record (TNF=0x01, Type='U').
// Returns (uri, true) if this is a URI record, or ("", false) otherwise.
func (r *NDEFRecord) GetURI() (string, bool) {
	if !r.IsURIRecord() {
		return "", false
	}
	uri, err := parseURIRecordPayload(r.Payload)
	if err != nil {
		return "", false
	}
	return uri, true
}

// IsTextRecord returns true if this is a Text Record.
func (r *NDEFRecord) IsTextRecord() bool {
	return r.TNF == 0x01 && len(r.Type) == 1 && r.Type[0] == 'T'
}

// IsURIRecord returns true if this is a URI Record.
func (r *NDEFRecord) IsURIRecord() bool {
	return r.TNF == 0x01 && len(r.Type) == 1 && r.Type[0] == 'U'
}

// NewNDEFMessage creates a new empty NDEF message.
func NewNDEFMessage() *NDEFMessage {
	return &NDEFMessage{records: []NDEFRecord{}}
}

// AddRecord adds a raw NDEF record to the message.
func (m *NDEFMessage) AddRecord(record NDEFRecord) *NDEFMessage {
	m.records = append(m.records, record)
	return m
}

// AddText adds an NDEF Text Record to the message.
func (m *NDEFMessage) AddText(text, langCode string) *NDEFMessage {
	if langCode == "" {
		langCode = "en"
	}
	payload := MakeTextRecordPayload(text, langCode)
	m.records = append(m.records, NDEFRecord{
		TNF:     0x01, // Well Known
		Type:    []byte("T"),
		Payload: payload,
	})
	return m
}

// AddURI adds an NDEF URI Record to the message.
func (m *NDEFMessage) AddURI(uri string) *NDEFMessage {
	payload := MakeURIRecordPayload(uri)
	m.records = append(m.records, NDEFRecord{
		TNF:     0x01, // Well Known
		Type:    []byte("U"),
		Payload: payload,
	})
	return m
}

// Encode converts the NDEF message to bytes.
func (m *NDEFMessage) Encode() ([]byte, error) {
	if len(m.records) == 0 {
		return nil, fmt.Errorf("cannot encode empty NDEF message")
	}
	return encodeNDEFRecords(m.records)
}

// Type returns "ndef" for debugging.
func (m *NDEFMessage) Type() string {
	return "ndef"
}

// Records returns the list of NDEF records in this message.
func (m *NDEFMessage) Records() []NDEFRecord {
	return m.records
}

// GetText returns the text content from the first Text Record in the message.
func (m *NDEFMessage) GetText() (string, error) {
	for _, r := range m.records {
		if text, ok := r.GetText(); ok {
			return text, nil
		}
	}
	return "", fmt.Errorf("no text record found in NDEF message")
}

// GetURI returns the URI from the first URI Record in the message.
func (m *NDEFMessage) GetURI() (string, error) {
	for _, r := range m.records {
		if uri, ok := r.GetURI(); ok {
			return uri, nil
		}
	}
	return "", fmt.Errorf("no URI record found in NDEF message")
}

// DecodeNDEF parses raw bytes into an NDEFMessage.
// Returns error if the data is not valid NDEF format.
func DecodeNDEF(data []byte) (*NDEFMessage, error) {
	records, err := parseNDEFRecords(data)
	if err != nil {
		return nil, err
	}
	return &NDEFMessage{records: records}, nil
}

// DecodeText creates a TextMessage from raw bytes (no parsing).
// This is used for cards that don't support NDEF.
func DecodeText(data []byte) *TextMessage {
	return NewTextMessage(data)
}

// uriPrefixes is the NFC Forum URI Record Type Definition (RTD) abbreviation
// table. The slice index is the identifier code stored as the first payload
// byte; the value is the prefix it expands to. Index 0 is the empty
// (no-abbreviation) prefix.
var uriPrefixes = []string{
	"",                           // 0x00
	"http://www.",                // 0x01
	"https://www.",               // 0x02
	"http://",                    // 0x03
	"https://",                   // 0x04
	"tel:",                       // 0x05
	"mailto:",                    // 0x06
	"ftp://anonymous:anonymous@", // 0x07
	"ftp://ftp.",                 // 0x08
	"ftps://",                    // 0x09
	"sftp://",                    // 0x0A
	"smb://",                     // 0x0B
	"nfs://",                     // 0x0C
	"ftp://",                     // 0x0D
	"dav://",                     // 0x0E
	"news:",                      // 0x0F
	"telnet://",                  // 0x10
	"imap:",                      // 0x11
	"rtsp://",                    // 0x12
	"urn:",                       // 0x13
	"pop:",                       // 0x14
	"sip:",                       // 0x15
	"sips:",                      // 0x16
	"tftp:",                      // 0x17
	"btspp://",                   // 0x18
	"btl2cap://",                 // 0x19
	"btgoep://",                  // 0x1A
	"tcpobex://",                 // 0x1B
	"irdaobex://",                // 0x1C
	"file://",                    // 0x1D
	"urn:epc:id:",                // 0x1E
	"urn:epc:tag:",               // 0x1F
	"urn:epc:pat:",               // 0x20
	"urn:epc:raw:",               // 0x21
	"urn:epc:",                   // 0x22
	"urn:nfc:",                   // 0x23
}

// MakeURIRecordPayload creates the payload for an NDEF URI record. It selects
// the longest matching NFC Forum abbreviation prefix to minimize the bytes
// written to the tag (URI capacity is scarce on small tags).
func MakeURIRecordPayload(uri string) []byte {
	bestCode := byte(0)
	bestLen := 0
	// Skip index 0 (empty prefix); find the longest prefix the URI starts with.
	for code := 1; code < len(uriPrefixes); code++ {
		p := uriPrefixes[code]
		if len(p) > bestLen && strings.HasPrefix(uri, p) {
			bestCode = byte(code)
			bestLen = len(p)
		}
	}

	suffix := uri[bestLen:]
	payload := make([]byte, 1+len(suffix))
	payload[0] = bestCode
	copy(payload[1:], suffix)
	return payload
}

// parseURIRecordPayload extracts the URI from an NDEF URI record payload,
// expanding the NFC Forum abbreviation prefix. Identifier codes outside the
// known table are treated as no prefix (forward-compatible).
func parseURIRecordPayload(payload []byte) (string, error) {
	if len(payload) < 1 {
		return "", fmt.Errorf("URI record payload too short")
	}

	prefix := ""
	if code := int(payload[0]); code > 0 && code < len(uriPrefixes) {
		prefix = uriPrefixes[code]
	}

	return prefix + string(payload[1:]), nil
}

// High-level record types for declarative message construction

// NDEFText represents a high-level text record.
//
// Example:
//
//	msg := &nfc.NDEFMessageBuilder{
//	    Records: []nfc.NDEFRecordBuilder{
//	        &nfc.NDEFText{Content: "Hello World", Language: "en"},
//	        &nfc.NDEFURI{Content: "https://example.com"},
//	    },
//	}
type NDEFText struct {
	Content  string
	Language string // Optional, defaults to "en"
}

// ToRecord converts NDEFText to NDEFRecord.
func (t *NDEFText) ToRecord() NDEFRecord {
	lang := t.Language
	if lang == "" {
		lang = "en"
	}
	return NDEFRecord{
		TNF:     0x01, // Well Known
		Type:    []byte("T"),
		Payload: MakeTextRecordPayload(t.Content, lang),
	}
}

// NDEFURI represents a high-level URI record.
type NDEFURI struct {
	Content string
}

// ToRecord converts NDEFURI to NDEFRecord.
func (u *NDEFURI) ToRecord() NDEFRecord {
	return NDEFRecord{
		TNF:     0x01, // Well Known
		Type:    []byte("U"),
		Payload: MakeURIRecordPayload(u.Content),
	}
}

// NDEFMIME represents a high-level MIME type record.
type NDEFMIME struct {
	Type string
	Data []byte
}

// ToRecord converts NDEFMIME to NDEFRecord.
func (m *NDEFMIME) ToRecord() NDEFRecord {
	return NDEFRecord{
		TNF:     0x02, // MIME Media Type
		Type:    []byte(m.Type),
		Payload: m.Data,
	}
}

// NDEFExternal represents a high-level external type record.
type NDEFExternal struct {
	Domain string // e.g., "example.com:myapp"
	Data   []byte
}

// ToRecord converts NDEFExternal to NDEFRecord.
func (e *NDEFExternal) ToRecord() NDEFRecord {
	return NDEFRecord{
		TNF:     0x04, // External Type
		Type:    []byte(e.Domain),
		Payload: e.Data,
	}
}

// NDEFSmartPoster represents a high-level Smart Poster record: a URI with an
// optional human-readable title. It encodes as a Well Known "Sp" record whose
// payload is a nested NDEF message containing an optional Title (Text) record
// and a mandatory URI record. This is the most common "tap to open <label>"
// tag and is widely understood by phones.
type NDEFSmartPoster struct {
	URI      string
	Title    string // Optional display title
	Language string // Optional, defaults to "en" when Title is set
}

// ToRecord converts NDEFSmartPoster to NDEFRecord.
func (s *NDEFSmartPoster) ToRecord() NDEFRecord {
	var records []NDEFRecord
	if s.Title != "" {
		lang := s.Language
		if lang == "" {
			lang = "en"
		}
		records = append(records, NDEFRecord{
			TNF:     0x01, // Well Known
			Type:    []byte("T"),
			Payload: MakeTextRecordPayload(s.Title, lang),
		})
	}
	records = append(records, NDEFRecord{
		TNF:     0x01, // Well Known
		Type:    []byte("U"),
		Payload: MakeURIRecordPayload(s.URI),
	})

	// encodeNDEFRecords only errors on an empty slice; records always contains
	// the URI record, so the error is unreachable here.
	payload, _ := encodeNDEFRecords(records)
	return NDEFRecord{
		TNF:     0x01, // Well Known
		Type:    []byte("Sp"),
		Payload: payload,
	}
}

// NDEFRaw represents a fully specified NDEF record for advanced or custom use
// cases where the caller provides the TNF, type, optional ID, and payload
// directly (e.g. proprietary external types or non-NDEF-Forum records).
type NDEFRaw struct {
	TNF     uint8
	Type    []byte
	ID      []byte
	Payload []byte
}

// ToRecord converts NDEFRaw to NDEFRecord. The TNF is masked to its valid
// 3-bit range.
func (r *NDEFRaw) ToRecord() NDEFRecord {
	return NDEFRecord{
		TNF:     r.TNF & 0x07,
		Type:    r.Type,
		ID:      r.ID,
		Payload: r.Payload,
	}
}

// NDEFEmpty represents a high-level empty record.
type NDEFEmpty struct{}

// ToRecord converts NDEFEmpty to NDEFRecord.
func (e *NDEFEmpty) ToRecord() NDEFRecord {
	return NDEFRecord{
		TNF:     0x00, // Empty
		Type:    nil,
		Payload: nil,
	}
}

// NDEFRecordBuilder is an interface that can be converted to NDEFRecord.
type NDEFRecordBuilder interface {
	ToRecord() NDEFRecord
}

// NDEFMessageBuilder provides a declarative way to construct NDEF messages.
//
// Example:
//
//	msg := &nfc.NDEFMessageBuilder{
//	    Records: []nfc.NDEFRecordBuilder{
//	        &nfc.NDEFText{Content: "Hello World", Language: "en"},
//	        &nfc.NDEFURI{Content: "https://example.com"},
//	    },
//	}.Build()
type NDEFMessageBuilder struct {
	Records []NDEFRecordBuilder
}

func (b *NDEFMessageBuilder) Encode() ([]byte, error) {
	msg, err := b.Build()
	if err != nil {
		return nil, err
	}
	return msg.Encode()
}

func (b *NDEFMessageBuilder) Type() string {
	return "ndef"
}

// Build transforms the high-level records into a low-level NDEFMessage.
func (b *NDEFMessageBuilder) Build() (*NDEFMessage, error) {
	if len(b.Records) == 0 {
		return nil, fmt.Errorf("cannot build empty NDEF message (no records provided)")
	}

	msg := NewNDEFMessage()
	for _, record := range b.Records {
		msg.AddRecord(record.ToRecord())
	}
	return msg, nil
}

// MustBuild is like Build but panics on error.
func (b *NDEFMessageBuilder) MustBuild() *NDEFMessage {
	msg, err := b.Build()
	if err != nil {
		panic(fmt.Sprintf("NDEFMessageBuilder.MustBuild: %v", err))
	}
	return msg
}

// ToBuilder converts a low-level NDEFMessage into a high-level NDEFMessageBuilder.
// This allows editing existing messages in a declarative way.
//
// Example:
//
//	// Read existing message
//	msg, _ := card.ReadMessage()
//	ndefMsg := msg.(*nfc.NDEFMessage)
//
//	// Convert to builder for editing
//	builder := ndefMsg.ToBuilder()
//	builder.Records = append(builder.Records, &nfc.NDEFText{Content: "New text"})
//
//	// Build and write back
//	updated := builder.MustBuild()
//	card.WriteMessage(updated)
func (m *NDEFMessage) ToBuilder() *NDEFMessageBuilder {
	builders := make([]NDEFRecordBuilder, 0, len(m.records))

	for _, record := range m.records {
		// Convert each NDEFRecord to its high-level equivalent
		builder := recordToBuilder(record)
		if builder != nil {
			builders = append(builders, builder)
		}
	}

	return &NDEFMessageBuilder{
		Records: builders,
	}
}

// recordToBuilder converts a low-level NDEFRecord to a high-level builder.
// Returns nil for unrecognized record types.
func recordToBuilder(record NDEFRecord) NDEFRecordBuilder {
	switch record.TNF {
	case 0x00: // Empty
		return &NDEFEmpty{}

	case 0x01: // Well Known
		if len(record.Type) == 1 {
			switch record.Type[0] {
			case 'T': // Text Record
				text, err := parseTextRecordPayload(record.Payload)
				if err != nil {
					return nil
				}
				// Try to extract language (first byte of payload has status + lang length)
				lang := "en" // default
				if len(record.Payload) > 0 {
					statusByte := record.Payload[0]
					langLen := int(statusByte & 0x3F) // Lower 6 bits
					if langLen > 0 && len(record.Payload) > 1+langLen {
						lang = string(record.Payload[1 : 1+langLen])
					}
				}
				return &NDEFText{
					Content:  text,
					Language: lang,
				}

			case 'U': // URI Record
				uri, err := parseURIRecordPayload(record.Payload)
				if err != nil {
					return nil
				}
				return &NDEFURI{
					Content: uri,
				}
			}
		}

	case 0x02: // MIME Media Type
		return &NDEFMIME{
			Type: string(record.Type),
			Data: record.Payload,
		}

	case 0x04: // External Type
		return &NDEFExternal{
			Domain: string(record.Type),
			Data:   record.Payload,
		}
	}

	// Unknown record type - return nil
	return nil
}
