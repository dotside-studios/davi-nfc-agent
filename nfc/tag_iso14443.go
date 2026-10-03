package nfc

import (
	"fmt"
)

// NDEF Application AID for Type 4 tags
var ndefAppAID = []byte{0xD2, 0x76, 0x00, 0x00, 0x85, 0x01, 0x01}

type pcscISO14443Tag struct {
	pcscBaseTag
}

func newPCSCISO14443Tag(dev CardTransport, uid string) *pcscISO14443Tag {
	return &pcscISO14443Tag{
		pcscBaseTag: pcscBaseTag{
			device:       dev,
			uid:          uid,
			detectedType: DetectedISO14443_4,
		},
	}
}

// profile follows the kind the tag was built as, so a card driven through the
// same Type 4 exchange but known more precisely reports its own name and
// capacity without reimplementing the exchange. See newPCSCNTAG424Tag.
//
// A kind with no profile falls back to the generic Type 4 one, which is what
// such a card is being driven as.
func (t *pcscISO14443Tag) profile() tagProfile {
	if p, ok := profileFor(t.detectedType); ok {
		return p
	}
	return tagProfiles[DetectedISO14443_4]
}

func (t *pcscISO14443Tag) Type() string {
	return t.profile().name
}

func (t *pcscISO14443Tag) NumericType() int {
	return t.profile().numericType
}

func (t *pcscISO14443Tag) Capabilities() TagCapabilities {
	return t.profile().capabilities()
}

func (t *pcscISO14443Tag) Transceive(data []byte) ([]byte, error) {
	return t.transmitRaw(data)
}

// type4CC is what a Type 4 Capability Container says about the NDEF file: its
// ID and size, and the most one read and one update may carry.
type type4CC struct {
	fileID   []byte
	fileSize int
	mle, mlc int
}

// defaultType4Chunk is the most one READ BINARY or UPDATE BINARY carries when
// the Capability Container names no smaller limit: a short APDU's 255 bytes
// less margin.
const defaultType4Chunk = 253

// parseType4CC reads the Capability Container. CC format: CCLEN (2) | version
// (1) | MLe (2) | MLc (2) | TLVs, of which the NDEF File Control TLV (tag 0x04)
// holds the file ID, its maximum size and the access bytes.
func parseType4CC(cc []byte) (type4CC, error) {
	if len(cc) < 7 {
		return type4CC{}, fmt.Errorf("CC file too short")
	}
	out := type4CC{
		fileID: []byte{0xE1, 0x04},
		mle:    int(cc[3])<<8 | int(cc[4]),
		mlc:    int(cc[5])<<8 | int(cc[6]),
	}
	for i := 7; i < len(cc)-3; {
		tag := cc[i]
		length := int(cc[i+1])
		if tag == 0x04 && length >= 6 && i+2+length <= len(cc) {
			out.fileID = append([]byte(nil), cc[i+2:i+4]...)
			out.fileSize = int(cc[i+4])<<8 | int(cc[i+5])
			break
		}
		i += 2 + length
	}
	return out, nil
}

func (c type4CC) readChunk() int  { return chunkLimit(c.mle) }
func (c type4CC) writeChunk() int { return chunkLimit(c.mlc) }

func chunkLimit(declared int) int {
	if declared > 0 && declared < defaultType4Chunk {
		return declared
	}
	return defaultType4Chunk
}

// selectNDEFFile selects the NDEF application, reads the Capability Container
// and selects the NDEF file it names.
func (t *pcscISO14443Tag) selectNDEFFile() (type4CC, error) {
	if _, err := t.transceive(SelectFileByAIDAPDU(ndefAppAID)); err != nil {
		return type4CC{}, fmt.Errorf("failed to select NDEF application: %w", err)
	}
	if _, err := t.transceive(SelectFileAPDU([]byte{0xE1, 0x03})); err != nil {
		return type4CC{}, fmt.Errorf("failed to select CC file: %w", err)
	}
	ccData, err := t.transceive(ReadBinaryExtAPDU(0, 15))
	if err != nil {
		return type4CC{}, fmt.Errorf("failed to read CC: %w", err)
	}
	cc, err := parseType4CC(ccData)
	if err != nil {
		return type4CC{}, err
	}
	if _, err := t.transceive(SelectFileAPDU(cc.fileID)); err != nil {
		return type4CC{}, fmt.Errorf("failed to select NDEF file: %w", err)
	}
	return cc, nil
}

func (t *pcscISO14443Tag) ReadData() ([]byte, error) {
	cc, err := t.selectNDEFFile()
	if err != nil {
		return nil, err
	}

	nlenData, err := t.transceive(ReadBinaryExtAPDU(0, 2))
	if err != nil {
		return nil, fmt.Errorf("failed to read NLEN: %w", err)
	}
	if len(nlenData) < 2 {
		return nil, fmt.Errorf("failed to read NLEN: card returned %d of 2 bytes", len(nlenData))
	}

	nlen := int(nlenData[0])<<8 | int(nlenData[1])
	if nlen == 0 {
		return nil, NewNoPayloadError("ReadData (Type 4)", t.uid, nil)
	}
	if cc.fileSize > 2 && nlen > cc.fileSize-2 {
		return nil, fmt.Errorf("NLEN %d exceeds the %d bytes the NDEF file holds", nlen, cc.fileSize-2)
	}

	var ndefData []byte
	offset := 2
	maxRead := cc.readChunk()
	for remaining := nlen; remaining > 0; {
		chunk, err := t.transceive(ReadBinaryExtAPDU(uint16(offset), byte(min(remaining, maxRead))))
		if err != nil {
			return nil, fmt.Errorf("failed to read NDEF chunk at offset %d: %w", offset, err)
		}
		if len(chunk) == 0 {
			return nil, fmt.Errorf("failed to read NDEF chunk at offset %d: card returned no data", offset)
		}
		ndefData = append(ndefData, chunk...)
		offset += len(chunk)
		remaining -= len(chunk)
	}

	return ndefData, nil
}

func (t *pcscISO14443Tag) WriteData(data []byte) error {
	cc, err := t.selectNDEFFile()
	if err != nil {
		return err
	}
	if cc.fileSize > 2 && len(data) > cc.fileSize-2 {
		return NewCapacityExceededError("WriteData (Type 4)", t.uid, len(data), cc.fileSize-2)
	}

	// Write NLEN = 0 first (clear)
	if _, err := t.transceive(UpdateBinaryExtAPDU(0, []byte{0x00, 0x00})); err != nil {
		return fmt.Errorf("failed to clear NLEN: %w", err)
	}

	maxWrite := cc.writeChunk()
	offset := 2
	for i := 0; i < len(data); i += maxWrite {
		chunk := data[i:min(i+maxWrite, len(data))]
		if _, err := t.transceive(UpdateBinaryExtAPDU(uint16(offset), chunk)); err != nil {
			return fmt.Errorf("failed to write NDEF chunk at offset %d: %w", offset, err)
		}
		offset += len(chunk)
	}

	nlen := len(data)
	if _, err := t.transceive(UpdateBinaryExtAPDU(0, []byte{byte(nlen >> 8), byte(nlen & 0xFF)})); err != nil {
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
	// Type 4 locking is not implemented (see MakeReadOnly). Report it honestly
	// rather than deciding from writability, so callers don't offer a lock that
	// is guaranteed to fail.
	return false, nil
}

func (t *pcscISO14443Tag) MakeReadOnly() error {
	// Locking a Type 4 tag means rewriting the CC file's WriteAccess byte,
	// which is not implemented. Capabilities report CanLock=false to match.
	return NewNotSupportedError("ISO14443-4 MakeReadOnly")
}
