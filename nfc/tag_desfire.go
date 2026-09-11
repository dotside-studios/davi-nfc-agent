package nfc

import (
	"fmt"
	"sync"

	"github.com/dotside-studios/davi-nfc-agent/nfc/ev2"
)

type pcscDESFireTag struct {
	pcscBaseTag

	// What the card reported about itself, filled in by probe, and the keys and
	// session used to reach a file whose rights name one. Memory size and NDEF
	// capacity vary per card, so the profile carries neither.
	//
	// Guarded by mu: a scan publishes the tag to whoever broadcasts it, and
	// Capabilities is read there while an operation on this side may still be
	// probing.
	mu         sync.Mutex
	probed     bool
	memorySize int
	ndefFile   desfireFileSettings

	keys         DESFireKeys
	session      *ev2.Session
	sessionKeyNo byte
}

// probedFacts reports what the card said about itself, with a false first
// result when it has not been asked yet.
func (t *pcscDESFireTag) probedFacts() (ok bool, memorySize int, ndef desfireFileSettings) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.probed, t.memorySize, t.ndefFile
}

// canWrite reports whether the NDEF file can be written: its rights grant it to
// anyone, or they name a key the agent holds.
func (t *pcscDESFireTag) canWrite(ndef desfireFileSettings) bool {
	if ndef.freeWrite() {
		return true
	}
	keyNo, ok := ndef.writeKey()
	if !ok {
		return false
	}
	_, held := t.keyFor(keyNo)
	return held
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

	probed, memorySize, ndef := t.probedFacts()
	if !probed {
		return caps
	}

	caps.MemorySize = memorySize
	if ndef.size > dfNLENSize {
		caps.MaxNDEFSize = ndef.size - dfNLENSize
	}
	caps.CanWrite = t.canWrite(ndef)
	caps.IsReadOnly = !caps.CanWrite
	caps.CanLock = t.canChangeSettings(ndef)
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
	// Larger payloads are split across additional frames.
	//
	// Corroborated by the Capability Container an NDEF-formatted DESFire
	// carries: AN11004's layout, as libfreefare writes it, declares MLe 0x003B
	// (59) for a read and MLc 0x0034 (52) for a write, which is this figure
	// less the 7-byte command header. Both match what the frame arithmetic
	// here already produced.
	dfFrameData = 59

	// dfNDEFFileNo is the NDEF data file, and dfNLENSize the length prefix it
	// opens with. Both come from the NFC Forum's DESFire mapping, which the
	// NDEF application below is created to.
	dfNDEFFileNo = 0x02
	dfNLENSize   = 2

	// dfFileTypeStdData is a standard data file, the only type this driver
	// reads settings for.
	dfFileTypeStdData = 0x00

	// Access-right nibbles that name no key. 0x0E grants the operation to
	// anyone; 0x0F denies it to everyone.
	dfAccessFree  = 0x0E
	dfAccessNever = 0x0F
)

// dfNDEFAppAID is the NFC Forum's DESFire NDEF application, 0x000001, encoded
// the way the card reads an application identifier.
var dfNDEFAppAID = DESFireAID(0x000001)

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
	if probed, _, _ := t.probedFacts(); probed {
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
	ndef, ok := parseDESFireFileSettings(settings)
	if !ok {
		return
	}

	t.mu.Lock()
	t.memorySize, t.ndefFile, t.probed = memorySize, ndef, true
	t.mu.Unlock()
}

// dfResponse rebuilds the raw response ParseWrappedVersion expects, which reads
// the status word itself.
func dfResponse(data []byte, status byte) []byte {
	return append(append([]byte(nil), data...), 0x91, status)
}

// desfireFileSettings is what a standard data file reports about itself: how
// much protection a command touching it must carry, how large it is, and which
// key may do what to it.
//
// The rights are four nibbles: read, write, read-write, change. 0x0E grants the
// operation to anyone, 0x0F denies it outright, and any other value names the
// key that has it.
type desfireFileSettings struct {
	comm      byte
	size      int
	read      byte
	write     byte
	readWrite byte
	change    byte
}

func (s desfireFileSettings) freeRead() bool {
	return s.read == dfAccessFree || s.readWrite == dfAccessFree
}

func (s desfireFileSettings) freeWrite() bool {
	return s.write == dfAccessFree || s.readWrite == dfAccessFree
}

// readKey names the key that may read the file, where one may. The read-write
// key stands in when the read nibble denies it, since a key granted both may
// still read.
func (s desfireFileSettings) readKey() (byte, bool) {
	return namedKey(s.read, s.readWrite)
}

// writeKey names the key that may write the file, where one may.
func (s desfireFileSettings) writeKey() (byte, bool) {
	return namedKey(s.write, s.readWrite)
}

// namedKey picks the first nibble naming a key rather than granting or denying
// the operation outright.
func namedKey(nibbles ...byte) (byte, bool) {
	for _, n := range nibbles {
		if n != dfAccessFree && n != dfAccessNever {
			return n, true
		}
	}
	return 0, false
}

// parseDESFireFileSettings reads a standard data file's settings. The response
// is file type, communication settings, two bytes of access rights and three of
// size, the last two least significant byte first.
func parseDESFireFileSettings(settings []byte) (desfireFileSettings, bool) {
	const stdDataFileSettingsLen = 7
	if len(settings) < stdDataFileSettingsLen || settings[0] != dfFileTypeStdData {
		return desfireFileSettings{}, false
	}

	return desfireFileSettings{
		comm:      settings[1],
		size:      int(settings[4]) | int(settings[5])<<8 | int(settings[6])<<16,
		read:      settings[3] >> 4,
		write:     settings[3] & 0x0F,
		readWrite: settings[2] >> 4,
		change:    settings[2] & 0x0F,
	}, true
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

// fileReader reads part of the NDEF file, through a session when the file's
// rights name a key and plainly when they do not. The second result reports
// whether a reader could be had at all: a file no key of ours opens has no
// payload to give.
func (t *pcscDESFireTag) fileReader() (func(offset, length int) ([]byte, error), error) {
	probed, _, ndef := t.probedFacts()
	if !probed || ndef.freeRead() {
		return func(offset, length int) ([]byte, error) {
			return t.dfReadFile(dfNDEFFileNo, uint32(offset), uint32(length))
		}, nil
	}

	session, mode, err := t.openFor(ndef, ndef.readKey)
	if err != nil {
		return nil, err
	}
	return func(offset, length int) ([]byte, error) {
		return t.sessionReadFile(session, mode, dfNDEFFileNo, offset, length)
	}, nil
}

// openFor authenticates with the key the named right points at, and reports the
// protection the file's settings demand of every command that follows.
func (t *pcscDESFireTag) openFor(ndef desfireFileSettings, named func() (byte, bool)) (*ev2.Session, ev2.CommMode, error) {
	keyNo, ok := named()
	if !ok {
		return nil, 0, NewAuthError("DESFire file access", t.uid,
			fmt.Errorf("the file's access rights deny the operation to every key"))
	}
	mode, err := commMode(ndef.comm)
	if err != nil {
		return nil, 0, err
	}
	session, err := t.authenticate(keyNo)
	if err != nil {
		return nil, 0, err
	}
	return session, mode, nil
}

func (t *pcscDESFireTag) ReadData() ([]byte, error) {
	if err := t.dfSelectNDEFApp(); err != nil {
		return nil, NewNoPayloadError("ReadData (DESFire)", t.uid, err)
	}
	t.probe()

	read, err := t.fileReader()
	if err != nil {
		// The file is there and shut. That is a tag with nothing to give this
		// agent, not a broken read.
		return nil, NewNoPayloadError("ReadData (DESFire)", t.uid, err)
	}

	// The NDEF file opens with NLEN, the message length.
	nlenData, err := read(0, dfNLENSize)
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

	ndefData, err := read(dfNLENSize, nlen)
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

	probed, _, ndef := t.probedFacts()
	if probed && !t.canWrite(ndef) {
		return NewReadOnlyError("WriteData (DESFire)", t.uid, nil)
	}
	if capacity := ndef.size - dfNLENSize; probed && len(data) > capacity {
		return NewCapacityExceededError("WriteData (DESFire)", t.uid, len(data), capacity)
	}

	write := func(offset int, chunk []byte) error {
		return t.dfWriteFile(dfNDEFFileNo, uint32(offset), chunk)
	}
	if probed && !ndef.freeWrite() {
		session, mode, err := t.openFor(ndef, ndef.writeKey)
		if err != nil {
			return err
		}
		write = func(offset int, chunk []byte) error {
			return t.sessionWriteFile(session, mode, dfNDEFFileNo, offset, chunk)
		}
	}

	// NLEN first, two bytes most significant first, then the message after it.
	nlen := len(data)
	if err := write(0, []byte{byte(nlen >> 8), byte(nlen & 0xFF)}); err != nil {
		return fmt.Errorf("write NLEN: %w", err)
	}
	if err := write(dfNLENSize, data); err != nil {
		return fmt.Errorf("write NDEF data: %w", err)
	}
	return nil
}

func (t *pcscDESFireTag) IsWritable() (bool, error) {
	if err := t.dfSelectNDEFApp(); err != nil {
		return false, nil
	}
	t.probe()

	probed, _, ndef := t.probedFacts()
	return !probed || t.canWrite(ndef), nil
}

// CanMakeReadOnly reports whether the file's rights can be rewritten, which is
// what locking one means here.
//
// A card nothing has read yet answers with the kind's own capability, as
// Capabilities does: the driver implements locking, and whether this card
// permits it is not known until its change right has been read.
func (t *pcscDESFireTag) CanMakeReadOnly() (bool, error) {
	probed, _, ndef := t.probedFacts()
	if !probed {
		return t.profile().canLock, nil
	}
	return t.canChangeSettings(ndef), nil
}

// MakeReadOnly rewrites the NDEF file's access rights so that nothing may write
// it and nothing may change that again.
//
// This cannot be undone. The change right is set to deny everyone, so no key
// reopens the file afterwards, which is what makes the lock permanent rather
// than merely current.
func (t *pcscDESFireTag) MakeReadOnly() error {
	if err := t.dfSelectNDEFApp(); err != nil {
		return NewNotSupportedError("MakeReadOnly (DESFire)")
	}
	t.probe()

	probed, _, ndef := t.probedFacts()
	if !probed || !t.canChangeSettings(ndef) {
		return NewNotSupportedError("MakeReadOnly (DESFire)")
	}

	locked := ndef
	locked.write, locked.readWrite, locked.change = dfAccessNever, dfAccessNever, dfAccessNever
	if err := t.changeFileSettings(ndef, locked); err != nil {
		return err
	}

	t.mu.Lock()
	t.ndefFile = locked
	t.mu.Unlock()
	return nil
}
