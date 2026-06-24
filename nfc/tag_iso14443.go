package nfc

import (
	"fmt"
)

// NDEF Application AID for Type 4 tags
var ndefAppAID = []byte{0xD2, 0x76, 0x00, 0x00, 0x85, 0x01, 0x01}

type pcscISO14443Tag struct {
	pcscBaseTag
}

func newPCSCISO14443Tag(dev cardTransport, uid string) *pcscISO14443Tag {
	return &pcscISO14443Tag{
		pcscBaseTag: pcscBaseTag{
			device:       dev,
			uid:          uid,
			detectedType: DetectedISO14443_4,
		},
	}
}

func (t *pcscISO14443Tag) Type() string {
	return CardTypeType4
}

func (t *pcscISO14443Tag) NumericType() int {
	return detectedTypeNumeric(t.detectedType)
}

func (t *pcscISO14443Tag) Capabilities() TagCapabilities {
	return InferTagCapabilities(t.Type())
}

func (t *pcscISO14443Tag) Transceive(data []byte) ([]byte, error) {
	return t.transceive(data)
}

func (t *pcscISO14443Tag) ReadData() ([]byte, error) {
	// Select NDEF application
	selectAppCmd := SelectFileByAIDAPDU(ndefAppAID)
	_, err := t.transceive(selectAppCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to select NDEF application: %w", err)
	}

	// Select CC file (E103)
	selectCCCmd := SelectFileAPDU([]byte{0xE1, 0x03})
	_, err = t.transceive(selectCCCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to select CC file: %w", err)
	}

	// Read CC file
	readCCCmd := ReadBinaryExtAPDU(0, 15)
	ccData, err := t.transceive(readCCCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to read CC: %w", err)
	}

	// Parse CC to find NDEF file ID
	// CC format: CCLEN (1) | Version (1) | MLe (2) | MLc (2) | TLVs...
	if len(ccData) < 7 {
		return nil, fmt.Errorf("CC file too short")
	}

	// Find NDEF File Control TLV (Tag 0x04)
	ndefFileID := []byte{0xE1, 0x04} // Default
	for i := 7; i < len(ccData)-3; {
		tag := ccData[i]
		length := int(ccData[i+1])
		if tag == 0x04 && length >= 6 && i+2+length <= len(ccData) {
			// NDEF File Control TLV
			ndefFileID = ccData[i+2 : i+4]
			break
		}
		i += 2 + length
	}

	// Select NDEF file
	selectNDEFCmd := SelectFileAPDU(ndefFileID)
	_, err = t.transceive(selectNDEFCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to select NDEF file: %w", err)
	}

	// Read NLEN (2 bytes)
	readNLENCmd := ReadBinaryExtAPDU(0, 2)
	nlenData, err := t.transceive(readNLENCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to read NLEN: %w", err)
	}

	nlen := int(nlenData[0])<<8 | int(nlenData[1])
	if nlen == 0 {
		return nil, fmt.Errorf("empty NDEF message")
	}

	// Read NDEF message in chunks
	var ndefData []byte
	offset := uint16(2)
	remaining := nlen
	maxRead := 253 // Max Le for single read

	for remaining > 0 {
		toRead := remaining
		if toRead > maxRead {
			toRead = maxRead
		}

		readCmd := ReadBinaryExtAPDU(offset, byte(toRead))
		chunk, err := t.transceive(readCmd)
		if err != nil {
			return nil, fmt.Errorf("failed to read NDEF chunk at offset %d: %w", offset, err)
		}

		ndefData = append(ndefData, chunk...)
		offset += uint16(len(chunk))
		remaining -= len(chunk)
	}

	return ndefData, nil
}

func (t *pcscISO14443Tag) WriteData(data []byte) error {
	// Select NDEF application
	selectAppCmd := SelectFileByAIDAPDU(ndefAppAID)
	_, err := t.transceive(selectAppCmd)
	if err != nil {
		return fmt.Errorf("failed to select NDEF application: %w", err)
	}

	// Select NDEF file (E104)
	selectNDEFCmd := SelectFileAPDU([]byte{0xE1, 0x04})
	_, err = t.transceive(selectNDEFCmd)
	if err != nil {
		return fmt.Errorf("failed to select NDEF file: %w", err)
	}

	// Write NLEN = 0 first (clear)
	writeNLENCmd := UpdateBinaryExtAPDU(0, []byte{0x00, 0x00})
	_, err = t.transceive(writeNLENCmd)
	if err != nil {
		return fmt.Errorf("failed to clear NLEN: %w", err)
	}

	// Write NDEF data in chunks
	offset := uint16(2)
	maxWrite := 253
	for i := 0; i < len(data); i += maxWrite {
		end := i + maxWrite
		if end > len(data) {
			end = len(data)
		}
		chunk := data[i:end]

		writeCmd := UpdateBinaryExtAPDU(offset, chunk)
		_, err = t.transceive(writeCmd)
		if err != nil {
			return fmt.Errorf("failed to write NDEF chunk at offset %d: %w", offset, err)
		}
		offset += uint16(len(chunk))
	}

	// Write final NLEN
	nlen := len(data)
	nlenBytes := []byte{byte(nlen >> 8), byte(nlen & 0xFF)}
	writeNLENCmd = UpdateBinaryExtAPDU(0, nlenBytes)
	_, err = t.transceive(writeNLENCmd)
	if err != nil {
		return fmt.Errorf("failed to write NLEN: %w", err)
	}

	return nil
}

func (t *pcscISO14443Tag) IsWritable() (bool, error) {
	// Select NDEF application and check CC WriteAccess byte
	selectAppCmd := SelectFileByAIDAPDU(ndefAppAID)
	_, err := t.transceive(selectAppCmd)
	if err != nil {
		return false, nil
	}

	// Select and read CC
	selectCCCmd := SelectFileAPDU([]byte{0xE1, 0x03})
	_, err = t.transceive(selectCCCmd)
	if err != nil {
		return false, nil
	}

	readCCCmd := ReadBinaryExtAPDU(0, 15)
	ccData, err := t.transceive(readCCCmd)
	if err != nil {
		return false, nil
	}

	// Find NDEF File Control TLV and check WriteAccess byte
	for i := 7; i < len(ccData)-3; {
		tag := ccData[i]
		length := int(ccData[i+1])
		if tag == 0x04 && length >= 6 && i+2+length <= len(ccData) {
			// WriteAccess is at offset 5 within the TLV value
			writeAccess := ccData[i+2+5]
			return writeAccess == 0x00, nil
		}
		i += 2 + length
	}

	return false, nil
}

func (t *pcscISO14443Tag) CanMakeReadOnly() (bool, error) {
	writable, err := t.IsWritable()
	if err != nil {
		return false, err
	}
	return writable, nil
}

func (t *pcscISO14443Tag) MakeReadOnly() error {
	// Modify CC WriteAccess byte to 0xFF
	// This is a simplified implementation
	return fmt.Errorf("ISO14443-4 MakeReadOnly not yet implemented")
}
