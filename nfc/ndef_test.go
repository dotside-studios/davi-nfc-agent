package nfc

import (
	"bytes"
	"testing"
)

// Test encoding and decoding of a single text record
func TestTextRecordEncodeDecode(t *testing.T) {
	text := "Hello NFC World!"
	langCode := "en"

	// Encode text record
	encoded := EncodeNdefMessageWithTextRecord(text, langCode)

	if len(encoded) == 0 {
		t.Fatal("encoded NDEF message should not be empty")
	}

	// Decode text record
	decoded, err := ParseNdefMessageForTextRecord(encoded)
	if err != nil {
		t.Fatalf("failed to decode NDEF text record: %v", err)
	}

	if decoded != text {
		t.Errorf("decoded text mismatch: got %q, want %q", decoded, text)
	}
}

// Test encoding with different language codes
func TestTextRecordLanguageCodes(t *testing.T) {
	tests := []struct {
		text     string
		langCode string
	}{
		{"Hello", "en"},
		{"Bonjour", "fr"},
		{"Hola", "es"},
		{"こんにちは", "ja"},
		{"", ""},     // Empty string
		{"Test", ""}, // Default lang code
	}

	for _, tt := range tests {
		encoded := EncodeNdefMessageWithTextRecord(tt.text, tt.langCode)
		decoded, err := ParseNdefMessageForTextRecord(encoded)
		if err != nil {
			t.Errorf("failed to decode text=%q langCode=%q: %v", tt.text, tt.langCode, err)
			continue
		}

		if decoded != tt.text {
			t.Errorf("text mismatch for langCode=%q: got %q, want %q", tt.langCode, decoded, tt.text)
		}
	}
}

// Test short vs long records
func TestTextRecordShortAndLong(t *testing.T) {
	// Short record (payload <= 255 bytes)
	shortText := "Short"
	shortEncoded := EncodeNdefMessageWithTextRecord(shortText, "en")

	// Check SR flag is set (bit 4 of header)
	if shortEncoded[0]&0x10 == 0 {
		t.Error("short record should have SR flag set")
	}

	shortDecoded, err := ParseNdefMessageForTextRecord(shortEncoded)
	if err != nil {
		t.Fatalf("failed to decode short record: %v", err)
	}
	if shortDecoded != shortText {
		t.Errorf("short record mismatch: got %q, want %q", shortDecoded, shortText)
	}

	// Long record (payload > 255 bytes)
	longText := string(make([]byte, 300)) // 300 bytes
	for i := range longText {
		longText = longText[:i] + "a"
	}
	longEncoded := EncodeNdefMessageWithTextRecord(longText, "en")

	// Check SR flag is not set
	if longEncoded[0]&0x10 != 0 {
		t.Error("long record should not have SR flag set")
	}

	longDecoded, err := ParseNdefMessageForTextRecord(longEncoded)
	if err != nil {
		t.Fatalf("failed to decode long record: %v", err)
	}
	if longDecoded != longText {
		t.Errorf("long record mismatch: length got %d, want %d", len(longDecoded), len(longText))
	}
}

// Test parseNDEFRecords with single text record
func TestParseNDEFRecordsSingleText(t *testing.T) {
	text := "Test Message"
	encoded := EncodeNdefMessageWithTextRecord(text, "en")

	records, err := parseNDEFRecords(encoded)
	if err != nil {
		t.Fatalf("failed to parse NDEF records: %v", err)
	}

	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}

	record := records[0]
	if record.TNF != 0x01 {
		t.Errorf("expected TNF=0x01 (Well Known), got 0x%02x", record.TNF)
	}

	if len(record.Type) != 1 || record.Type[0] != 'T' {
		t.Errorf("expected Type='T', got %v", record.Type)
	}

	decodedText, err := parseTextRecordPayload(record.Payload)
	if err != nil {
		t.Fatalf("failed to parse text payload: %v", err)
	}

	if decodedText != text {
		t.Errorf("text mismatch: got %q, want %q", decodedText, text)
	}
}

// Test encodeNDEFRecords with multiple records
func TestEncodeNDEFRecordsMultiple(t *testing.T) {
	textPayload := MakeTextRecordPayload("Hello", "en")
	uriPayload := MakeURIRecordPayload("https://example.com")

	records := []NDEFRecord{
		{
			TNF:     0x01,
			Type:    []byte("T"),
			Payload: textPayload,
		},
		{
			TNF:     0x01,
			Type:    []byte("U"),
			Payload: uriPayload,
		},
	}

	encoded, err := encodeNDEFRecords(records)
	if err != nil {
		t.Fatalf("failed to encode NDEF records: %v", err)
	}

	if len(encoded) == 0 {
		t.Fatal("encoded message should not be empty")
	}

	// Check MB flag on first record (bit 7)
	if encoded[0]&0x80 == 0 {
		t.Error("first record should have MB flag set")
	}

	// Parse back
	decoded, err := parseNDEFRecords(encoded)
	if err != nil {
		t.Fatalf("failed to parse encoded records: %v", err)
	}

	if len(decoded) != 2 {
		t.Fatalf("expected 2 records, got %d", len(decoded))
	}

	// Verify first record (text)
	if decoded[0].TNF != 0x01 || len(decoded[0].Type) != 1 || decoded[0].Type[0] != 'T' {
		t.Error("first record is not a text record")
	}

	// Verify second record (URI)
	if decoded[1].TNF != 0x01 || len(decoded[1].Type) != 1 || decoded[1].Type[0] != 'U' {
		t.Error("second record is not a URI record")
	}
}

// Test URI record encoding/decoding
func TestURIRecordPayload(t *testing.T) {
	tests := []string{
		"https://example.com",
		"http://test.com",
		"mailto:test@example.com",
		"tel:+1234567890",
	}

	for _, uri := range tests {
		payload := MakeURIRecordPayload(uri)
		decoded, err := parseURIRecordPayload(payload)
		if err != nil {
			t.Errorf("failed to decode URI %q: %v", uri, err)
			continue
		}

		if decoded != uri {
			t.Errorf("URI mismatch: got %q, want %q", decoded, uri)
		}
	}
}

// Test URI record with abbreviations
func TestURIRecordAbbreviations(t *testing.T) {
	tests := []struct {
		fullURI        string
		identifierCode byte
		suffix         string
	}{
		{"http://www.example.com", 0x01, "example.com"},
		{"https://www.example.com", 0x02, "example.com"},
		{"http://example.com", 0x03, "example.com"},
		{"https://example.com", 0x04, "example.com"},
	}

	for _, tt := range tests {
		// Create payload with abbreviation
		payload := make([]byte, 1+len(tt.suffix))
		payload[0] = tt.identifierCode
		copy(payload[1:], []byte(tt.suffix))

		decoded, err := parseURIRecordPayload(payload)
		if err != nil {
			t.Errorf("failed to decode abbreviated URI: %v", err)
			continue
		}

		if decoded != tt.fullURI {
			t.Errorf("URI abbreviation mismatch: got %q, want %q", decoded, tt.fullURI)
		}
	}
}

// Test empty NDEF message
func TestParseEmptyNDEF(t *testing.T) {
	_, err := parseNDEFRecords([]byte{})
	if err == nil {
		t.Error("parsing empty NDEF should return error")
	}
}

// Test malformed NDEF messages
func TestParseMalformedNDEF(t *testing.T) {
	tests := []struct {
		name string
		data []byte
	}{
		{"truncated header", []byte{}},
		{"truncated type length", []byte{0xD1}},
		{"truncated payload length", []byte{0xD1, 0x01}},
		{"truncated type", []byte{0xD1, 0x05, 0x10, 0x54}},    // Type length=5 but only 1 byte
		{"truncated payload", []byte{0xD1, 0x01, 0xFF, 0x54}}, // Payload length=255 but no payload
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseNDEFRecords(tt.data)
			if err == nil {
				t.Errorf("parsing malformed NDEF %q should return error", tt.name)
			}
		})
	}
}

// Test MakeTextRecordPayload
func TestMakeTextRecordPayload(t *testing.T) {
	payload := MakeTextRecordPayload("Hello", "en")

	// Payload format: [status byte][lang code][text]
	if len(payload) < 1 {
		t.Fatal("payload should have at least status byte")
	}

	statusByte := payload[0]
	langLength := int(statusByte & 0x3F)

	if langLength != 2 { // "en" = 2 bytes
		t.Errorf("expected language length=2, got %d", langLength)
	}

	langCode := string(payload[1 : 1+langLength])
	if langCode != "en" {
		t.Errorf("expected language code 'en', got %q", langCode)
	}

	text := string(payload[1+langLength:])
	if text != "Hello" {
		t.Errorf("expected text 'Hello', got %q", text)
	}
}

// Test that encoding and then decoding produces the same result
func TestRoundTrip(t *testing.T) {
	texts := []string{
		"Simple text",
		"Text with emoji 😀🎉",
		"Multiple\nLines\nOf\nText",
		string(make([]byte, 500)), // Long text
		"",                        // Empty
	}

	for _, text := range texts {
		// Encode
		encoded := EncodeNdefMessageWithTextRecord(text, "en")

		// Decode
		decoded, err := ParseNdefMessageForTextRecord(encoded)
		if err != nil {
			t.Errorf("round trip failed for text length %d: %v", len(text), err)
			continue
		}

		if decoded != text {
			t.Errorf("round trip mismatch: original length=%d, decoded length=%d", len(text), len(decoded))
		}
	}
}

// Test parseTextRecordPayload with UTF-16
func TestParseTextRecordPayloadUTF16(t *testing.T) {
	// UTF-16 LE encoded "Hi" (0x0048 0x0069)
	payload := []byte{
		0x82,     // Status: UTF-16 (bit 7 set), lang length = 2
		'e', 'n', // Language code
		0x48, 0x00, // 'H' in UTF-16 LE
		0x69, 0x00, // 'i' in UTF-16 LE
	}

	text, err := parseTextRecordPayload(payload)
	if err != nil {
		t.Fatalf("failed to parse UTF-16 payload: %v", err)
	}

	// Note: the function trims spaces, so we need to check trimmed result
	expected := "Hi"
	if text != expected {
		t.Errorf("UTF-16 text mismatch: got %q, want %q", text, expected)
	}
}

// Test record with ID field
func TestEncodeDecodeRecordWithID(t *testing.T) {
	record := NDEFRecord{
		TNF:     0x01,
		Type:    []byte("T"),
		ID:      []byte("test-id"),
		Payload: MakeTextRecordPayload("Hello", "en"),
	}

	encoded, err := encodeNDEFRecords([]NDEFRecord{record})
	if err != nil {
		t.Fatalf("failed to encode record with ID: %v", err)
	}

	// Check IL flag is set (bit 3)
	if encoded[0]&0x08 == 0 {
		t.Error("record with ID should have IL flag set")
	}

	decoded, err := parseNDEFRecords(encoded)
	if err != nil {
		t.Fatalf("failed to parse record with ID: %v", err)
	}

	if len(decoded) != 1 {
		t.Fatalf("expected 1 record, got %d", len(decoded))
	}

	if !bytes.Equal(decoded[0].ID, record.ID) {
		t.Errorf("ID mismatch: got %v, want %v", decoded[0].ID, record.ID)
	}
}

// Benchmark encoding
func BenchmarkEncodeTextRecord(b *testing.B) {
	text := "Hello NFC World!"
	for i := 0; i < b.N; i++ {
		_ = EncodeNdefMessageWithTextRecord(text, "en")
	}
}

// Benchmark decoding
func BenchmarkDecodeTextRecord(b *testing.B) {
	encoded := EncodeNdefMessageWithTextRecord("Hello NFC World!", "en")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = ParseNdefMessageForTextRecord(encoded)
	}
}

// Benchmark multi-record encoding
func BenchmarkEncodeMultipleRecords(b *testing.B) {
	records := []NDEFRecord{
		{TNF: 0x01, Type: []byte("T"), Payload: MakeTextRecordPayload("Hello", "en")},
		{TNF: 0x01, Type: []byte("U"), Payload: MakeURIRecordPayload("https://example.com")},
		{TNF: 0x01, Type: []byte("T"), Payload: MakeTextRecordPayload("World", "en")},
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = encodeNDEFRecords(records)
	}
}
