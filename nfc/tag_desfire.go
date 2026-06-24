package nfc

import (
	"fmt"
)

type pcscDESFireTag struct {
	pcscBaseTag
}

func newPCSCDESFireTag(dev cardTransport, uid string) *pcscDESFireTag {
	return &pcscDESFireTag{
		pcscBaseTag: pcscBaseTag{
			device:       dev,
			uid:          uid,
			detectedType: DetectedDESFire,
		},
	}
}

func (t *pcscDESFireTag) Type() string {
	return CardTypeDesfire
}

func (t *pcscDESFireTag) NumericType() int {
	return detectedTypeNumeric(t.detectedType)
}

func (t *pcscDESFireTag) Capabilities() TagCapabilities {
	return InferTagCapabilities(t.Type())
}

func (t *pcscDESFireTag) Transceive(data []byte) ([]byte, error) {
	return t.transceive(data)
}

func (t *pcscDESFireTag) ReadData() ([]byte, error) {
	// Select NDEF application (AID: 0x010000)
	selectCmd := DESFireSelectAppAPDU([]byte{0x00, 0x00, 0x01})
	_, err := t.transceive(selectCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to select NDEF application: %w", err)
	}

	// Read file 2 (NDEF data file)
	// First 2 bytes are NLEN (NDEF length)
	readCmd := DESFireReadDataAPDU(0x02, 0, 2)
	nlenData, err := t.transceive(readCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to read NLEN: %w", err)
	}

	if len(nlenData) < 2 {
		return nil, fmt.Errorf("invalid NLEN data")
	}

	nlen := int(nlenData[0])<<8 | int(nlenData[1])
	if nlen == 0 {
		return nil, fmt.Errorf("empty NDEF message")
	}

	// Read NDEF message
	readCmd = DESFireReadDataAPDU(0x02, 2, uint32(nlen))
	ndefData, err := t.transceive(readCmd)
	if err != nil {
		return nil, fmt.Errorf("failed to read NDEF data: %w", err)
	}

	return ndefData, nil
}

func (t *pcscDESFireTag) WriteData(data []byte) error {
	// Select NDEF application
	selectCmd := DESFireSelectAppAPDU([]byte{0x00, 0x00, 0x01})
	_, err := t.transceive(selectCmd)
	if err != nil {
		return fmt.Errorf("failed to select NDEF application: %w", err)
	}

	// Write NLEN (2 bytes, big-endian)
	nlen := len(data)
	nlenBytes := []byte{byte(nlen >> 8), byte(nlen & 0xFF)}

	writeCmd := DESFireWriteDataAPDU(0x02, 0, nlenBytes)
	_, err = t.transceive(writeCmd)
	if err != nil {
		return fmt.Errorf("failed to write NLEN: %w", err)
	}

	// Write NDEF data
	writeCmd = DESFireWriteDataAPDU(0x02, 2, data)
	_, err = t.transceive(writeCmd)
	if err != nil {
		return fmt.Errorf("failed to write NDEF data: %w", err)
	}

	return nil
}

func (t *pcscDESFireTag) IsWritable() (bool, error) {
	// Try to select NDEF application
	selectCmd := DESFireSelectAppAPDU([]byte{0x00, 0x00, 0x01})
	_, err := t.transceive(selectCmd)
	return err == nil, nil
}

func (t *pcscDESFireTag) CanMakeReadOnly() (bool, error) {
	return false, nil // DESFire locking is complex
}

func (t *pcscDESFireTag) MakeReadOnly() error {
	return fmt.Errorf("DESFire MakeReadOnly not supported")
}
