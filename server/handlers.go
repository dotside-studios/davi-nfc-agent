package server

import (
	"fmt"
	"log"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// WriteRecord represents a single NDEF record in the write request
type WriteRecord struct {
	// Type specifies the record type: "text" or "uri"
	Type string `json:"type"`

	// Content is the text or URI content
	Content string `json:"content"`

	// Language code for text records (default: "en")
	Language string `json:"language,omitempty"`
}

// WriteRequest represents a request to write data to an NFC card.
// This API follows the "overwrite" approach - clients send the complete
// NDEF message to write. To append, clients should read current data,
// modify it, and send back the complete message.
type WriteRequest struct {
	// Records is an array of NDEF records to write
	Records []WriteRecord `json:"records"`
}

// BuildNDEFMessage builds an NDEF message from the request.
// This always creates a complete NDEF message that will overwrite the card.
func BuildNDEFMessage(writeReq WriteRequest) (*nfc.NDEFMessage, error) {
	if len(writeReq.Records) == 0 {
		return nil, fmt.Errorf("no records provided in write request")
	}

	var recordBuilders []nfc.NDEFRecordBuilder

	for i, record := range writeReq.Records {
		recordType := record.Type
		if recordType == "" {
			recordType = "text" // default to text
		}

		language := record.Language
		if language == "" {
			language = "en"
		}

		var builder nfc.NDEFRecordBuilder
		switch recordType {
		case "text":
			builder = &nfc.NDEFText{Content: record.Content, Language: language}
		case "uri":
			builder = &nfc.NDEFURI{Content: record.Content}
		default:
			return nil, fmt.Errorf("unsupported record type '%s' at index %d", recordType, i)
		}

		recordBuilders = append(recordBuilders, builder)
	}

	// Build complete NDEF message
	builder := &nfc.NDEFMessageBuilder{
		Records: recordBuilders,
	}
	ndefMsg := builder.MustBuild()

	log.Printf("WriteRequest: Writing %d NDEF record(s) (complete overwrite)", len(recordBuilders))
	return ndefMsg, nil
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
	})
	if err != nil {
		return nil, fmt.Errorf("write failed: %w", err)
	}

	log.Printf("WriteRequest: Successfully wrote NDEF message to card (verified=%v, attempts=%d)",
		result.Verified, result.Attempts)
	return result, nil
}
