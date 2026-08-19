package nfc

import (
	"fmt"
	"log"
)

type pcscUltralightTag struct {
	pcscBaseTag
	layout ultralightLayout
}

// ultralightLayout describes an Ultralight-family tag's memory: where user
// memory ends, how much of it a write may fill, and how to lock all of it.
type ultralightLayout struct {
	endPage         byte // first page past user memory
	writablePages   int  // user pages a write may use
	dynamicLockPage byte // page holding the dynamic lock bytes, 0 if none
	lockable        bool // whether every user page can be made read-only
}

func ultralightLayoutFor(tagType DetectedTagType) ultralightLayout {
	switch tagType {
	case DetectedUltralightC:
		// 48 pages, user memory 4-39. The dynamic lock bytes at page 0x28
		// govern the pages the static lock does not reach.
		return ultralightLayout{endPage: 48, writablePages: 36, dynamicLockPage: 0x28, lockable: true}
	case DetectedUltralightEV1_128:
		// MF0UL21: 41 pages, user memory 4-35. Its dynamic lock page is not
		// wired up here, and setting only the static lock bytes would leave
		// pages 16-35 writable, so a lock is refused rather than half done --
		// lock bits are one-way, and a partial lock cannot be undone.
		return ultralightLayout{endPage: 36, writablePages: 32, lockable: false}
	default:
		// Original Ultralight and the MF0UL11 EV1: 16 pages, user memory 4-15,
		// all of it covered by the static lock bytes.
		return ultralightLayout{endPage: 16, writablePages: 12, lockable: true}
	}
}

func newPCSCUltralightTag(dev CardTransport, uid string, tagType DetectedTagType) *pcscUltralightTag {
	return &pcscUltralightTag{
		pcscBaseTag: pcscBaseTag{
			device:       dev,
			uid:          uid,
			detectedType: tagType,
		},
		layout: ultralightLayoutFor(tagType),
	}
}

func (t *pcscUltralightTag) Type() string {
	return CardTypeMifareUltralight
}

func (t *pcscUltralightTag) NumericType() int {
	return detectedTypeNumeric(t.detectedType)
}

func (t *pcscUltralightTag) Capabilities() TagCapabilities {
	caps := InferTagCapabilities(t.Type())

	// The inference works from the type string, which reads "MIFARE
	// Ultralight" for every variant in this family. The layout knows what the
	// chip actually is, and MaxNDEFSize gates writes (see Reader.WriteMessage),
	// so an EV1 has to report its own memory or its extra pages go unused.
	caps.CanLock = t.layout.lockable
	switch t.detectedType {
	case DetectedUltralightEV1:
		caps.MemorySize = 80 // MF0UL11: 20 pages
	case DetectedUltralightEV1_128:
		caps.MemorySize = 164 // MF0UL21: 41 pages
		caps.MaxNDEFSize = t.layout.writablePages*4 - 2
	}
	return caps
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
	for page := byte(4); page < t.layout.endPage; page++ {
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
	if requiredPages > t.layout.writablePages {
		return fmt.Errorf("data too large: need %d pages, have %d", requiredPages, t.layout.writablePages)
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
	return t.layout.lockable, nil
}

func (t *pcscUltralightTag) MakeReadOnly() error {
	if !t.layout.lockable {
		return NewNotSupportedError("MakeReadOnly")
	}

	// A complete lock needs both lock regions on an Ultralight C. The static
	// lock bytes (page 2) only cover pages 3-15, but an UL-C has 48 pages;
	// pages 16-47 are governed by the dynamic lock bytes at page 0x28. Setting
	// only the static lock — as this previously did — left most of an UL-C
	// writable. The original Ultralight has just 16 pages and no dynamic lock
	// area, so the static lock alone is complete there.
	if dynamicLockPage := t.layout.dynamicLockPage; dynamicLockPage != 0 {
		dyn, err := t.readPage(dynamicLockPage)
		if err != nil {
			return fmt.Errorf("failed to read dynamic lock page %d: %w", dynamicLockPage, err)
		}
		dyn[0], dyn[1], dyn[2] = 0xFF, 0xFF, 0xFF // byte 3 is RFU
		if err := t.writePage(dynamicLockPage, dyn); err != nil {
			return fmt.Errorf("failed to set dynamic lock bytes: %w", err)
		}
	}

	// Static lock bytes live in page 2, bytes 2-3 (locks pages 3-15).
	page2, err := t.readPage(2)
	if err != nil {
		return fmt.Errorf("failed to read page 2: %w", err)
	}
	page2[2] = 0xFF
	page2[3] = 0xFF

	return t.writePage(2, page2)
}
