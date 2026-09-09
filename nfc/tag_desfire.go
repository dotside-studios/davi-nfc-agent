package nfc

import (
	"fmt"
	"sync"
)

type pcscDESFireTag struct {
	pcscBaseTag

	// What the card reported about itself, filled in by probe. Memory size and
	// NDEF capacity vary per card, so the profile carries neither.
	//
	// Guarded by mu: a scan publishes the tag to whoever broadcasts it, and
	// Capabilities is read there while an operation on this side may still be
	// probing.
	mu           sync.Mutex
	probed       bool
	memorySize   int
	ndefFileSize int
	ndefWritable bool
}

// probedFacts reports what the card said about itself, with a false first
// result when it has not been asked yet.
func (t *pcscDESFireTag) probedFacts() (ok bool, memorySize, ndefFileSize int, writable bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.probed, t.memorySize, t.ndefFileSize, t.ndefWritable
}

func newPCSCDESFireTag(dev CardTransport, uid string, kind DetectedTagType) *pcscDESFireTag {
	if _, ok := profileFor(kind); !ok {
		kind = DetectedDESFire
	}
	return &pcscDESFireTag{
		pcscBaseTag: pcscBaseTag{
			device:       dev,
			uid:          uid,
			detectedType: kind,
		},
	}
}

func (t *pcscDESFireTag) profile() tagProfile {
	return tagProfiles[t.detectedType]
}

func (t *pcscDESFireTag) Type() string {
	return t.profile().name
}

func (t *pcscDESFireTag) NumericType() int {
	return t.profile().numericType
}

// Capabilities reports the profile's, with what probe read off this card
// layered over it. It sends nothing itself: a card that has not been read
// reports the kind's defaults, which claim no capacity rather than a wrong one.
func (t *pcscDESFireTag) Capabilities() TagCapabilities {
	caps := t.profile().capabilities()

	probed, memorySize, ndefFileSize, writable := t.probedFacts()
	if !probed {
		return caps
	}

	caps.MemorySize = memorySize
	if ndefFileSize > dfNLENSize {
		caps.MaxNDEFSize = ndefFileSize - dfNLENSize
	}
	caps.CanWrite = writable
	caps.IsReadOnly = !writable
	return caps
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

	// dfNDEFFileNo is the NDEF data file, and dfNLENSize the length prefix it
	// opens with. Both come from the NFC Forum's DESFire mapping, which the
	// NDEF application below is created to.
	dfNDEFFileNo = 0x02
	dfNLENSize   = 2

	// dfFileTypeStdData is a standard data file, the only type this driver
	// reads settings for.
	dfFileTypeStdData = 0x00

	// Access-right nibbles that need no key. 0x0E grants the operation to
	// anyone; 0x0F denies it to everyone.
	dfAccessFree = 0x0E
)

// dfNDEFAppAID is the NFC Forum's DESFire NDEF application.
var dfNDEFAppAID = []byte{0x00, 0x00, 0x01}

// dfTransceive sends a wrapped DESFire command and returns the response data and
// the DESFire native status byte. In ISO-wrapped mode DESFire returns its status
// in SW2 with SW1=0x91 (0x00 = OK, 0xAF = additional frame), NOT the ISO 90 00
// that the generic APDU layer treats as success, so DESFire must interpret its
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
	_, status, err := t.dfTransceive(DESFireSelectAppAPDU(dfNDEFAppAID))
	if err != nil || status != dfStatusOK {
		return dfStatusErr("select NDEF application", status, err)
	}
	return nil
}

// probe reads what varies per card: the EEPROM size from GET_VERSION, and the
// NDEF file's size and access rights from GetFileSettings. Two commands, sent
// once per tag, before the first read or write.
//
// A card that refuses either is left unprobed rather than failing the operation:
// the capacity check and the memory size are better skipped than wrong, and the
// read or write that follows reports its own failure.
func (t *pcscDESFireTag) probe() {
	if probed, _, _, _ := t.probedFacts(); probed {
		return
	}

	var memorySize int
	if data, status, err := t.dfTransceive(DESFireWrapAPDU(DFCmdGetVersion, nil)); err == nil &&
		(status == dfStatusAdditionalFrame || status == dfStatusOK) {
		if version, ok := ParseWrappedVersion(dfResponse(data, status)); ok {
			memorySize = version.MemorySize()
		}
	}

	settings, status, err := t.dfTransceive(DESFireGetFileSettingsAPDU(dfNDEFFileNo))
	if err != nil || status != dfStatusOK {
		return
	}
	size, writable, ok := parseDESFireFileSettings(settings)
	if !ok {
		return
	}

	t.mu.Lock()
	t.memorySize, t.ndefFileSize, t.ndefWritable, t.probed = memorySize, size, writable, true
	t.mu.Unlock()
}

// dfResponse rebuilds the raw response ParseWrappedVersion expects, which reads
// the status word itself.
func dfResponse(data []byte, status byte) []byte {
	return append(append([]byte(nil), data...), 0x91, status)
}

// parseDESFireFileSettings reads a standard data file's size and whether it can
// be written without authenticating.
//
// The response is file type, communication settings, two bytes of access rights
// and three of size, the last two least significant byte first. The rights are
// four nibbles: read, write, read-write, change. 0x0E grants the operation to
// anyone, 0x0F denies it outright, and any other value names the key that has
// it, which this driver cannot present.
func parseDESFireFileSettings(settings []byte) (size int, writable, ok bool) {
	const stdDataFileSettingsLen = 7
	if len(settings) < stdDataFileSettingsLen || settings[0] != dfFileTypeStdData {
		return 0, false, false
	}

	write := settings[3] & 0x0F
	readWrite := settings[2] >> 4
	size = int(settings[4]) | int(settings[5])<<8 | int(settings[6])<<16
	return size, write == dfAccessFree || readWrite == dfAccessFree, true
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
		return nil, NewNoPayloadError("ReadData (DESFire)", t.uid, err)
	}
	t.probe()

	// Read the NDEF file; its first two bytes are NLEN, the message length.
	nlenData, err := t.dfReadFile(dfNDEFFileNo, 0, dfNLENSize)
	if err != nil {
		return nil, fmt.Errorf("read NLEN: %w", err)
	}
	if len(nlenData) < dfNLENSize {
		return nil, fmt.Errorf("invalid NLEN data")
	}

	nlen := int(nlenData[0])<<8 | int(nlenData[1])
	if nlen == 0 {
		return nil, NewNoPayloadError("ReadData (DESFire)", t.uid, nil)
	}

	ndefData, err := t.dfReadFile(dfNDEFFileNo, dfNLENSize, uint32(nlen))
	if err != nil {
		return nil, fmt.Errorf("read NDEF data: %w", err)
	}
	return ndefData, nil
}

func (t *pcscDESFireTag) WriteData(data []byte) error {
	if err := t.dfSelectNDEFApp(); err != nil {
		return err
	}
	t.probe()

	probed, _, ndefFileSize, writable := t.probedFacts()
	if probed && !writable {
		return NewReadOnlyError("WriteData (DESFire)", t.uid, nil)
	}
	if capacity := ndefFileSize - dfNLENSize; probed && len(data) > capacity {
		return NewCapacityExceededError("WriteData (DESFire)", t.uid, len(data), capacity)
	}

	// Write NLEN (2 bytes, big-endian) at offset 0, then the NDEF message at
	// offset 2. Both follow the frame chain for payloads beyond one frame.
	nlen := len(data)
	if err := t.dfWriteFile(dfNDEFFileNo, 0, []byte{byte(nlen >> 8), byte(nlen & 0xFF)}); err != nil {
		return fmt.Errorf("write NLEN: %w", err)
	}
	if err := t.dfWriteFile(dfNDEFFileNo, dfNLENSize, data); err != nil {
		return fmt.Errorf("write NDEF data: %w", err)
	}
	return nil
}

func (t *pcscDESFireTag) IsWritable() (bool, error) {
	if err := t.dfSelectNDEFApp(); err != nil {
		return false, nil
	}
	t.probe()

	probed, _, _, writable := t.probedFacts()
	return !probed || writable, nil
}

func (t *pcscDESFireTag) CanMakeReadOnly() (bool, error) {
	return false, nil // DESFire locking is complex
}

func (t *pcscDESFireTag) MakeReadOnly() error {
	return NewNotSupportedError("DESFire MakeReadOnly")
}
