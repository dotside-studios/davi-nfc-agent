package nfc

import (
	"fmt"
)

type pcscDESFireTag struct {
	pcscBaseTag
}

func newPCSCDESFireTag(dev CardTransport, uid string) *pcscDESFireTag {
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

// DESFire native status codes carried in SW2 of a wrapped response, plus the
// per-frame data limit used for chaining.
const (
	dfStatusOK              = 0x00 // operation OK
	dfStatusAdditionalFrame = 0xAF // more data follows / send next frame

	// dfFrameData is the max bytes of file data carried in one native frame.
	// Larger payloads are split across additional frames. Modeled from the
	// DESFire 60-byte frame (1 status byte); cross-check on hardware.
	dfFrameData = 59
)

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

// dfReadFile reads length bytes from a DESFire file, following the additional-
// frame (0xAF) chain when the payload spans more than one native frame.
func (t *pcscDESFireTag) dfReadFile(fileNo byte, offset, length uint32) ([]byte, error) {
	data, status, err := t.dfTransceive(DESFireReadDataAPDU(fileNo, offset, length))
	if err != nil {
		return nil, err
	}
	out := append([]byte(nil), data...)
	for status == dfStatusAdditionalFrame {
		data, status, err = t.dfTransceive(DESFireAdditionalFrameAPDU(nil))
		if err != nil {
			return nil, err
		}
		out = append(out, data...)
	}
	if status != dfStatusOK {
		return nil, dfStatusErr("read file", status, nil)
	}
	return out, nil
}

// dfWriteFile writes data to a DESFire file, splitting payloads larger than a
// single native frame across additional frames. The command header declares the
// full length; the first frame carries what fits, the rest follow as 0xAF
// frames.
func (t *pcscDESFireTag) dfWriteFile(fileNo byte, offset uint32, data []byte) error {
	total := uint32(len(data))
	header := []byte{
		fileNo,
		byte(offset), byte(offset >> 8), byte(offset >> 16),
		byte(total), byte(total >> 8), byte(total >> 16),
	}

	first := len(data)
	if first > dfFrameData-len(header) {
		first = dfFrameData - len(header)
	}
	_, status, err := t.dfTransceive(DESFireWrapAPDU(DFCmdWriteData, append(header, data[:first]...)))
	if err != nil {
		return err
	}

	for sent := first; sent < len(data); {
		end := sent + dfFrameData
		if end > len(data) {
			end = len(data)
		}
		_, status, err = t.dfTransceive(DESFireAdditionalFrameAPDU(data[sent:end]))
		if err != nil {
			return err
		}
		sent = end
	}
	if status != dfStatusOK {
		return dfStatusErr("write file", status, nil)
	}
	return nil
}

func (t *pcscDESFireTag) ReadData() ([]byte, error) {
	if err := t.dfSelectNDEFApp(); err != nil {
		return nil, err
	}

	// Read file 2 (NDEF data file); first 2 bytes are NLEN (NDEF length).
	nlenData, err := t.dfReadFile(0x02, 0, 2)
	if err != nil {
		return nil, fmt.Errorf("read NLEN: %w", err)
	}
	if len(nlenData) < 2 {
		return nil, fmt.Errorf("invalid NLEN data")
	}

	nlen := int(nlenData[0])<<8 | int(nlenData[1])
	if nlen == 0 {
		return nil, fmt.Errorf("empty NDEF message")
	}

	ndefData, err := t.dfReadFile(0x02, 2, uint32(nlen))
	if err != nil {
		return nil, fmt.Errorf("read NDEF data: %w", err)
	}
	return ndefData, nil
}

func (t *pcscDESFireTag) WriteData(data []byte) error {
	if err := t.dfSelectNDEFApp(); err != nil {
		return err
	}

	// Write NLEN (2 bytes, big-endian) at offset 0, then the NDEF message at
	// offset 2. Both follow the frame chain for payloads beyond one frame.
	nlen := len(data)
	if err := t.dfWriteFile(0x02, 0, []byte{byte(nlen >> 8), byte(nlen & 0xFF)}); err != nil {
		return fmt.Errorf("write NLEN: %w", err)
	}
	if err := t.dfWriteFile(0x02, 2, data); err != nil {
		return fmt.Errorf("write NDEF data: %w", err)
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
