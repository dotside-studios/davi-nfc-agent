package server

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// TestBuildNDEFMessage tests NDEF message building logic
func TestBuildNDEFMessage(t *testing.T) {
	tests := []struct {
		name        string
		request     WriteRequest
		expectError bool
		checkMsg    func(*testing.T, *nfc.NDEFMessage)
	}{
		{
			name: "Single text record",
			request: WriteRequest{
				Records: []WriteRecord{
					{Type: "text", Content: "Hello, NFC!"},
				},
			},
			expectError: false,
			checkMsg: func(t *testing.T, msg *nfc.NDEFMessage) {
				records := msg.Records()
				if len(records) != 1 {
					t.Errorf("Expected 1 record, got %d", len(records))
				}
				text, _ := records[0].GetText()
				if text != "Hello, NFC!" {
					t.Errorf("Expected 'Hello, NFC!', got '%s'", text)
				}
			},
		},
		{
			name: "Multiple records",
			request: WriteRequest{
				Records: []WriteRecord{
					{Type: "text", Content: "First"},
					{Type: "text", Content: "Second"},
				},
			},
			expectError: false,
			checkMsg: func(t *testing.T, msg *nfc.NDEFMessage) {
				records := msg.Records()
				if len(records) != 2 {
					t.Errorf("Expected 2 records, got %d", len(records))
				}
			},
		},
		{
			name: "URI record",
			request: WriteRequest{
				Records: []WriteRecord{
					{Type: "uri", Content: "https://example.com"},
				},
			},
			expectError: false,
			checkMsg: func(t *testing.T, msg *nfc.NDEFMessage) {
				records := msg.Records()
				if len(records) != 1 {
					t.Errorf("Expected 1 record, got %d", len(records))
				}
				uri, _ := records[0].GetURI()
				if uri != "https://example.com" {
					t.Errorf("Expected 'https://example.com', got '%s'", uri)
				}
			},
		},
		{
			name: "Mixed record types",
			request: WriteRequest{
				Records: []WriteRecord{
					{Type: "text", Content: "Hello"},
					{Type: "uri", Content: "https://example.com"},
				},
			},
			expectError: false,
			checkMsg: func(t *testing.T, msg *nfc.NDEFMessage) {
				records := msg.Records()
				if len(records) != 2 {
					t.Errorf("Expected 2 records, got %d", len(records))
				}
			},
		},
		{
			name: "Unsupported record type",
			request: WriteRequest{
				Records: []WriteRecord{
					{Type: "unknown", Content: "test"},
				},
			},
			expectError: true,
		},
		{
			name: "Empty records array",
			request: WriteRequest{
				Records: []WriteRecord{},
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := BuildNDEFMessage(tt.request)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if msg == nil {
				t.Fatal("Expected NDEF message, got nil")
			}

			if tt.checkMsg != nil {
				tt.checkMsg(t, msg)
			}
		})
	}
}

func tnfPtr(v uint8) *uint8 { return &v }

// TestBuildNDEFMessage_RecordTypes covers the record types beyond text/uri
// that the write API now supports.
func TestBuildNDEFMessage_RecordTypes(t *testing.T) {
	tests := []struct {
		name        string
		record      WriteRecord
		expectError bool
		check       func(*testing.T, nfc.NDEFRecord)
	}{
		{
			name:   "url alias",
			record: WriteRecord{Type: "url", Content: "https://x.com"},
			check: func(t *testing.T, r nfc.NDEFRecord) {
				if uri, ok := r.GetURI(); !ok || uri != "https://x.com" {
					t.Errorf("got ok=%v uri=%q", ok, uri)
				}
			},
		},
		{
			name:   "email scheme prepended",
			record: WriteRecord{Type: "email", Content: "a@b.com"},
			check: func(t *testing.T, r nfc.NDEFRecord) {
				if uri, _ := r.GetURI(); uri != "mailto:a@b.com" {
					t.Errorf("got %q", uri)
				}
			},
		},
		{
			name:   "tel scheme not double-prefixed",
			record: WriteRecord{Type: "tel", Content: "tel:+1555"},
			check: func(t *testing.T, r nfc.NDEFRecord) {
				if uri, _ := r.GetURI(); uri != "tel:+1555" {
					t.Errorf("got %q", uri)
				}
			},
		},
		{
			name:   "mime record",
			record: WriteRecord{Type: "mime", MimeType: "application/json", Payload: []byte(`{"a":1}`)},
			check: func(t *testing.T, r nfc.NDEFRecord) {
				if r.TNF != 0x02 || string(r.Type) != "application/json" {
					t.Errorf("got TNF=0x%02X type=%q", r.TNF, string(r.Type))
				}
				if string(r.Payload) != `{"a":1}` {
					t.Errorf("payload %q", string(r.Payload))
				}
			},
		},
		{
			name:   "vcard record",
			record: WriteRecord{Type: "vcard", Content: "BEGIN:VCARD\nEND:VCARD"},
			check: func(t *testing.T, r nfc.NDEFRecord) {
				if r.TNF != 0x02 || string(r.Type) != "text/vcard" {
					t.Errorf("got TNF=0x%02X type=%q", r.TNF, string(r.Type))
				}
			},
		},
		{
			name:   "aar record",
			record: WriteRecord{Type: "aar", Content: "com.example.app"},
			check: func(t *testing.T, r nfc.NDEFRecord) {
				if r.TNF != 0x04 || string(r.Type) != "android.com:pkg" {
					t.Errorf("got TNF=0x%02X type=%q", r.TNF, string(r.Type))
				}
				if string(r.Payload) != "com.example.app" {
					t.Errorf("payload %q", string(r.Payload))
				}
			},
		},
		{
			name:   "smartposter record",
			record: WriteRecord{Type: "smartposter", Content: "https://example.com", Title: "Hi"},
			check: func(t *testing.T, r nfc.NDEFRecord) {
				if string(r.Type) != "Sp" {
					t.Fatalf("got type %q", string(r.Type))
				}
				inner, err := nfc.DecodeNDEF(r.Payload)
				if err != nil {
					t.Fatalf("decode Sp payload: %v", err)
				}
				if uri, _ := inner.GetURI(); uri != "https://example.com" {
					t.Errorf("sp uri %q", uri)
				}
				if text, _ := inner.GetText(); text != "Hi" {
					t.Errorf("sp title %q", text)
				}
			},
		},
		{
			name:   "empty record",
			record: WriteRecord{Type: "empty"},
			check: func(t *testing.T, r nfc.NDEFRecord) {
				if r.TNF != 0x00 {
					t.Errorf("expected empty record TNF 0x00, got 0x%02X", r.TNF)
				}
			},
		},
		{
			name:   "raw record",
			record: WriteRecord{Type: "raw", TNF: tnfPtr(0x02), TypeBytes: []byte("x/y"), Payload: []byte("z")},
			check: func(t *testing.T, r nfc.NDEFRecord) {
				if r.TNF != 0x02 || string(r.Type) != "x/y" || string(r.Payload) != "z" {
					t.Errorf("got TNF=0x%02X type=%q payload=%q", r.TNF, string(r.Type), string(r.Payload))
				}
			},
		},
		{
			name:        "raw without tnf errors",
			record:      WriteRecord{Type: "raw", Payload: []byte("z")},
			expectError: true,
		},
		{
			name:        "uri without content errors",
			record:      WriteRecord{Type: "uri"},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := BuildNDEFMessage(WriteRequest{Records: []WriteRecord{tt.record}})
			if tt.expectError {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			records := msg.Records()
			if len(records) != 1 {
				t.Fatalf("expected 1 record, got %d", len(records))
			}
			if tt.check != nil {
				tt.check(t, records[0])
			}
		})
	}
}
