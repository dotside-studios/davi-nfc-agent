package protocol

import (
	"fmt"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// ConvertNDEFInput converts protocol NDEF format to internal nfc.NDEFMessage.
func ConvertNDEFInput(data *NDEFMessageInput) (*nfc.NDEFMessage, error) {
	if data == nil || len(data.Records) == 0 {
		return nil, fmt.Errorf("empty NDEF message")
	}

	msg := nfc.NewNDEFMessage()
	for i, recordData := range data.Records {
		record, err := ConvertNDEFRecordInput(recordData)
		if err != nil {
			return nil, fmt.Errorf("failed to convert record %d: %w", i, err)
		}
		msg.AddRecord(*record)
	}

	return msg, nil
}

// ConvertNDEFRecordInput converts protocol NDEF record to internal nfc.NDEFRecord.
func ConvertNDEFRecordInput(data NDEFRecordInput) (*nfc.NDEFRecord, error) {
	// If TNF is provided (low-level format), use it directly
	if data.TNF != nil {
		if *data.TNF > 0x07 {
			return nil, fmt.Errorf("invalid TNF value: 0x%02X", *data.TNF)
		}
		return &nfc.NDEFRecord{
			TNF:     *data.TNF,
			Type:    data.Type,
			ID:      data.ID,
			Payload: data.Payload,
		}, nil
	}

	// High-level format - convert based on RecordType
	switch data.RecordType {
	case "text", "":
		if data.Content == "" && data.RecordType == "" {
			return nil, fmt.Errorf("text record requires content")
		}
		lang := data.Language
		if lang == "" {
			lang = "en"
		}
		text := &nfc.NDEFText{Content: data.Content, Language: lang}
		record := text.ToRecord()
		return &record, nil

	case "uri":
		if data.Content == "" {
			return nil, fmt.Errorf("URI record requires content")
		}
		uri := &nfc.NDEFURI{Content: data.Content}
		record := uri.ToRecord()
		return &record, nil

	case "mime":
		mimeType := data.MimeType
		if mimeType == "" {
			mimeType = "application/octet-stream"
		}
		mime := &nfc.NDEFMIME{Type: mimeType, Data: data.Payload}
		record := mime.ToRecord()
		return &record, nil

	case "external":
		if data.Content == "" {
			return nil, fmt.Errorf("external record requires domain in content field")
		}
		ext := &nfc.NDEFExternal{Domain: data.Content, Data: data.Payload}
		record := ext.ToRecord()
		return &record, nil

	default:
		return nil, fmt.Errorf("unsupported record type: %s", data.RecordType)
	}
}
