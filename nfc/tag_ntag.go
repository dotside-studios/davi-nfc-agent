package nfc

import (
	"fmt"
	"log"
)

type pcscNtagTag struct {
	pcscBaseTag
	maxPages byte
}

func newPCSCNtagTag(dev cardTransport, uid string, tagType DetectedTagType) *pcscNtagTag {
	maxPages := byte(45) // NTAG213
	switch tagType {
	case DetectedNTAG215:
		maxPages = 135
	case DetectedNTAG216:
		maxPages = 231
	}

	return &pcscNtagTag{
		pcscBaseTag: pcscBaseTag{
			device:       dev,
			uid:          uid,
			detectedType: tagType,
		},
		maxPages: maxPages,
	}
}

func (t *pcscNtagTag) Type() string {
	switch t.detectedType {
	case DetectedNTAG213:
		return CardTypeNtag213
	case DetectedNTAG215:
		return CardTypeNtag215
	case DetectedNTAG216:
		return CardTypeNtag216
	default:
		return CardTypeNtag215 // Default
	}
}

func (t *pcscNtagTag) NumericType() int {
	return detectedTypeNumeric(t.detectedType)
}

func (t *pcscNtagTag) Capabilities() TagCapabilities {
	return InferTagCapabilities(t.Type())
}

func (t *pcscNtagTag) Transceive(data []byte) ([]byte, error) {
	return nil, fmt.Errorf("Transceive not supported for NTAG")
}

// readPage reads 4 bytes from the specified page
func (t *pcscNtagTag) readPage(page byte) ([]byte, error) {
	cmd := ReadBinaryAPDU(page, 4)
	return t.transceive(cmd)
}

// writePage writes 4 bytes to the specified page
func (t *pcscNtagTag) writePage(page byte, data []byte) error {
	if len(data) != 4 {
		return fmt.Errorf("page data must be 4 bytes")
	}
	cmd := UpdateBinaryAPDU(page, data)
	_, err := t.transceive(cmd)
	return err
}

func (t *pcscNtagTag) ReadData() ([]byte, error) {
	// Read pages 4 to maxPages-5 (user data area, excluding config pages)
	var allData []byte
	var lastError error
	userPages := t.maxPages - 5 // Leave room for config pages at end

	for page := byte(4); page < userPages; page++ {
		data, err := t.readPage(page)
		if err != nil {
			// If card was removed, propagate that error immediately
			if IsCardRemovedError(err) {
				return nil, err
			}
			log.Printf("Error reading page %d: %v", page, err)
			lastError = err
			break
		}
		allData = append(allData, data...)
	}

	if len(allData) == 0 {
		// Check if error was due to card removal (APDU errors when card is gone)
		if lastError != nil && !t.device.IsCardPresent() {
			return nil, NewCardRemovedError(fmt.Errorf("card removed during read"))
		}
		if lastError != nil {
			return nil, fmt.Errorf("failed to read any data: %w", lastError)
		}
		return nil, fmt.Errorf("failed to read any data")
	}

	// Parse TLV to find NDEF message
	ndefData, found := TLVFindNDEF(allData)
	if !found {
		return nil, fmt.Errorf("no NDEF message found")
	}

	return ndefData, nil
}

func (t *pcscNtagTag) WriteData(data []byte) error {
	// Build TLV payload
	tlvPayload := TLVEncode(data, TLVNDEF)

	// Calculate required pages
	totalBytes := len(tlvPayload)
	requiredPages := (totalBytes + 3) / 4

	// Check if it fits
	userPages := int(t.maxPages) - 5 - 4 // Subtract config pages and reserved pages
	if requiredPages > userPages {
		return fmt.Errorf("data too large: need %d pages, have %d", requiredPages, userPages)
	}

	// Pad to 4-byte boundary
	for len(tlvPayload)%4 != 0 {
		tlvPayload = append(tlvPayload, 0x00)
	}

	// Write pages starting at page 4
	for i := 0; i < len(tlvPayload); i += 4 {
		page := byte(4 + i/4)
		if err := t.writePage(page, tlvPayload[i:i+4]); err != nil {
			return fmt.Errorf("failed to write page %d: %w", page, err)
		}
	}

	return nil
}

func (t *pcscNtagTag) IsWritable() (bool, error) {
	// Try to read page 4
	_, err := t.readPage(4)
	return err == nil, nil
}

func (t *pcscNtagTag) CanMakeReadOnly() (bool, error) {
	return true, nil
}

func (t *pcscNtagTag) MakeReadOnly() error {
	// Write lock bytes to page 2
	page2, err := t.readPage(2)
	if err != nil {
		return fmt.Errorf("failed to read page 2: %w", err)
	}

	page2[2] = 0xFF
	page2[3] = 0xFF

	return t.writePage(2, page2)
}
