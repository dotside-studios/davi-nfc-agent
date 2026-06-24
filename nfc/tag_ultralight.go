package nfc

import (
	"fmt"
	"log"
)

type pcscUltralightTag struct {
	pcscBaseTag
	isC bool
}

func newPCSCUltralightTag(dev cardTransport, uid string, tagType DetectedTagType) *pcscUltralightTag {
	return &pcscUltralightTag{
		pcscBaseTag: pcscBaseTag{
			device:       dev,
			uid:          uid,
			detectedType: tagType,
		},
		isC: tagType == DetectedUltralightC,
	}
}

func (t *pcscUltralightTag) Type() string {
	return CardTypeMifareUltralight
}

func (t *pcscUltralightTag) NumericType() int {
	return detectedTypeNumeric(t.detectedType)
}

func (t *pcscUltralightTag) Capabilities() TagCapabilities {
	return InferTagCapabilities(t.Type())
}

func (t *pcscUltralightTag) Transceive(data []byte) ([]byte, error) {
	return nil, fmt.Errorf("Transceive not supported for Ultralight")
}

// readPage reads 4 bytes from the specified page
func (t *pcscUltralightTag) readPage(page byte) ([]byte, error) {
	cmd := ReadBinaryAPDU(page, 4)
	return t.transceive(cmd)
}

// writePage writes 4 bytes to the specified page
func (t *pcscUltralightTag) writePage(page byte, data []byte) error {
	if len(data) != 4 {
		return fmt.Errorf("page data must be 4 bytes")
	}
	cmd := UpdateBinaryAPDU(page, data)
	_, err := t.transceive(cmd)
	return err
}

func (t *pcscUltralightTag) ReadData() ([]byte, error) {
	// Read pages 4 onwards (user data area)
	var allData []byte
	var lastError error
	maxPages := byte(16) // Ultralight has 16 pages
	if t.isC {
		maxPages = 48 // Ultralight C has 48 pages
	}

	for page := byte(4); page < maxPages; page++ {
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

func (t *pcscUltralightTag) WriteData(data []byte) error {
	// Build TLV payload
	tlvPayload := TLVEncode(data, TLVNDEF)

	// Calculate required pages
	totalBytes := len(tlvPayload)
	requiredPages := (totalBytes + 3) / 4

	// Check if it fits
	maxPages := 12 // Pages 4-15 for Ultralight
	if t.isC {
		maxPages = 36 // Pages 4-39 for Ultralight C (excluding auth pages)
	}
	if requiredPages > maxPages {
		return fmt.Errorf("data too large: need %d pages, have %d", requiredPages, maxPages)
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

func (t *pcscUltralightTag) IsWritable() (bool, error) {
	// Try to read page 4
	_, err := t.readPage(4)
	return err == nil, nil
}

func (t *pcscUltralightTag) CanMakeReadOnly() (bool, error) {
	return true, nil
}

func (t *pcscUltralightTag) MakeReadOnly() error {
	// Write lock bytes to page 2
	// Bytes 2-3 of page 2 are lock bytes
	page2, err := t.readPage(2)
	if err != nil {
		return fmt.Errorf("failed to read page 2: %w", err)
	}

	// Set lock bytes to 0xFF to lock all pages
	page2[2] = 0xFF
	page2[3] = 0xFF

	return t.writePage(2, page2)
}
