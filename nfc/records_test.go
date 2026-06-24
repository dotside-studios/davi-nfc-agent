package nfc

import "testing"

// TestMakeURIRecordPayload_PrefixOptimization verifies the encoder picks the
// longest matching NFC Forum abbreviation prefix and that every result decodes
// back to the original URI.
func TestMakeURIRecordPayload_PrefixOptimization(t *testing.T) {
	cases := []struct {
		uri        string
		wantCode   byte
		wantSuffix string
	}{
		{"https://www.example.com", 0x02, "example.com"},
		{"http://www.example.com", 0x01, "example.com"},
		{"https://example.com", 0x04, "example.com"},
		{"http://example.com", 0x03, "example.com"},
		{"tel:+15551234567", 0x05, "+15551234567"},
		{"mailto:a@b.com", 0x06, "a@b.com"},
		{"ftp://example.com/x", 0x0D, "example.com/x"},
		{"urn:epc:id:foo", 0x1E, "foo"},
		{"customscheme://host", 0x00, "customscheme://host"},
	}

	for _, c := range cases {
		payload := MakeURIRecordPayload(c.uri)
		if payload[0] != c.wantCode {
			t.Errorf("%q: got identifier code 0x%02X, want 0x%02X", c.uri, payload[0], c.wantCode)
		}
		if string(payload[1:]) != c.wantSuffix {
			t.Errorf("%q: got suffix %q, want %q", c.uri, string(payload[1:]), c.wantSuffix)
		}

		decoded, err := parseURIRecordPayload(payload)
		if err != nil {
			t.Errorf("%q: decode error: %v", c.uri, err)
			continue
		}
		if decoded != c.uri {
			t.Errorf("%q: round-trip mismatch, got %q", c.uri, decoded)
		}
	}
}

// TestNDEFSmartPoster_EncodeDecode verifies a Smart Poster encodes as an "Sp"
// record whose nested message carries the URI and title.
func TestNDEFSmartPoster_EncodeDecode(t *testing.T) {
	sp := &NDEFSmartPoster{URI: "https://example.com", Title: "Example", Language: "en"}
	record := sp.ToRecord()

	if record.TNF != 0x01 || string(record.Type) != "Sp" {
		t.Fatalf("unexpected smartposter header: TNF=0x%02X Type=%q", record.TNF, string(record.Type))
	}

	inner, err := parseNDEFRecords(record.Payload)
	if err != nil {
		t.Fatalf("failed to parse smartposter payload: %v", err)
	}

	var gotURI, gotTitle string
	for _, r := range inner {
		if uri, ok := r.GetURI(); ok {
			gotURI = uri
		}
		if text, ok := r.GetText(); ok {
			gotTitle = text
		}
	}
	if gotURI != "https://example.com" {
		t.Errorf("smartposter URI: got %q", gotURI)
	}
	if gotTitle != "Example" {
		t.Errorf("smartposter title: got %q", gotTitle)
	}
}

// TestNDEFSmartPoster_NoTitle verifies the title record is omitted when empty.
func TestNDEFSmartPoster_NoTitle(t *testing.T) {
	record := (&NDEFSmartPoster{URI: "https://example.com"}).ToRecord()
	inner, err := parseNDEFRecords(record.Payload)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(inner) != 1 {
		t.Fatalf("expected 1 inner record (URI only), got %d", len(inner))
	}
	if uri, ok := inner[0].GetURI(); !ok || uri != "https://example.com" {
		t.Errorf("expected URI record, got ok=%v uri=%q", ok, uri)
	}
}

// TestNDEFRaw_ToRecord verifies raw record passthrough and TNF masking.
func TestNDEFRaw_ToRecord(t *testing.T) {
	record := (&NDEFRaw{
		TNF:     0x02,
		Type:    []byte("application/json"),
		ID:      []byte("id1"),
		Payload: []byte(`{"a":1}`),
	}).ToRecord()

	if record.TNF != 0x02 {
		t.Errorf("TNF: got 0x%02X", record.TNF)
	}
	if string(record.Type) != "application/json" {
		t.Errorf("Type: got %q", string(record.Type))
	}
	if string(record.ID) != "id1" {
		t.Errorf("ID: got %q", string(record.ID))
	}
	if string(record.Payload) != `{"a":1}` {
		t.Errorf("Payload: got %q", string(record.Payload))
	}

	// TNF is masked to its valid 3-bit range.
	if masked := (&NDEFRaw{TNF: 0xF2}).ToRecord(); masked.TNF != 0x02 {
		t.Errorf("expected masked TNF 0x02, got 0x%02X", masked.TNF)
	}
}
