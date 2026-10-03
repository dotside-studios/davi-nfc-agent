package nfctest

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"strings"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/ev2"
	"github.com/dotside-studios/davi-nfc-agent/nfc/ntag424"
)

// The NTAG 424 DNA's secure side: EV2 authentication, five AES keys, three
// files with settings, SDM mirroring and the native commands the ntag424
// package builds. Defaults follow NT4H2421Gx: every key zero at version 0, file
// 01 the CC, file 02 the NDEF file free to read and write, file 03 a
// proprietary file behind keys 2 and 3.

// Native instruction bytes the emulator answers.
const (
	n4InsAuthFirst       = 0x71
	n4InsAuthNonFirst    = 0x77
	n4InsAdditionalFrame = 0xAF
	n4InsGetVersion      = 0x60
	n4InsChangeFileSet   = 0x5F
	n4InsChangeKey       = 0xC4
	n4InsGetCardUID      = 0x51
	n4InsGetFileSettings = 0xF5
	n4InsReadData        = 0xAD
	n4InsWriteData       = 0x8D
	n4InsGetFileCounters = 0xF6
	n4InsGetKeyVersion   = 0x64
	n4InsSetConfig       = 0x5C
	n4InsReadSig         = 0x3C
	n4InsGetTTStatus     = 0xF7
)

// Native status bytes (SW2 under SW1 = 0x91).
const (
	n4StOK          = 0x00
	n4StNoSuchKey   = 0x40
	n4StIllegalCmd  = 0x1C
	n4StIntegrity   = 0x1E
	n4StLength      = 0x7E
	n4StParameter   = 0x9E
	n4StPermission  = 0x9D
	n4StAuthError   = 0xAE
	n4StAuthDelay   = 0xAD
	n4StBoundary    = 0xBE
	n4StFileMissing = 0xF0
)

// NTAG424FileSizes are the sizes of files 01, 02 and 03.
var NTAG424FileSizes = [3]int{32, 256, 128}

// NTAG424Signature is the fixed 56 bytes the emulator answers ReadSig with.
var NTAG424Signature = func() []byte {
	b := make([]byte, ntag424.SigSize)
	for i := range b {
		b[i] = byte(0xA0 + i)
	}
	return b
}()

const (
	n4MaxFailLimit  = 1000
	n4MaxFailDec    = 10
	n4MaxReadBytes  = 224
	n4MaxCounter    = 0xFFFFFF
	n4PICCDataLen   = 16
	n4UIDLen        = 7
	n4RandomIDFlag  = 0x02
	n4ISOFileCC     = 0xE103
	n4ISOFileNDEF   = 0xE104
	n4ISOFileCustom = 0xE105
)

type n4File struct {
	settings ntag424.FileSettings
	data     []byte
}

type ntag424State struct {
	uid      []byte
	keys     [5][]byte
	versions [5]byte
	files    [3]n4File

	randomID  bool
	presented []byte

	failEnabled bool
	failLimit   uint16
	failDec     uint16
	failTotal   uint16

	tamper *[2]byte

	pendStage int
	pendFirst bool
	pendKey   byte
	rndB      []byte

	session *ev2.Session
	authKey byte

	appSelected bool
	selected    int

	sdmCounter uint32
	sdmCounted bool
	sdmCurrent uint32
}

// NTAG424Option provisions an emulated NTAG 424 DNA before it is first
// presented. A bad option panics, so a test fails at setup.
type NTAG424Option func(*ntag424State)

func newNTAG424State(uid []byte, opts []NTAG424Option) *ntag424State {
	s := &ntag424State{
		uid:         append([]byte(nil), uid...),
		failEnabled: true,
		failLimit:   n4MaxFailLimit,
		failDec:     n4MaxFailDec,
	}
	for i := range s.keys {
		s.keys[i] = make([]byte, ntag424.KeySize)
	}
	for i, size := range NTAG424FileSizes {
		s.files[i].data = make([]byte, size)
	}
	copy(s.files[0].data, []byte{
		0x00, 0x17, 0x20, 0x00, 0xFF, 0x00, 0xFF,
		0x04, 0x06, 0xE1, 0x04, 0x01, 0x00, 0x00, 0x00,
		0x05, 0x06, 0xE1, 0x05, 0x00, 0x80, 0x82, 0x83,
	})
	s.files[0].settings = ntag424.FileSettings{ReadWrite: 0, Change: 0, Read: ntag424.AccessFree, Write: 0}
	s.files[1].settings = ntag424.FileSettings{
		ReadWrite: ntag424.AccessFree, Change: 0, Read: ntag424.AccessFree, Write: ntag424.AccessFree,
	}
	s.files[2].settings = ntag424.FileSettings{CommMode: ntag424.CommFull, ReadWrite: 3, Change: 0, Read: 2, Write: 3}
	for i := range s.files {
		s.files[i].settings.FileType = 0x00
		s.files[i].settings.FileSize = uint32(NTAG424FileSizes[i])
	}
	s.presented = s.uid
	for _, o := range opts {
		o(s)
	}
	return s
}

// NTAG424WithKeys sets keys by number (0 to 4), each 16 bytes.
func NTAG424WithKeys(keys map[byte][]byte) NTAG424Option {
	return func(s *ntag424State) {
		for n, k := range keys {
			if n > 4 || len(k) != ntag424.KeySize {
				panic(fmt.Sprintf("nfctest: NTAG424 key %d: want a 16-byte key for 0..4", n))
			}
			s.keys[n] = append([]byte(nil), k...)
		}
	}
}

// NTAG424WithKeySet sets keys 0 to 4 from a KeySet, diversified by the card's
// UID when the set says so.
func NTAG424WithKeySet(ks ntag424.KeySet) NTAG424Option {
	return func(s *ntag424State) {
		for n := byte(0); n <= 4; n++ {
			if k, ok := ks.Key(n, s.uid); ok {
				s.keys[n] = k
			}
		}
	}
}

// NTAG424WithKeyVersion sets a key's version byte.
func NTAG424WithKeyVersion(keyNo, version byte) NTAG424Option {
	return func(s *ntag424State) {
		if keyNo > 4 {
			panic("nfctest: NTAG424 key number above 4")
		}
		s.versions[keyNo] = version
	}
}

// NTAG424WithFileSettings replaces a file's settings, checked as
// ChangeFileSettings checks them.
func NTAG424WithFileSettings(fileNo byte, fs ntag424.FileSettings) NTAG424Option {
	return func(s *ntag424State) {
		if fileNo < 1 || fileNo > 3 {
			panic("nfctest: NTAG424 file number outside 1..3")
		}
		if st := s.checkSettings(fileNo, fs); st != n4StOK {
			panic(fmt.Sprintf("nfctest: NTAG424 file %d settings refused (%02X)", fileNo, st))
		}
		s.setSettings(fileNo, fs)
	}
}

// NTAG424WithNDEF sets the raw content of the NDEF file, NLEN included.
func NTAG424WithNDEF(content []byte) NTAG424Option {
	return func(s *ntag424State) {
		if len(content) > NTAG424FileSizes[1] {
			panic("nfctest: NTAG424 NDEF content larger than the file")
		}
		clear(s.files[1].data)
		copy(s.files[1].data, content)
	}
}

// NTAG424WithSDM provisions the NDEF file from a plan: its message and its
// settings, so the tag mirrors as PlanSDM laid it out.
func NTAG424WithSDM(plan *ntag424.SDMPlan) NTAG424Option {
	return func(s *ntag424State) {
		NTAG424WithNDEF(plan.NDEF)(s)
		NTAG424WithFileSettings(ntag424.NDEFFileNo, plan.Settings)(s)
	}
}

// NTAG424WithRandomID turns the random UID on.
func NTAG424WithRandomID() NTAG424Option {
	return func(s *ntag424State) { s.setRandomID(true) }
}

// NTAG424WithFailedAuthLimit configures the failed-authentication counter, as
// SetConfiguration option 0x0A does. A zero limit is a card that delays at
// once; a disabled counter never delays.
func NTAG424WithFailedAuthLimit(enabled bool, limit, decrement uint16) NTAG424Option {
	return func(s *ntag424State) { s.failEnabled, s.failLimit, s.failDec = enabled, limit, decrement }
}

// NTAG424WithTagTamper makes the card a TagTamper one, answering GetTTStatus
// with these statuses ('C', 'O' or 'I').
func NTAG424WithTagTamper(permanent, current byte) NTAG424Option {
	return func(s *ntag424State) { s.tamper = &[2]byte{permanent, current} }
}

func (s *ntag424State) setRandomID(on bool) {
	s.randomID = on
	s.reroll()
}

func (s *ntag424State) reroll() {
	if !s.randomID {
		s.presented = s.uid
		return
	}
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	b[0] = 0x08
	s.presented = b
}

func (s *ntag424State) setSettings(fileNo byte, fs ntag424.FileSettings) {
	f := &s.files[fileNo-1]
	fs.FileType = 0x00
	fs.FileSize = uint32(len(f.data))
	f.settings = fs
}

func (s *ntag424State) dropSession() {
	s.session = nil
	s.pendStage = 0
}

func (s *ntag424State) clearSelection() {
	s.dropSession()
	s.selected = 0
	s.sdmCounted = false
}

func (s *ntag424State) transceive(e *type4Emulator, cmd []byte) []byte {
	switch cmd[0] {
	case nfc.CLADESFire:
		return s.native(e, cmd)
	case nfc.CLAStandard:
		switch cmd[1] {
		case nfc.INSSelectFile:
			return s.isoSelect(cmd)
		case nfc.INSReadBinary:
			return s.isoRead(cmd)
		case nfc.INSUpdateBin:
			return s.isoUpdate(cmd)
		}
		return apduSW(0x6D00)
	}
	return apduSW(0x6E00)
}

func n4SW(status byte) []byte { return []byte{0x91, status} }

func n4Plain(data []byte) []byte { return append(append([]byte(nil), data...), 0x91, n4StOK) }

// n4Payload is the data of an ISO-wrapped native command.
func n4Payload(cmd []byte) ([]byte, bool) {
	if len(cmd) == 5 {
		return nil, true
	}
	lc := int(cmd[4])
	if len(cmd) != 6+lc {
		return nil, false
	}
	return cmd[5 : 5+lc], true
}

func (s *ntag424State) native(e *type4Emulator, cmd []byte) []byte {
	ins := cmd[1]
	payload, ok := n4Payload(cmd)
	if !ok {
		return n4SW(n4StLength)
	}

	if ins == n4InsAdditionalFrame && s.pendStage == 1 {
		return s.authSecond(payload)
	}
	s.pendStage = 0

	switch ins {
	case n4InsGetVersion:
		s.dropSession()
		e.frame = 0
		return s.versionFrame(e)
	case n4InsAdditionalFrame:
		if e.frame == 0 {
			return n4SW(n4StIllegalCmd)
		}
		return s.versionFrame(e)
	case n4InsAuthFirst, n4InsAuthNonFirst:
		return s.authFirstStep(ins == n4InsAuthFirst, payload)
	case n4InsGetFileSettings:
		return s.getFileSettings(cmd, payload)
	case n4InsGetKeyVersion:
		return s.getKeyVersion(cmd, payload)
	case n4InsReadData:
		return s.readData(cmd, payload)
	case n4InsWriteData:
		return s.writeData(cmd, payload)
	case n4InsChangeFileSet:
		return s.changeFileSettings(cmd)
	case n4InsChangeKey:
		return s.changeKey(cmd)
	case n4InsGetCardUID:
		return s.getCardUID(cmd)
	case n4InsGetFileCounters:
		return s.getFileCounters(cmd)
	case n4InsSetConfig:
		return s.setConfiguration(cmd)
	case n4InsReadSig:
		return s.readSig(cmd)
	case n4InsGetTTStatus:
		return s.getTTStatus(cmd)
	}
	return n4SW(n4StIllegalCmd)
}

func (s *ntag424State) versionFrame(e *type4Emulator) []byte {
	if e.frame >= len(e.version) {
		return n4SW(n4StIllegalCmd)
	}
	frame := e.version[e.frame]
	e.frame++
	status := byte(dfStatusAdditionalFrame)
	if e.frame == len(e.version) {
		status = n4StOK
	}
	return append(append([]byte(nil), frame...), 0x91, status)
}

func (s *ntag424State) authFirstStep(first bool, payload []byte) []byte {
	if len(payload) < 1 || (first && len(payload) < 2) {
		return n4SW(n4StLength)
	}
	keyNo := payload[0]
	if keyNo > 4 {
		return n4SW(n4StNoSuchKey)
	}
	if !first && s.session == nil {
		return n4SW(n4StIllegalCmd)
	}
	if s.failEnabled && s.failTotal >= s.failLimit {
		return n4SW(n4StAuthDelay)
	}
	block, err := ev2.NewCipher(s.keys[keyNo])
	if err != nil {
		return n4SW(n4StAuthError)
	}
	s.rndB = make([]byte, 16)
	_, _ = rand.Read(s.rndB)
	s.pendStage, s.pendFirst, s.pendKey = 1, first, keyNo

	out := make([]byte, 16)
	cipher.NewCBCEncrypter(block, make([]byte, 16)).CryptBlocks(out, s.rndB)
	return append(out, 0x91, n4InsAdditionalFrame)
}

func (s *ntag424State) failAuth() []byte {
	s.dropSession()
	if s.failTotal < 0xFFFF {
		s.failTotal++
	}
	return n4SW(n4StAuthError)
}

func (s *ntag424State) authSecond(payload []byte) []byte {
	s.pendStage = 0
	if len(payload) != 32 {
		return n4SW(n4StLength)
	}
	key := s.keys[s.pendKey]
	block, err := ev2.NewCipher(key)
	if err != nil {
		return s.failAuth()
	}
	plain := make([]byte, 32)
	cipher.NewCBCDecrypter(block, make([]byte, 16)).CryptBlocks(plain, payload)
	rndA := plain[:16]
	if !bytes.Equal(plain[16:], rotateLeft(s.rndB)) {
		return s.failAuth()
	}

	var ti, reply []byte
	var counter uint16
	if s.pendFirst {
		ti = make([]byte, ev2.TISize)
		_, _ = rand.Read(ti)
		reply = append(append(append([]byte(nil), ti...), rotateLeft(rndA)...), make([]byte, 12)...)
	} else {
		ti = s.session.TI()
		counter = s.session.Counter()
		reply = rotateLeft(rndA)
	}
	encKey, macKey, err := ev2.DeriveSessionKeys(key, rndA, s.rndB)
	if err != nil {
		return s.failAuth()
	}
	session, err := ev2.NewSessionAt(ti, encKey, macKey, counter)
	if err != nil {
		return s.failAuth()
	}
	s.session, s.authKey = session, s.pendKey

	if s.failDec >= s.failTotal {
		s.failTotal = 0
	} else {
		s.failTotal -= s.failDec
	}
	out := make([]byte, len(reply))
	cipher.NewCBCEncrypter(block, make([]byte, 16)).CryptBlocks(out, reply)
	return append(out, 0x91, n4StOK)
}

// secure verifies a command inside the open session and returns its header and
// data. The failure is the response to send.
func (s *ntag424State) secure(cmd []byte, mode ev2.CommMode, headerLen int) (hdr, data, fail []byte) {
	if s.session == nil {
		return nil, nil, n4SW(n4StAuthError)
	}
	hdr, data, err := s.session.VerifyCommand(cmd, mode, headerLen)
	if err != nil {
		s.dropSession()
		if err == ev2.ErrMACMismatch {
			return nil, nil, n4SW(n4StIntegrity)
		}
		return nil, nil, n4SW(n4StLength)
	}
	return hdr, data, nil
}

func (s *ntag424State) answer(data []byte, mode ev2.CommMode) []byte {
	resp, err := s.session.Answer(n4StOK, data, mode)
	if err != nil {
		s.dropSession()
		return n4SW(n4StIllegalCmd)
	}
	return resp
}

// permitted reports whether the rights allow the operation to the session as it
// stands, and the status to refuse with when they do not.
func (s *ntag424State) permitted(fs ntag424.FileSettings, write bool) (bool, byte) {
	rights := [2]byte{fs.Read, fs.ReadWrite}
	if write {
		rights[0] = fs.Write
	}
	needsKey := false
	for _, r := range rights {
		if r == ntag424.AccessFree {
			return true, 0
		}
		if r <= 4 {
			needsKey = true
			if s.session != nil && r == s.authKey {
				return true, 0
			}
		}
	}
	if needsKey && s.session == nil {
		return false, n4StAuthError
	}
	return false, n4StPermission
}

func (s *ntag424State) file(fileNo byte) (*n4File, bool) {
	if fileNo < 1 || fileNo > 3 {
		return nil, false
	}
	return &s.files[fileNo-1], true
}

func (s *ntag424State) settingsBlock(f *n4File) []byte {
	enc, err := f.settings.Encode()
	if err != nil {
		return nil
	}
	size := len(f.data)
	out := []byte{0x00}
	out = append(out, enc[:3]...)
	out = append(out, byte(size), byte(size>>8), byte(size>>16))
	return append(out, enc[3:]...)
}

func (s *ntag424State) getFileSettings(cmd, payload []byte) []byte {
	if s.session == nil {
		if len(payload) != 1 {
			return n4SW(n4StLength)
		}
		f, ok := s.file(payload[0])
		if !ok {
			return n4SW(n4StFileMissing)
		}
		fs := f.settings
		if fs.Read != ntag424.AccessFree && fs.ReadWrite != ntag424.AccessFree && fs.Change != ntag424.AccessFree {
			return n4SW(n4StAuthError)
		}
		return n4Plain(s.settingsBlock(f))
	}
	hdr, _, fail := s.secure(cmd, ev2.CommMAC, 1)
	if fail != nil {
		return fail
	}
	f, ok := s.file(hdr[0])
	if !ok {
		return n4SW(n4StFileMissing)
	}
	return s.answer(s.settingsBlock(f), ev2.CommMAC)
}

func (s *ntag424State) getKeyVersion(cmd, payload []byte) []byte {
	if s.session == nil {
		if len(payload) != 1 {
			return n4SW(n4StLength)
		}
		if payload[0] > 4 {
			return n4SW(n4StNoSuchKey)
		}
		return n4Plain([]byte{s.versions[payload[0]]})
	}
	hdr, _, fail := s.secure(cmd, ev2.CommMAC, 1)
	if fail != nil {
		return fail
	}
	if hdr[0] > 4 {
		return n4SW(n4StNoSuchKey)
	}
	return s.answer([]byte{s.versions[hdr[0]]}, ev2.CommMAC)
}

func n4Offset(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 }

func (s *ntag424State) readData(cmd, payload []byte) []byte {
	var hdr []byte
	mode := ev2.CommPlain
	if s.session == nil {
		if len(payload) != 7 {
			return n4SW(n4StLength)
		}
		hdr = payload
	} else {
		if len(payload) < 1 {
			return n4SW(n4StLength)
		}
		f, ok := s.file(payload[0])
		if !ok {
			return n4SW(n4StFileMissing)
		}
		mode = commModeOf(f.settings.CommMode)
		var fail []byte
		if hdr, _, fail = s.secure(cmd, mode, 7); fail != nil {
			return fail
		}
	}
	f, ok := s.file(hdr[0])
	if !ok {
		return n4SW(n4StFileMissing)
	}
	if ok, st := s.permitted(f.settings, false); !ok {
		return n4SW(st)
	}
	off, length := n4Offset(hdr[1:4]), n4Offset(hdr[4:7])
	if length == 0 {
		length = len(f.data) - off
	}
	if off > len(f.data) || off+length > len(f.data) || length < 0 {
		return n4SW(n4StBoundary)
	}
	if length > n4MaxReadBytes {
		return n4SW(n4StLength)
	}
	view := s.view(hdr[0], f)
	data := view[off : off+length]
	if s.session == nil {
		return n4Plain(data)
	}
	return s.answer(data, mode)
}

func (s *ntag424State) writeData(cmd, payload []byte) []byte {
	var hdr, data []byte
	mode := ev2.CommPlain
	if s.session == nil {
		if len(payload) < 7 {
			return n4SW(n4StLength)
		}
		hdr, data = payload[:7], payload[7:]
	} else {
		if len(payload) < 1 {
			return n4SW(n4StLength)
		}
		f, ok := s.file(payload[0])
		if !ok {
			return n4SW(n4StFileMissing)
		}
		mode = commModeOf(f.settings.CommMode)
		var fail []byte
		if hdr, data, fail = s.secure(cmd, mode, 7); fail != nil {
			return fail
		}
	}
	f, ok := s.file(hdr[0])
	if !ok {
		return n4SW(n4StFileMissing)
	}
	if ok, st := s.permitted(f.settings, true); !ok {
		return n4SW(st)
	}
	off, length := n4Offset(hdr[1:4]), n4Offset(hdr[4:7])
	if length != len(data) {
		return n4SW(n4StLength)
	}
	if off+length > len(f.data) {
		return n4SW(n4StBoundary)
	}
	copy(f.data[off:], data)
	if s.session == nil {
		return n4Plain(nil)
	}
	return s.answer(nil, mode)
}

func commModeOf(m ntag424.CommMode) ev2.CommMode { return ev2.CommMode(m) }

// needChange checks the session may change a file's settings.
func (s *ntag424State) needChange(f *n4File) []byte {
	switch f.settings.Change {
	case ntag424.AccessFree:
		return nil
	case ntag424.AccessNever:
		return n4SW(n4StPermission)
	case s.authKey:
		return nil
	}
	return n4SW(n4StPermission)
}

func (s *ntag424State) changeFileSettings(cmd []byte) []byte {
	hdr, data, fail := s.secure(cmd, ev2.CommFull, 1)
	if fail != nil {
		return fail
	}
	f, ok := s.file(hdr[0])
	if !ok {
		return n4SW(n4StFileMissing)
	}
	if fail := s.needChange(f); fail != nil {
		return fail
	}
	fs, err := ntag424.ParseEncodedFileSettings(data)
	if err != nil {
		return n4SW(n4StParameter)
	}
	if st := s.checkSettings(hdr[0], *fs); st != n4StOK {
		return n4SW(st)
	}
	s.setSettings(hdr[0], *fs)
	return s.answer(nil, ev2.CommFull)
}

// checkSettings applies the card's rules to a settings block: SDM only on the
// NDEF file, in ASCII, with every mirror inside the file.
func (s *ntag424State) checkSettings(fileNo byte, fs ntag424.FileSettings) byte {
	if _, err := fs.Encode(); err != nil {
		return n4StParameter
	}
	if !fs.SDMEnabled {
		return n4StOK
	}
	size := uint32(NTAG424FileSizes[fileNo-1])
	if fileNo != ntag424.NDEFFileNo || !fs.ASCIIEncoding {
		return n4StParameter
	}
	fits := func(off, width uint32) bool { return off+width <= size }
	if fs.MirrorUID && fs.SDMMetaRead == ntag424.AccessFree && !fits(fs.UIDOffset, 2*n4UIDLen) {
		return n4StBoundary
	}
	if fs.MirrorReadCounter && fs.SDMMetaRead == ntag424.AccessFree && !fits(fs.ReadCounterOffset, 6) {
		return n4StBoundary
	}
	if fs.SDMMetaRead <= 4 && !fits(fs.PICCDataOffset, 2*n4PICCDataLen) {
		return n4StBoundary
	}
	if fs.SDMFileRead <= 4 {
		if fs.MACInputOffset > fs.MACOffset || !fits(fs.MACOffset, 2*ntag424.MACSize) {
			return n4StBoundary
		}
	}
	if fs.EncryptFileData {
		if fs.SDMFileRead > 4 || fs.ENCLength == 0 || fs.ENCLength%32 != 0 || !fits(fs.ENCOffset, fs.ENCLength) {
			return n4StParameter
		}
	}
	return n4StOK
}

func n4KeyCRC(key []byte) []byte {
	sum := ^crc32.ChecksumIEEE(key)
	return []byte{byte(sum), byte(sum >> 8), byte(sum >> 16), byte(sum >> 24)}
}

func (s *ntag424State) changeKey(cmd []byte) []byte {
	hdr, data, fail := s.secure(cmd, ev2.CommFull, 1)
	if fail != nil {
		return fail
	}
	keyNo := hdr[0]
	if keyNo > 4 {
		return n4SW(n4StNoSuchKey)
	}
	if s.authKey != 0 {
		return n4SW(n4StPermission)
	}

	var newKey []byte
	var version byte
	if keyNo == 0 {
		if len(data) != ntag424.KeySize+1 {
			return n4SW(n4StLength)
		}
		newKey, version = data[:ntag424.KeySize], data[ntag424.KeySize]
	} else {
		if len(data) != ntag424.KeySize+5 {
			return n4SW(n4StLength)
		}
		newKey = make([]byte, ntag424.KeySize)
		for i := range newKey {
			newKey[i] = data[i] ^ s.keys[keyNo][i]
		}
		version = data[ntag424.KeySize]
		if !bytes.Equal(data[ntag424.KeySize+1:], n4KeyCRC(newKey)) {
			s.dropSession()
			return n4SW(n4StIntegrity)
		}
	}
	s.keys[keyNo] = append([]byte(nil), newKey...)
	s.versions[keyNo] = version

	if keyNo == s.authKey {
		s.dropSession()
		return n4SW(n4StOK)
	}
	return s.answer(nil, ev2.CommFull)
}

func (s *ntag424State) getCardUID(cmd []byte) []byte {
	if _, _, fail := s.secure(cmd, ev2.CommFull, 0); fail != nil {
		return fail
	}
	return s.answer(s.uid, ev2.CommFull)
}

func (s *ntag424State) getFileCounters(cmd []byte) []byte {
	hdr, _, fail := s.secure(cmd, ev2.CommFull, 1)
	if fail != nil {
		return fail
	}
	f, ok := s.file(hdr[0])
	if !ok {
		return n4SW(n4StFileMissing)
	}
	fs := f.settings
	if !fs.SDMEnabled || (fs.SDMCounterRet != ntag424.AccessFree && fs.SDMCounterRet != s.authKey) {
		return n4SW(n4StPermission)
	}
	c := s.sdmCounter
	return s.answer([]byte{byte(c), byte(c >> 8), byte(c >> 16)}, ev2.CommFull)
}

func (s *ntag424State) setConfiguration(cmd []byte) []byte {
	hdr, data, fail := s.secure(cmd, ev2.CommFull, 1)
	if fail != nil {
		return fail
	}
	if s.authKey != 0 {
		return n4SW(n4StPermission)
	}
	switch hdr[0] {
	case ntag424.ConfigPICC:
		if len(data) < 1 {
			return n4SW(n4StLength)
		}
		s.setRandomID(data[0]&n4RandomIDFlag != 0)
	case ntag424.ConfigFailedAuthCounter:
		if len(data) != 5 {
			return n4SW(n4StLength)
		}
		s.failEnabled = data[0]&1 != 0
		s.failLimit = binary.LittleEndian.Uint16(data[1:])
		s.failDec = binary.LittleEndian.Uint16(data[3:])
	default:
		return n4SW(n4StParameter)
	}
	return s.answer(nil, ev2.CommFull)
}

func (s *ntag424State) readSig(cmd []byte) []byte {
	hdr, _, fail := s.secure(cmd, ev2.CommFull, 1)
	if fail != nil {
		return fail
	}
	if hdr[0] != 0x00 {
		return n4SW(n4StParameter)
	}
	return s.answer(NTAG424Signature, ev2.CommFull)
}

func (s *ntag424State) getTTStatus(cmd []byte) []byte {
	if _, _, fail := s.secure(cmd, ev2.CommFull, 0); fail != nil {
		return fail
	}
	if s.tamper == nil {
		return n4SW(n4StIllegalCmd)
	}
	return s.answer(s.tamper[:], ev2.CommFull)
}

// ISO path.

func (s *ntag424State) isoSelect(cmd []byte) []byte {
	s.clearSelection()
	if len(cmd) < 5 {
		return apduSW(0x6700)
	}
	lc := int(cmd[4])
	if len(cmd) < 5+lc {
		return apduSW(0x6700)
	}
	p1, p2 := cmd[2], cmd[3]
	data := cmd[5 : 5+lc]

	switch p1 {
	case 0x04:
		if p2 == 0x00 && bytes.Equal(data, type4NDEFAppAID) {
			s.appSelected = true
			return apduSW(0x9000)
		}
		return apduSW(0x6A82)
	case 0x00:
		if p2 != 0x0C {
			return apduSW(0x6A86)
		}
		if !s.appSelected {
			return apduSW(0x6985)
		}
		if len(data) == 2 {
			switch binary.BigEndian.Uint16(data) {
			case n4ISOFileCC:
				s.selected = 1
			case n4ISOFileNDEF:
				s.selected = 2
			case n4ISOFileCustom:
				s.selected = 3
			default:
				return apduSW(0x6A82)
			}
			return apduSW(0x9000)
		}
		return apduSW(0x6A82)
	}
	return apduSW(0x6A86)
}

func (s *ntag424State) isoRead(cmd []byte) []byte {
	if len(cmd) < 5 {
		return apduSW(0x6700)
	}
	if s.selected == 0 {
		return apduSW(0x6986)
	}
	le := int(cmd[4])
	if le == 0 {
		le = 256
	}
	fileNo := byte(s.selected)
	f := &s.files[s.selected-1]
	if ok, _ := s.permitted(f.settings, false); !ok {
		return apduSW(0x6982)
	}
	off := int(cmd[2]&0x7F)<<8 | int(cmd[3])
	if off > len(f.data) {
		return apduSW(0x6B00)
	}
	view := s.view(fileNo, f)
	end := min(off+le, len(view))
	return apduData(view[off:end])
}

func (s *ntag424State) isoUpdate(cmd []byte) []byte {
	if len(cmd) < 5 {
		return apduSW(0x6700)
	}
	lc := int(cmd[4])
	if len(cmd) < 5+lc {
		return apduSW(0x6700)
	}
	if s.selected == 0 {
		return apduSW(0x6986)
	}
	f := &s.files[s.selected-1]
	if ok, _ := s.permitted(f.settings, true); !ok {
		return apduSW(0x6982)
	}
	off := int(cmd[2]&0x7F)<<8 | int(cmd[3])
	if off+lc > len(f.data) {
		return apduSW(0x6A84)
	}
	copy(f.data[off:], cmd[5:5+lc])
	return apduSW(0x9000)
}

// view is the file as a reader sees it: with SDM on, the NDEF file's mirrors
// filled in for this read. The counter advances once per selection.
func (s *ntag424State) view(fileNo byte, f *n4File) []byte {
	fs := f.settings
	if fileNo != ntag424.NDEFFileNo || !fs.SDMEnabled {
		return f.data
	}
	if !s.sdmCounted {
		s.sdmCurrent = s.sdmCounter
		if s.sdmCounter < n4MaxCounter {
			s.sdmCounter++
		}
		s.sdmCounted = true
	}

	out := append([]byte(nil), f.data...)
	put := func(off uint32, text string) { copy(out[off:], text) }
	hexUp := func(b []byte) string { return strings.ToUpper(hex.EncodeToString(b)) }
	ctr := s.sdmCurrent
	ctr3 := []byte{byte(ctr), byte(ctr >> 8), byte(ctr >> 16)}

	picc := &ntag424.PICCData{
		UID: s.uid, ReadCounter: ctr,
		UIDMirrored: fs.MirrorUID, CounterMirrored: fs.MirrorReadCounter,
	}
	switch {
	case fs.SDMMetaRead <= 4:
		plain := make([]byte, n4PICCDataLen)
		tag := byte(n4UIDLen)
		n := 1
		if fs.MirrorUID {
			tag |= 0x80
			n += copy(plain[n:], s.uid)
		}
		if fs.MirrorReadCounter {
			tag |= 0x40
			copy(plain[n:], ctr3)
		}
		plain[0] = tag
		block, err := aes.NewCipher(s.keys[fs.SDMMetaRead])
		if err != nil {
			return out
		}
		enc := make([]byte, n4PICCDataLen)
		cipher.NewCBCEncrypter(block, make([]byte, 16)).CryptBlocks(enc, plain)
		put(fs.PICCDataOffset, hexUp(enc))
	case fs.SDMMetaRead == ntag424.AccessFree:
		if fs.MirrorUID {
			put(fs.UIDOffset, hexUp(s.uid))
		}
		if fs.MirrorReadCounter {
			put(fs.ReadCounterOffset, fmt.Sprintf("%06X", ctr))
		}
	}

	if fs.SDMFileRead > 4 {
		return out
	}
	fileKey := s.keys[fs.SDMFileRead]
	if fs.EncryptFileData {
		encKey, _, err := ntag424.SessionKeys(fileKey, picc)
		if err != nil {
			return out
		}
		block, err := aes.NewCipher(encKey)
		if err != nil {
			return out
		}
		iv := make([]byte, 16)
		copy(iv, ctr3)
		block.Encrypt(iv, iv)
		plain := out[fs.ENCOffset : fs.ENCOffset+fs.ENCLength/2]
		enc := make([]byte, len(plain))
		cipher.NewCBCEncrypter(block, iv).CryptBlocks(enc, plain)
		put(fs.ENCOffset, hexUp(enc))
	}
	mac, err := ntag424.MAC(fileKey, picc, out[fs.MACInputOffset:fs.MACOffset])
	if err != nil {
		return out
	}
	put(fs.MACOffset, hexUp(mac))
	return out
}

// Observation hooks, for tests.

func (c *EmulatedCard) ntagEmu() (*type4Emulator, *ntag424State) {
	e, ok := c.transport.(*type4Emulator)
	if !ok || e.ntag == nil {
		panic(fmt.Sprintf("nfctest: %s is not an emulated NTAG 424 DNA", c.uid))
	}
	return e, e.ntag
}

// Transceive sends a raw APDU to the card, as a reader would, and returns the
// full reply with its status word.
func (c *EmulatedCard) Transceive(apdu []byte) ([]byte, error) {
	return strictTransport{c.transport}.Transceive(apdu)
}

// NTAG424Authenticated reports whether the card still holds an EV2 session. A
// SELECT, a wrapped GET_VERSION or a new presentation ends it.
func (c *EmulatedCard) NTAG424Authenticated() bool {
	e, s := c.ntagEmu()
	e.mu.Lock()
	defer e.mu.Unlock()
	return s.session != nil
}

// NTAG424AuthKey is the key number the open session authenticated with.
func (c *EmulatedCard) NTAG424AuthKey() (keyNo byte, ok bool) {
	e, s := c.ntagEmu()
	e.mu.Lock()
	defer e.mu.Unlock()
	return s.authKey, s.session != nil
}

// NTAG424SessionCounter is the card's command counter for the open session.
func (c *EmulatedCard) NTAG424SessionCounter() (counter uint16, ok bool) {
	e, s := c.ntagEmu()
	e.mu.Lock()
	defer e.mu.Unlock()
	if s.session == nil {
		return 0, false
	}
	return s.session.Counter(), true
}

// NTAG424FailedAuths is the card's running total of failed authentications.
func (c *EmulatedCard) NTAG424FailedAuths() int {
	e, s := c.ntagEmu()
	e.mu.Lock()
	defer e.mu.Unlock()
	return int(s.failTotal)
}

// NTAG424ReadCounter is the SDM read counter: how many reads of the NDEF file
// have been counted.
func (c *EmulatedCard) NTAG424ReadCounter() uint32 {
	e, s := c.ntagEmu()
	e.mu.Lock()
	defer e.mu.Unlock()
	return s.sdmCounter
}

// NTAG424Key returns a key, which is never on the wire.
func (c *EmulatedCard) NTAG424Key(keyNo byte) []byte {
	e, s := c.ntagEmu()
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]byte(nil), s.keys[keyNo]...)
}

// NTAG424KeyVersion returns a key's version byte.
func (c *EmulatedCard) NTAG424KeyVersion(keyNo byte) byte {
	e, s := c.ntagEmu()
	e.mu.Lock()
	defer e.mu.Unlock()
	return s.versions[keyNo]
}

// NTAG424FileSettings returns a file's current settings.
func (c *EmulatedCard) NTAG424FileSettings(fileNo byte) ntag424.FileSettings {
	e, s := c.ntagEmu()
	e.mu.Lock()
	defer e.mu.Unlock()
	return s.files[fileNo-1].settings
}

// NTAG424FileData returns a file's stored bytes, without SDM mirrors.
func (c *EmulatedCard) NTAG424FileData(fileNo byte) []byte {
	e, s := c.ntagEmu()
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]byte(nil), s.files[fileNo-1].data...)
}

// NTAG424RandomID reports whether the random UID is on.
func (c *EmulatedCard) NTAG424RandomID() bool {
	e, s := c.ntagEmu()
	e.mu.Lock()
	defer e.mu.Unlock()
	return s.randomID
}

// NTAG424PresentedUID is the UID the card presents: its real 7 bytes, or with
// random ID on, 4 bytes opening 0x08 that change per presentation.
func (c *EmulatedCard) NTAG424PresentedUID() []byte {
	e, s := c.ntagEmu()
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]byte(nil), s.presented...)
}

// NTAG424NewPresentation puts the card through a fresh presentation to a
// reader: selection and session are gone, and with random ID on the presented
// UID is new. It returns that UID.
func (c *EmulatedCard) NTAG424NewPresentation() []byte {
	e, s := c.ntagEmu()
	e.mu.Lock()
	defer e.mu.Unlock()
	s.clearSelection()
	s.appSelected = false
	e.frame = 0
	s.reroll()
	return append([]byte(nil), s.presented...)
}
