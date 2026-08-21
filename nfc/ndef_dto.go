package nfc

// NDEFRecordPayload represents an NDEF record in JSON-friendly format.
// This structure is used for serialization to WebSocket clients and API responses.
type NDEFRecordPayload struct {
	Type     string `json:"type"`               // Record type: "text", "uri", etc. (human-readable)
	Content  string `json:"content,omitempty"`  // Decoded content (text or URI)
	Language string `json:"language,omitempty"` // Language code for text records
	TNF      uint8  `json:"tnf"`                // Type Name Format (technical detail)
	ID       string `json:"id,omitempty"`       // Record ID (optional)
	Payload  []byte `json:"payload"`            // Raw payload data
}

// NDEFMessagePayload represents an NDEF message in JSON-friendly format.
type NDEFMessagePayload struct {
	Type    string              `json:"type"`    // Message type: "ndef"
	Records []NDEFRecordPayload `json:"records"` // Array of NDEF records
}

// ToPayload converts an NDEFMessage to a JSON-friendly payload structure.
// This method extracts human-readable content (text, URI) from each record
// and returns a structure suitable for WebSocket/API responses.
func (m *NDEFMessage) ToPayload() *NDEFMessagePayload {
	if m == nil {
		return nil
	}

	payload := &NDEFMessagePayload{
		Type:    "ndef",
		Records: make([]NDEFRecordPayload, 0, len(m.records)),
	}

	for _, record := range m.records {
		recordPayload := NDEFRecordPayload{
			TNF:     record.TNF,
			Payload: record.Payload,
		}

		// Add ID if present
		if len(record.ID) > 0 {
			recordPayload.ID = string(record.ID)
		}

		// Extract type-specific data and set Type + Content fields
		if recordText, ok := record.GetText(); ok {
			recordPayload.Type = "text"
			recordPayload.Content = recordText
			// Extract language from record payload
			recordPayload.Language = extractLanguageFromTextRecord(record.Payload)
		} else if recordURI, ok := record.GetURI(); ok {
			recordPayload.Type = "uri"
			recordPayload.Content = recordURI
		} else {
			// Unknown type - use raw type field
			recordPayload.Type = string(record.Type)
		}

		payload.Records = append(payload.Records, recordPayload)
	}

	return payload
}

// ToJSONMap converts an NDEFMessage to a map suitable for JSON serialization.
// This is useful for building WebSocket/API responses.
func (m *NDEFMessage) ToJSONMap() map[string]interface{} {
	if m == nil {
		return map[string]interface{}{
			"type":    "ndef",
			"records": []interface{}{},
		}
	}

	payload := m.ToPayload()
	return map[string]interface{}{
		"type":    payload.Type,
		"records": payload.Records,
	}
}

// extractLanguageFromTextRecord extracts the language code from a text record payload.
// Returns "en" as default if extraction fails.
func extractLanguageFromTextRecord(payload []byte) string {
	if len(payload) < 1 {
		return "en"
	}

	statusByte := payload[0]
	langLen := int(statusByte & 0x3F) // Lower 6 bits

	if langLen > 0 && len(payload) > 1+langLen {
		return string(payload[1 : 1+langLen])
	}

	return "en"
}
