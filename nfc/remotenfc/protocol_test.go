package remotenfc

import (
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

func TestParseUID(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{
			name:    "colon-separated uppercase",
			input:   "04:AB:CD:EF",
			want:    "04:AB:CD:EF",
			wantErr: false,
		},
		{
			name:    "colon-separated lowercase",
			input:   "04:ab:cd:ef",
			want:    "04:AB:CD:EF",
			wantErr: false,
		},
		{
			name:    "no separator",
			input:   "04ABCDEF",
			want:    "04:AB:CD:EF",
			wantErr: false,
		},
		{
			name:    "space separator",
			input:   "04 AB CD EF",
			want:    "04:AB:CD:EF",
			wantErr: false,
		},
		{
			name:    "dash separator",
			input:   "04-AB-CD-EF",
			want:    "04:AB:CD:EF",
			wantErr: false,
		},
		{
			name:    "7-byte UID",
			input:   "04:AB:CD:12:34:56:78",
			want:    "04:AB:CD:12:34:56:78",
			wantErr: false,
		},
		{
			name:    "empty UID",
			input:   "",
			want:    "",
			wantErr: true,
		},
		{
			name:    "invalid characters",
			input:   "04:AB:XY:EF",
			want:    "",
			wantErr: true,
		},
		{
			name:    "odd number of characters",
			input:   "04ABC",
			want:    "",
			wantErr: true,
		},
		{
			name:    "too short",
			input:   "A",
			want:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseUID(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseUID() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("ParseUID() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestConvertNDEFRecordInput(t *testing.T) {
	tnf01 := uint8(0x01)
	tnf08 := uint8(0x08)

	tests := []struct {
		name    string
		input   protocol.NDEFRecordInput
		wantErr bool
	}{
		{
			name: "valid text record with TNF",
			input: protocol.NDEFRecordInput{
				TNF:        &tnf01,
				Type:       []byte("T"),
				Payload:    []byte("test payload"),
				RecordType: "text",
				Content:    "test",
			},
			wantErr: false,
		},
		{
			name: "valid URI record with TNF",
			input: protocol.NDEFRecordInput{
				TNF:        &tnf01,
				Type:       []byte("U"),
				Payload:    []byte{0x00, 'h', 't', 't', 'p', 's', ':', '/', '/', 'e', 'x', 'a', 'm', 'p', 'l', 'e', '.', 'c', 'o', 'm'},
				RecordType: "uri",
				Content:    "https://example.com",
			},
			wantErr: false,
		},
		{
			name: "invalid TNF",
			input: protocol.NDEFRecordInput{
				TNF:     &tnf08,
				Type:    []byte("T"),
				Payload: []byte("test"),
			},
			wantErr: true,
		},
		{
			name: "high-level text record",
			input: protocol.NDEFRecordInput{
				RecordType: "text",
				Content:    "Hello World",
				Language:   "en",
			},
			wantErr: false,
		},
		{
			name: "high-level URI record",
			input: protocol.NDEFRecordInput{
				RecordType: "uri",
				Content:    "https://example.com",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record, err := ConvertNDEFRecordInput(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ConvertNDEFRecordInput() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && record == nil {
				t.Error("ConvertNDEFRecordInput() returned nil record")
			}
		})
	}
}

func TestConvertNDEFInput(t *testing.T) {
	tnf01 := uint8(0x01)

	tests := []struct {
		name    string
		input   *protocol.NDEFMessageInput
		wantErr bool
	}{
		{
			name: "valid message with text record",
			input: &protocol.NDEFMessageInput{
				Records: []protocol.NDEFRecordInput{
					{
						TNF:        &tnf01,
						Type:       []byte("T"),
						Payload:    nfc.MakeTextRecordPayload("Hello", "en"),
						RecordType: "text",
						Content:    "Hello",
						Language:   "en",
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid message with multiple records",
			input: &protocol.NDEFMessageInput{
				Records: []protocol.NDEFRecordInput{
					{
						TNF:        &tnf01,
						Type:       []byte("T"),
						Payload:    nfc.MakeTextRecordPayload("Hello", "en"),
						RecordType: "text",
					},
					{
						TNF:        &tnf01,
						Type:       []byte("U"),
						Payload:    nfc.MakeURIRecordPayload("https://example.com"),
						RecordType: "uri",
					},
				},
			},
			wantErr: false,
		},
		{
			name:    "nil message",
			input:   nil,
			wantErr: true,
		},
		{
			name: "empty records",
			input: &protocol.NDEFMessageInput{
				Records: []protocol.NDEFRecordInput{},
			},
			wantErr: true,
		},
		{
			name: "high-level format only",
			input: &protocol.NDEFMessageInput{
				Records: []protocol.NDEFRecordInput{
					{
						RecordType: "text",
						Content:    "Hello",
						Language:   "en",
					},
					{
						RecordType: "uri",
						Content:    "https://example.com",
					},
				},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg, err := ConvertNDEFInput(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ConvertNDEFInput() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && msg == nil {
				t.Error("ConvertNDEFInput() returned nil message")
			}
		})
	}
}

func TestConvertTagData(t *testing.T) {
	tnf01 := uint8(0x01)

	tests := []struct {
		name    string
		input   TagData
		wantErr bool
	}{
		{
			name: "valid tag with NDEF",
			input: TagData{
				DeviceID:   "device-123",
				UID:        "04:AB:CD:EF",
				Technology: "ISO14443A",
				Type:       "MIFARE Classic 1K",
				ScannedAt:  time.Now(),
				NDEFMessage: &protocol.NDEFMessageInput{
					Records: []protocol.NDEFRecordInput{
						{
							TNF:        &tnf01,
							Type:       []byte("T"),
							Payload:    nfc.MakeTextRecordPayload("Test", "en"),
							RecordType: "text",
							Content:    "Test",
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "valid tag without NDEF",
			input: TagData{
				DeviceID:   "device-123",
				UID:        "04ABCDEF",
				Technology: "ISO14443A",
				Type:       "MIFARE Classic 1K",
				ScannedAt:  time.Now(),
				RawData:    []byte("raw data"),
			},
			wantErr: false,
		},
		{
			name: "missing UID",
			input: TagData{
				DeviceID:   "device-123",
				Technology: "ISO14443A",
				Type:       "MIFARE Classic 1K",
			},
			wantErr: true,
		},
		{
			name: "missing technology",
			input: TagData{
				DeviceID: "device-123",
				UID:      "04:AB:CD:EF",
				Type:     "MIFARE Classic 1K",
			},
			wantErr: true,
		},
		{
			name: "invalid UID format",
			input: TagData{
				DeviceID:   "device-123",
				UID:        "invalid",
				Technology: "ISO14443A",
				Type:       "MIFARE Classic 1K",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tag, err := ConvertTagData(tt.input)
			if (err != nil) != tt.wantErr {
				t.Errorf("ConvertTagData() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr {
				if tag == nil {
					t.Error("ConvertTagData() returned nil tag")
				} else {
					// Verify tag fields
					if tag.UID() != tt.input.UID {
						// UID might be normalized
						normalizedUID, _ := ParseUID(tt.input.UID)
						if tag.UID() != normalizedUID {
							t.Errorf("Tag UID = %v, want %v", tag.UID(), normalizedUID)
						}
					}
				}
			}
		})
	}
}
