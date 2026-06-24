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

// dfStatusOK is the DESFire native "operation OK" status (carried in SW2 of a
// wrapped response). Frame chaining (status 0xAF) is not yet handled, so reads
// and writes are limited to a single frame (~59 bytes) for now.
const dfStatusOK = 0x00

// dfTransceive sends a wrapped DESFire command and returns the response data and
// the DESFire native status byte. In ISO-wrapped mode DESFire returns its status
// in SW2 with SW1=0x91 (0x00 = OK, 0xAF = additional frame), NOT the ISO 90 00
// that the generic APDU layer treats as success — so DESFire must interpret its
// own status. A plain 90 00 is still accepted as OK for readers that unwrap.
func (t *pcscDESFireTag) dfTransceive(cmd []byte) ([]byte, byte, error) {
	resp, err := t.transmitRaw(cmd)
	if err != nil {
		return nil, 0, err
	}
	parsed, err := ParseAPDUResponse(resp)
	if err != nil {
		return nil, 0, err
	}
	switch {
	case parsed.SW1 == 0x91:
		return parsed.Data, parsed.SW2, nil
	case parsed.IsSuccess():
		return parsed.Data, dfStatusOK, nil
	default:
		return nil, 0, fmt.Errorf("DESFire error: SW=%02X%02X", parsed.SW1, parsed.SW2)
	}
}

// dfStatusErr formats a DESFire step failure from a transport error and/or a
// non-OK status byte.
func dfStatusErr(op string, status byte, err error) error {
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	return fmt.Errorf("%s: DESFire status %#02x", op, status)
}

// dfSelectNDEFApp selects the NDEF application (AID 0x000001).
func (t *pcscDESFireTag) dfSelectNDEFApp() error {
	_, status, err := t.dfTransceive(DESFireSelectAppAPDU([]byte{0x00, 0x00, 0x01}))
	if err != nil || status != dfStatusOK {
		return dfStatusErr("select NDEF application", status, err)
	}
	return nil
}

func (t *pcscDESFireTag) ReadData() ([]byte, error) {
	if err := t.dfSelectNDEFApp(); err != nil {
		return nil, err
	}

	// Read file 2 (NDEF data file); first 2 bytes are NLEN (NDEF length).
	nlenData, status, err := t.dfTransceive(DESFireReadDataAPDU(0x02, 0, 2))
	if err != nil || status != dfStatusOK {
		return nil, dfStatusErr("read NLEN", status, err)
	}
	if len(nlenData) < 2 {
		return nil, fmt.Errorf("invalid NLEN data")
	}

	nlen := int(nlenData[0])<<8 | int(nlenData[1])
	if nlen == 0 {
		return nil, fmt.Errorf("empty NDEF message")
	}

	ndefData, status, err := t.dfTransceive(DESFireReadDataAPDU(0x02, 2, uint32(nlen)))
	if err != nil || status != dfStatusOK {
		return nil, dfStatusErr("read NDEF data", status, err)
	}
	return ndefData, nil
}

func (t *pcscDESFireTag) WriteData(data []byte) error {
	if err := t.dfSelectNDEFApp(); err != nil {
		return err
	}

	// Write NLEN (2 bytes, big-endian) at offset 0.
	nlen := len(data)
	nlenBytes := []byte{byte(nlen >> 8), byte(nlen & 0xFF)}
	if _, status, err := t.dfTransceive(DESFireWriteDataAPDU(0x02, 0, nlenBytes)); err != nil || status != dfStatusOK {
		return dfStatusErr("write NLEN", status, err)
	}

	// Write the NDEF message at offset 2.
	if _, status, err := t.dfTransceive(DESFireWriteDataAPDU(0x02, 2, data)); err != nil || status != dfStatusOK {
		return dfStatusErr("write NDEF data", status, err)
	}
	return nil
}

func (t *pcscDESFireTag) IsWritable() (bool, error) {
	return t.dfSelectNDEFApp() == nil, nil
}

func (t *pcscDESFireTag) CanMakeReadOnly() (bool, error) {
	return false, nil // DESFire locking is complex
}

func (t *pcscDESFireTag) MakeReadOnly() error {
	return fmt.Errorf("DESFire MakeReadOnly not supported")
}
