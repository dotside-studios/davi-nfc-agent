package server

import (
	"fmt"
	"log"
	"strings"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// WriteRecord represents a single NDEF record in a write request. The Type
// field selects how the remaining fields are interpreted.
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

// WriteRequest represents a request to write data to an NFC card.
// This API follows the "overwrite" approach - clients send the complete
// NDEF message to write. To append, clients should read current data,
// modify it, and send back the complete message.
type WriteRequest struct {
	// Records is an array of NDEF records to write
	Records []WriteRecord `json:"records"`

	// Lock, when true, makes the tag permanently read-only after a successful
	// write. Only tags that support locking honor this. WARNING: irreversible.
	Lock bool `json:"lock,omitempty"`
}

// BuildNDEFMessage builds an NDEF message from the request.
// This always creates a complete NDEF message that will overwrite the card.
func BuildNDEFMessage(writeReq WriteRequest) (*nfc.NDEFMessage, error) {
	if len(writeReq.Records) == 0 {
		return nil, fmt.Errorf("no records provided in write request")
	}

	recordBuilders := make([]nfc.NDEFRecordBuilder, 0, len(writeReq.Records))
	for i, record := range writeReq.Records {
		builder, err := buildRecord(record)
		if err != nil {
			return nil, fmt.Errorf("record %d: %w", i, err)
		}
		recordBuilders = append(recordBuilders, builder)
	}

	// Build complete NDEF message
	ndefMsg, err := (&nfc.NDEFMessageBuilder{Records: recordBuilders}).Build()
	if err != nil {
		return nil, fmt.Errorf("failed to build NDEF message: %w", err)
	}

	log.Printf("WriteRequest: Writing %d NDEF record(s) (complete overwrite)", len(recordBuilders))
	return ndefMsg, nil
}

// buildRecord maps a single WriteRecord to an nfc record builder based on its
// Type. It is the single place new record types are wired into the write API.
func buildRecord(record WriteRecord) (nfc.NDEFRecordBuilder, error) {
	switch strings.ToLower(strings.TrimSpace(record.Type)) {
	case "", "text":
		language := record.Language
		if language == "" {
			language = "en"
		}
		return &nfc.NDEFText{Content: record.Content, Language: language}, nil

	case "uri", "url":
		if record.Content == "" {
			return nil, fmt.Errorf("uri record requires content")
		}
		return &nfc.NDEFURI{Content: record.Content}, nil

	case "mailto", "email":
		return uriWithScheme("mailto:", record.Content)
	case "tel":
		return uriWithScheme("tel:", record.Content)
	case "sms":
		return uriWithScheme("sms:", record.Content)
	case "geo":
		return uriWithScheme("geo:", record.Content)

	case "smartposter", "smart-poster", "sp":
		if record.Content == "" {
			return nil, fmt.Errorf("smartposter record requires a URI in content")
		}
		return &nfc.NDEFSmartPoster{URI: record.Content, Title: record.Title, Language: record.Language}, nil

	case "mime":
		mimeType := record.MimeType
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		data := record.Payload
		if len(data) == 0 && record.Content != "" {
			data = []byte(record.Content)
		}
		return &nfc.NDEFMIME{Type: mimeType, Data: data}, nil

	case "vcard", "vcf":
		data := record.Payload
		if len(data) == 0 {
			data = []byte(record.Content)
		}
		if len(data) == 0 {
			return nil, fmt.Errorf("vcard record requires content or payload")
		}
		return &nfc.NDEFMIME{Type: "text/vcard", Data: data}, nil

	case "external":
		if record.Content == "" {
			return nil, fmt.Errorf("external record requires a domain:type in content")
		}
		return &nfc.NDEFExternal{Domain: record.Content, Data: record.Payload}, nil

	case "aar", "android":
		if record.Content == "" {
			return nil, fmt.Errorf("aar record requires an Android package name in content")
		}
		// Android Application Record: external type "android.com:pkg" whose
		// payload is the package name.
		return &nfc.NDEFExternal{Domain: "android.com:pkg", Data: []byte(record.Content)}, nil

	case "raw", "custom":
		if record.TNF == nil {
			return nil, fmt.Errorf("raw record requires tnf")
		}
		if *record.TNF > 0x07 {
			return nil, fmt.Errorf("invalid tnf value: 0x%02X", *record.TNF)
		}
		return &nfc.NDEFRaw{TNF: *record.TNF, Type: record.TypeBytes, ID: record.ID, Payload: record.Payload}, nil

	default:
		return nil, fmt.Errorf("unsupported record type '%s'", record.Type)
	}
}

// uriWithScheme builds a URI record, prepending scheme when the content does
// not already start with it (e.g. "tel:" for a bare phone number).
func uriWithScheme(scheme, content string) (nfc.NDEFRecordBuilder, error) {
	if content == "" {
		return nil, fmt.Errorf("%s record requires content", strings.TrimSuffix(scheme, ":"))
	}
	if !strings.HasPrefix(strings.ToLower(content), scheme) {
		content = scheme + content
	}
	return &nfc.NDEFURI{Content: content}, nil
}

// HandleWriteRequest processes a write request and performs the NFC write operation.
// This always performs a complete overwrite of the NDEF message on the card.
// It returns a WriteResult describing the verified outcome of the write.
func HandleWriteRequest(reader *nfc.NFCReader, writeReq WriteRequest) (*nfc.WriteResult, error) {
	// Build complete NDEF message
	ndefMsg, err := BuildNDEFMessage(writeReq)
	if err != nil {
		return nil, fmt.Errorf("failed to build NDEF message: %w", err)
	}

	// Write with overwrite option (complete replacement)
	result, err := reader.WriteMessageWithResult(ndefMsg, nfc.WriteOptions{
		Overwrite: true,
		Index:     -1,
		Lock:      writeReq.Lock,
	})
	if err != nil {
		return nil, fmt.Errorf("write failed: %w", err)
	}

	log.Printf("WriteRequest: Successfully wrote NDEF message to card (verified=%v, attempts=%d)",
		result.Verified, result.Attempts)
	return result, nil
}
