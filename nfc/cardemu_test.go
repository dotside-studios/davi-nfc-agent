package nfc

import (
	"bytes"
	"strings"
	"sync"
	"testing"
)

// memEmulator is an in-memory NTAG/Ultralight tag that speaks the PC/SC
// pseudo-APDU wire protocol the real tag I/O emits (READ = FF B0 00 <page> <Le>,
// WRITE = FF D6 00 <page> <Lc> <data>), returning real SW1/SW2 framing. It
// enforces the page memory map and lock-byte rules from the NXP NTAG21x /
// Ultralight datasheets, so the production pcscNtagTag / pcscUltralightTag logic
// (page math, TLV, lock bytes) runs against emulated silicon — no hardware.
//
// It is safe for concurrent use: when driven through the reader the background
// poll goroutine touches it alongside a write, and the tests run under -race.
//
// Lock granularity is modeled per the datasheets: the static lock bytes
// (page 2, bytes 2-3) cover pages 3-15, and the NTAG dynamic lock bytes cover
// pages >=16 in 16-page blocks. The block granularity is a datasheet-derived
// model and should be cross-checked against real hardware.
type memEmulator struct {
	mu          sync.Mutex
	pages       [][4]byte
	dynLockPage int // 0 means no dynamic lock area (original Ultralight)
	present     bool

	// Fault injection (set before use): failWrites NAKs the next N write
	// commands to exercise retry; corrupt stores inverted bytes to exercise
	// read-back verification.
	failWrites int
	corrupt    bool
}

// newNTAGEmulator builds an emulator with the geometry of the given NTAG model.
func newNTAGEmulator(model DetectedTagType) *memEmulator {
	maxPages, dynLock := 135, 130 // NTAG215 default
	switch model {
	case DetectedNTAG213:
		maxPages, dynLock = 45, 40
	case DetectedNTAG216:
		maxPages, dynLock = 231, 226
	}
	return &memEmulator{
		pages:       make([][4]byte, maxPages),
		dynLockPage: dynLock,
		present:     true,
	}
}

// newUltralightEmulator builds a 16-page original MIFARE Ultralight emulator.
// Original Ultralight has no dynamic lock area — its static lock bytes cover the
// whole user area (pages 3-15).
func newUltralightEmulator() *memEmulator {
	return &memEmulator{
		pages:   make([][4]byte, 16),
		present: true,
	}
}

func (e *memEmulator) IsCardPresent() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.present
}

// Transceive decodes the PC/SC pseudo-APDU and applies memory/lock rules.
func (e *memEmulator) Transceive(cmd []byte) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(cmd) < 5 || cmd[0] != CLAPCSC {
		return emuFail(), nil
	}
	ins, page := cmd[1], cmd[3]
	switch ins {
	case INSReadBinary: // FF B0 00 <page> <Le>
		data, ok := e.readMem(int(page), int(cmd[4]))
		if !ok {
			return emuFail(), nil
		}
		return append(data, SW1Success, SW2Success), nil
	case INSUpdateBin: // FF D6 00 <page> <Lc> <data...>
		lc := int(cmd[4])
		if len(cmd) < 5+lc {
			return emuFail(), nil
		}
		if e.failWrites > 0 {
			e.failWrites--
			return emuFail(), nil // simulate a transient NAK
		}
		data := cmd[5 : 5+lc]
		if e.corrupt {
			bad := make([]byte, len(data))
			for i, b := range data {
				bad[i] = b ^ 0xFF
			}
			data = bad
		}
		if !e.writeMem(int(page), data) {
			return emuFail(), nil
		}
		return []byte{SW1Success, SW2Success}, nil
	}
	return emuFail(), nil
}

// tryWrite attempts a raw page write under the lock and reports success. Tests
// use it to probe lock state.
func (e *memEmulator) tryWrite(page int, data []byte) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.writeMem(page, data)
}

// emuFail mimics the reader returning a non-success status (e.g. on a NAK'd
// write to a locked page).
func emuFail() []byte { return []byte{0x63, 0x00} }

// readMem returns le bytes starting at the given page. Reads are always
// permitted (no read-password is modeled). The caller must hold e.mu.
func (e *memEmulator) readMem(page, le int) ([]byte, bool) {
	out := make([]byte, 0, le+4)
	for p := page; len(out) < le; p++ {
		if p < 0 || p >= len(e.pages) {
			return nil, false
		}
		out = append(out, e.pages[p][:]...)
	}
	return out[:le], true
}

// writeMem applies a 4-byte page write, honoring read-only and lock-byte rules.
// The caller must hold e.mu.
func (e *memEmulator) writeMem(page int, data []byte) bool {
	if page < 0 || page >= len(e.pages) || len(data) != 4 {
		return false
	}
	switch {
	case page == 0 || page == 1:
		return false // UID / serial number: permanently read-only
	case page == 2:
		// Static lock bytes (bytes 2-3) are OR-only; bytes 0-1 are read-only.
		e.pages[2][2] |= data[2]
		e.pages[2][3] |= data[3]
		return true
	case e.dynLockPage != 0 && page == e.dynLockPage:
		// Dynamic lock bytes are OR-only.
		for i := 0; i < 4; i++ {
			e.pages[page][i] |= data[i]
		}
		return true
	default:
		if e.lockedMem(page) {
			return false
		}
		copy(e.pages[page][:], data)
		return true
	}
}

// lockedMem reports whether a page is write-locked given the current lock bytes.
// The caller must hold e.mu.
func (e *memEmulator) lockedMem(page int) bool {
	lock0 := e.pages[2][2] // static lock byte 0
	lock1 := e.pages[2][3] // static lock byte 1
	switch {
	case page == 3:
		return lock0&(1<<3) != 0 // CC
	case page >= 4 && page <= 7:
		return lock0&(1<<uint(page)) != 0 // bits 4-7 -> pages 4-7
	case page >= 8 && page <= 15:
		return lock1&(1<<uint(page-8)) != 0 // bits 0-7 -> pages 8-15
	case page >= 16 && e.dynLockPage != 0:
		block := (page - 16) / 16 // dynamic lock bits cover 16-page blocks
		byteIdx, bitIdx := block/8, block%8
		if byteIdx > 2 {
			return false
		}
		return e.pages[e.dynLockPage][byteIdx]&(1<<uint(bitIdx)) != 0
	}
	return false
}

// sampleNDEF is a minimal well-formed NDEF text record ("Hi", lang "en").
var sampleNDEF = []byte{0xD1, 0x01, 0x04, 0x54, 0x02, 0x65, 0x6E, 0x48, 0x69}

// TestKnownAnswer_PseudoAPDUFraming pins the exact PC/SC pseudo-APDU bytes the
// tag layer emits (ACR122U app note framing) and confirms the emulator answers
// the documented frames — anchoring both code and emulator to the spec rather
// than to each other.
func TestKnownAnswer_PseudoAPDUFraming(t *testing.T) {
	if got, want := ReadBinaryAPDU(4, 4), []byte{0xFF, 0xB0, 0x00, 0x04, 0x04}; !bytes.Equal(got, want) {
		t.Errorf("READ APDU framing:\n got % X\nwant % X", got, want)
	}
	writeCmd := UpdateBinaryAPDU(4, []byte{0xDE, 0xAD, 0xBE, 0xEF})
	if want := []byte{0xFF, 0xD6, 0x00, 0x04, 0x04, 0xDE, 0xAD, 0xBE, 0xEF}; !bytes.Equal(writeCmd, want) {
		t.Errorf("WRITE APDU framing:\n got % X\nwant % X", writeCmd, want)
	}

	e := newNTAGEmulator(DetectedNTAG215)
	if resp, _ := e.Transceive(writeCmd); !bytes.Equal(resp, []byte{0x90, 0x00}) {
		t.Errorf("write response = % X, want 90 00", resp)
	}
	resp, _ := e.Transceive(ReadBinaryAPDU(4, 4))
	if want := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x90, 0x00}; !bytes.Equal(resp, want) {
		t.Errorf("read response:\n got % X\nwant % X", resp, want)
	}
}

// TestNTAGEmulator_WriteReadRoundTrip runs the real pcscNtagTag WriteData/ReadData
// (TLV encode + page writes, then page reads + TLV parse) against the emulator.
func TestNTAGEmulator_WriteReadRoundTrip(t *testing.T) {
	for _, model := range []DetectedTagType{DetectedNTAG213, DetectedNTAG215, DetectedNTAG216} {
		e := newNTAGEmulator(model)
		tag := newPCSCNtagTag(e, "04A1B2C3D4E5F6", model)

		if err := tag.WriteData(sampleNDEF); err != nil {
			t.Fatalf("model %d: WriteData: %v", model, err)
		}
		got, err := tag.ReadData()
		if err != nil {
			t.Fatalf("model %d: ReadData: %v", model, err)
		}
		if !bytes.Equal(got, sampleNDEF) {
			t.Errorf("model %d round-trip mismatch:\n got % X\nwant % X", model, got, sampleNDEF)
		}
	}
}

// TestUltralightEmulator_WriteReadRoundTrip exercises the real pcscUltralightTag
// I/O against the emulator.
func TestUltralightEmulator_WriteReadRoundTrip(t *testing.T) {
	e := newUltralightEmulator()
	tag := newPCSCUltralightTag(e, "04AABBCCDDEEFF", DetectedUltralight)

	if err := tag.WriteData(sampleNDEF); err != nil {
		t.Fatalf("WriteData: %v", err)
	}
	got, err := tag.ReadData()
	if err != nil {
		t.Fatalf("ReadData: %v", err)
	}
	if !bytes.Equal(got, sampleNDEF) {
		t.Errorf("round-trip mismatch:\n got % X\nwant % X", got, sampleNDEF)
	}
}

// TestUltralightEmulator_LockMakesUserPagesReadOnly verifies that locking an
// original Ultralight makes its entire user area (pages 4-15) read-only — the
// static lock bytes cover the whole tag here, so locking is complete.
func TestUltralightEmulator_LockMakesUserPagesReadOnly(t *testing.T) {
	e := newUltralightEmulator()
	tag := newPCSCUltralightTag(e, "04AABBCCDDEEFF", DetectedUltralight)

	if err := tag.WriteData(sampleNDEF); err != nil {
		t.Fatalf("WriteData: %v", err)
	}
	if err := tag.MakeReadOnly(); err != nil {
		t.Fatalf("MakeReadOnly: %v", err)
	}

	for _, page := range []int{4, 8, 15} {
		if e.tryWrite(page, []byte{1, 2, 3, 4}) {
			t.Errorf("page %d should be locked after MakeReadOnly", page)
		}
	}
}

// TestNTAGEmulator_LockMakesAllUserPagesReadOnly verifies that locking an NTAG
// makes its ENTIRE user area read-only — both the static-lock range (pages 3-15)
// and the dynamic-lock range (pages >=16). With only the static lock bytes set,
// pages >=16 stayed writable; this is the regression guard for that fix.
func TestNTAGEmulator_LockMakesAllUserPagesReadOnly(t *testing.T) {
	cases := []struct {
		model    DetectedTagType
		highPage int // last user page for the model
	}{
		{DetectedNTAG213, 39},
		{DetectedNTAG215, 129},
		{DetectedNTAG216, 225},
	}
	for _, tc := range cases {
		e := newNTAGEmulator(tc.model)
		tag := newPCSCNtagTag(e, "04A1B2C3D4E5F6", tc.model)

		if err := tag.MakeReadOnly(); err != nil {
			t.Fatalf("model %d: MakeReadOnly: %v", tc.model, err)
		}

		for _, page := range []int{4, 15, 16, tc.highPage} {
			if e.tryWrite(page, []byte{1, 2, 3, 4}) {
				t.Errorf("model %d: page %d writable after lock (expected locked)", tc.model, page)
			}
		}
	}
}

// classicEmulator is an in-memory MIFARE Classic 1K that speaks the PC/SC
// pseudo-APDU protocol the real pcscClassicTag emits: load key (FF 82),
// authenticate (FF 86), read block (FF B0), update block (FF D6). It models the
// 16-sector / 4-block layout with per-sector key authentication enforced before
// block access. Safe for concurrent use (reader poll + write under -race).
type classicEmulator struct {
	mu        sync.Mutex
	blocks    [][16]byte
	loadedKey []byte
	authed    int // authenticated sector, -1 if none
	present   bool
}

// newClassicEmulator builds a 1K emulator with factory-default sector trailers
// (key A and key B = FF*6, default access bits).
func newClassicEmulator() *classicEmulator {
	e := &classicEmulator{
		blocks:  make([][16]byte, 64),
		authed:  -1,
		present: true,
	}
	for sector := 0; sector < 16; sector++ {
		tr := sector*4 + 3
		for i := 0; i < 6; i++ {
			e.blocks[tr][i] = 0xFF    // key A
			e.blocks[tr][10+i] = 0xFF // key B
		}
		e.blocks[tr][6], e.blocks[tr][7] = 0xFF, 0x07 // default access bytes
		e.blocks[tr][8], e.blocks[tr][9] = 0x80, 0x69
	}
	return e
}

func (e *classicEmulator) IsCardPresent() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.present
}

func (e *classicEmulator) Transceive(cmd []byte) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(cmd) < 5 || cmd[0] != CLAPCSC {
		return emuFail(), nil
	}
	switch cmd[1] {
	case INSLoadKey: // FF 82 00 <slot> <Lc> <key>
		lc := int(cmd[4])
		if len(cmd) < 5+lc {
			return emuFail(), nil
		}
		e.loadedKey = append([]byte(nil), cmd[5:5+lc]...)
		return []byte{SW1Success, SW2Success}, nil
	case INSAuth: // FF 86 00 00 05 01 00 <block> <keyType> <slot>
		if len(cmd) < 10 {
			return emuFail(), nil
		}
		if e.authenticate(int(cmd[7]), cmd[8]) {
			return []byte{SW1Success, SW2Success}, nil
		}
		return emuFail(), nil
	case INSReadBinary: // FF B0 00 <block> <Le>
		block := int(cmd[3])
		if block < 0 || block >= len(e.blocks) || !e.isAuthedFor(block) {
			return emuFail(), nil
		}
		return append(append([]byte(nil), e.blocks[block][:]...), SW1Success, SW2Success), nil
	case INSUpdateBin: // FF D6 00 <block> <Lc> <data>
		block, lc := int(cmd[3]), int(cmd[4])
		if lc != 16 || len(cmd) < 5+lc || block <= 0 || block >= len(e.blocks) || !e.isAuthedFor(block) {
			return emuFail(), nil // block 0 is the read-only manufacturer block
		}
		copy(e.blocks[block][:], cmd[5:5+lc])
		return []byte{SW1Success, SW2Success}, nil
	}
	return emuFail(), nil
}

// authenticate succeeds when the loaded key matches the target sector's stored
// key A (0x60) or key B (0x61). The caller must hold e.mu.
func (e *classicEmulator) authenticate(block int, keyType byte) bool {
	sector := block / 4
	tr := sector*4 + 3
	if e.loadedKey == nil || tr >= len(e.blocks) {
		return false
	}
	stored := e.blocks[tr][0:6] // key A
	if keyType == MIFAREKeyB {
		stored = e.blocks[tr][10:16]
	}
	if bytes.Equal(e.loadedKey, stored) {
		e.authed = sector
		return true
	}
	return false
}

func (e *classicEmulator) isAuthedFor(block int) bool { return e.authed == block/4 }

// TestClassicEmulator_WriteReadRoundTrip runs the real pcscClassicTag WriteData/
// ReadData (key auth + block writes + TLV) against the emulator.
func TestClassicEmulator_WriteReadRoundTrip(t *testing.T) {
	e := newClassicEmulator()
	tag := newPCSCClassicTag(e, "04112233", DetectedClassic1K)

	if err := tag.WriteData(sampleNDEF); err != nil {
		t.Fatalf("WriteData: %v", err)
	}
	got, err := tag.ReadData()
	if err != nil {
		t.Fatalf("ReadData: %v", err)
	}
	if !bytes.Equal(got, sampleNDEF) {
		t.Errorf("round-trip mismatch:\n got % X\nwant % X", got, sampleNDEF)
	}
}

// TestClassicEmulator_AuthRequiredForAccess verifies the auth flow is enforced:
// a sector whose keys aren't among the defaults can't be read.
func TestClassicEmulator_AuthRequiredForAccess(t *testing.T) {
	e := newClassicEmulator()
	tr := 1*4 + 3 // sector 1 trailer
	for i := 0; i < 6; i++ {
		e.blocks[tr][i] = 0x11    // non-default key A
		e.blocks[tr][10+i] = 0x22 // non-default key B
	}
	tag := newPCSCClassicTag(e, "04112233", DetectedClassic1K)

	if _, err := tag.ReadData(); err == nil {
		t.Error("expected ReadData to fail when no default key authenticates sector 1")
	}
}

// desfireEmulator is an in-memory DESFire NDEF tag speaking the ISO-wrapped
// native protocol the real pcscDESFireTag emits (CLA 0x90: SelectApplication
// 0x5A, ReadData 0xBD, WriteData 0x3D). Responses carry the DESFire native
// status in SW2 with SW1=0x91 (0x00 = OK), matching real silicon — which is why
// the tag layer needed DESFire-specific status handling. File 2 holds the
// NLEN-prefixed NDEF message. Safe for concurrent use (reader poll under -race).
//
// Frame chaining (the 0x91 0xAF "additional frame" flow for payloads beyond one
// frame) is not modeled; round-trips here use short messages. Real DESFire
// chunks reads/writes at ~59 bytes — a fidelity limit to revisit on hardware,
// and a gap the production code does not handle yet either.
type desfireEmulator struct {
	mu           sync.Mutex
	selectedNDEF bool
	file2        []byte
	present      bool
}

func newDESFireEmulator() *desfireEmulator {
	return &desfireEmulator{file2: make([]byte, 256), present: true}
}

func (e *desfireEmulator) IsCardPresent() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.present
}

// dfResp wraps optional data with the DESFire status (SW = 91 <status>).
func dfResp(data []byte, status byte) []byte {
	return append(append([]byte(nil), data...), 0x91, status)
}

func (e *desfireEmulator) Transceive(cmd []byte) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(cmd) < 5 || cmd[0] != CLADESFire {
		return dfResp(nil, 0xA0), nil
	}
	lc := int(cmd[4])
	if len(cmd) < 5+lc {
		return dfResp(nil, 0x7E), nil // length error
	}
	body := cmd[5 : 5+lc]
	switch cmd[1] {
	case DFCmdSelectApplication:
		if len(body) == 3 && body[0] == 0x00 && body[1] == 0x00 && body[2] == 0x01 {
			e.selectedNDEF = true
			return dfResp(nil, 0x00), nil
		}
		return dfResp(nil, 0xA0), nil // application not found
	case DFCmdReadData:
		off, length, ok := e.fileRange(body)
		if !ok {
			return dfResp(nil, 0xBE), nil // boundary error
		}
		return dfResp(e.file2[off:off+length], 0x00), nil
	case DFCmdWriteData:
		off, length, ok := e.fileRange(body)
		if !ok || len(body) < 7+length {
			return dfResp(nil, 0xBE), nil
		}
		copy(e.file2[off:off+length], body[7:7+length])
		return dfResp(nil, 0x00), nil
	}
	return dfResp(nil, 0xA0), nil
}

// fileRange decodes (fileNo, 3-byte LE offset, 3-byte LE length) from a
// Read/Write command body and validates it targets file 2 within bounds. The
// caller must hold e.mu.
func (e *desfireEmulator) fileRange(body []byte) (off, length int, ok bool) {
	if !e.selectedNDEF || len(body) < 7 || body[0] != 0x02 {
		return 0, 0, false
	}
	off = int(body[1]) | int(body[2])<<8 | int(body[3])<<16
	length = int(body[4]) | int(body[5])<<8 | int(body[6])<<16
	if off < 0 || length < 0 || off+length > len(e.file2) {
		return 0, 0, false
	}
	return off, length, true
}

// TestDESFireEmulator_WriteReadRoundTrip runs the real pcscDESFireTag
// WriteData/ReadData (select app + NLEN + NDEF, with DESFire status handling)
// against the emulator.
func TestDESFireEmulator_WriteReadRoundTrip(t *testing.T) {
	e := newDESFireEmulator()
	tag := newPCSCDESFireTag(e, "04DE5F1RE0")

	if err := tag.WriteData(sampleNDEF); err != nil {
		t.Fatalf("WriteData: %v", err)
	}
	got, err := tag.ReadData()
	if err != nil {
		t.Fatalf("ReadData: %v", err)
	}
	if !bytes.Equal(got, sampleNDEF) {
		t.Errorf("round-trip mismatch:\n got % X\nwant % X", got, sampleNDEF)
	}
}

// TestDESFire_WrappedStatusNotPlainISO documents why DESFire needs its own
// status handling: a wrapped DESFire OK (91 00) is not the ISO success (90 00)
// the generic APDU layer recognizes. The tag layer previously used that generic
// check and would have rejected every real DESFire response.
func TestDESFire_WrappedStatusNotPlainISO(t *testing.T) {
	parsed, err := ParseAPDUResponse([]byte{0xDE, 0xAD, 0x91, 0x00})
	if err != nil {
		t.Fatalf("ParseAPDUResponse: %v", err)
	}
	if parsed.IsSuccess() {
		t.Error("wrapped DESFire OK (91 00) must not pass the generic ISO 90 00 check")
	}
}

// --- Full-pipeline tests: the real reader write path over emulated silicon ---
//
// These drive NFCReader.WriteMessageWithResult — capacity check, write,
// read-back verification, retry, structured result, and the lock option —
// against a real pcsc tag backed by an emulator, so the whole write stack
// (reader orchestration + tag driver + "silicon") is exercised together rather
// than each half in isolation.

// TestPipeline_NTAGWriteVerify confirms the reader's read-back verification
// passes against a real NTAG driver + emulator.
func TestPipeline_NTAGWriteVerify(t *testing.T) {
	emu := newNTAGEmulator(DetectedNTAG215)
	tag := newPCSCNtagTag(emu, "04A1B2C3D4E5F6", DetectedNTAG215)
	reader := newWriteTestReader(t, tag)

	result, err := reader.WriteMessageWithResult(textMessage("emulated"), WriteOptions{Overwrite: true, Index: -1})
	if err != nil {
		t.Fatalf("WriteMessageWithResult: %v", err)
	}
	if !result.Verified {
		t.Error("expected the write to be verified by read-back through the real driver")
	}
}

// TestPipeline_NTAGWriteThenLock confirms write+lock through the reader leaves
// the emulated NTAG215 genuinely read-only across both lock ranges.
func TestPipeline_NTAGWriteThenLock(t *testing.T) {
	emu := newNTAGEmulator(DetectedNTAG215)
	tag := newPCSCNtagTag(emu, "04A1B2C3D4E5F6", DetectedNTAG215)
	reader := newWriteTestReader(t, tag)

	result, err := reader.WriteMessageWithResult(textMessage("lock me"), WriteOptions{Overwrite: true, Index: -1, Lock: true})
	if err != nil {
		t.Fatalf("write+lock: %v", err)
	}
	if !result.Verified || !result.Locked {
		t.Fatalf("expected verified+locked, got %+v", result)
	}
	for _, page := range []int{4, 16, 129} {
		if emu.tryWrite(page, []byte{1, 2, 3, 4}) {
			t.Errorf("page %d writable after write+lock", page)
		}
	}
}

// TestPipeline_ClassicWriteVerify drives the reader pipeline against a real
// Classic driver + emulator (key auth + block I/O + verification).
func TestPipeline_ClassicWriteVerify(t *testing.T) {
	emu := newClassicEmulator()
	tag := newPCSCClassicTag(emu, "04112233", DetectedClassic1K)
	reader := newWriteTestReader(t, tag)

	result, err := reader.WriteMessageWithResult(textMessage("classic"), WriteOptions{Overwrite: true, Index: -1})
	if err != nil {
		t.Fatalf("WriteMessageWithResult: %v", err)
	}
	if !result.Verified {
		t.Error("expected verified write through the Classic driver")
	}
}

// TestPipeline_DESFireWriteVerify drives the reader pipeline against a real
// DESFire driver + emulator (select app + NLEN + NDEF + status handling).
func TestPipeline_DESFireWriteVerify(t *testing.T) {
	emu := newDESFireEmulator()
	tag := newPCSCDESFireTag(emu, "04DE5F1RE0")
	reader := newWriteTestReader(t, tag)

	result, err := reader.WriteMessageWithResult(textMessage("desfire"), WriteOptions{Overwrite: true, Index: -1})
	if err != nil {
		t.Fatalf("WriteMessageWithResult: %v", err)
	}
	if !result.Verified {
		t.Error("expected verified write through the DESFire driver")
	}
}

// --- Edge cases: stress the reliability features against real drivers ---

// TestPipeline_NTAGLargePayloadMultiPage writes a payload spanning many pages so
// the page-iteration math and read-back verification are exercised at scale, not
// just for a single page.
func TestPipeline_NTAGLargePayloadMultiPage(t *testing.T) {
	emu := newNTAGEmulator(DetectedNTAG215) // 504-byte capacity
	tag := newPCSCNtagTag(emu, "04A1B2C3D4E5F6", DetectedNTAG215)
	reader := newWriteTestReader(t, tag)

	result, err := reader.WriteMessageWithResult(textMessage(strings.Repeat("a", 200)), WriteOptions{Overwrite: true, Index: -1})
	if err != nil {
		t.Fatalf("multi-page write: %v", err)
	}
	if !result.Verified {
		t.Error("expected multi-page write to verify by read-back")
	}
}

// TestPipeline_ClassicMultiSector writes a payload large enough to span several
// MIFARE Classic sectors, exercising per-sector re-authentication and
// sector-trailer skipping under verification.
func TestPipeline_ClassicMultiSector(t *testing.T) {
	emu := newClassicEmulator()
	tag := newPCSCClassicTag(emu, "04112233", DetectedClassic1K)
	reader := newWriteTestReader(t, tag)

	result, err := reader.WriteMessageWithResult(textMessage(strings.Repeat("y", 120)), WriteOptions{Overwrite: true, Index: -1})
	if err != nil {
		t.Fatalf("multi-sector write: %v", err)
	}
	if !result.Verified {
		t.Error("expected multi-sector write to verify by read-back")
	}
}

// TestPipeline_OversizedWriteRejected confirms a payload larger than the tag's
// capacity is rejected (rather than silently truncated or corrupting the tag).
func TestPipeline_OversizedWriteRejected(t *testing.T) {
	emu := newNTAGEmulator(DetectedNTAG213) // 144-byte capacity
	tag := newPCSCNtagTag(emu, "04A1B2C3D4E5F6", DetectedNTAG213)
	reader := newWriteTestReader(t, tag)

	if _, err := reader.WriteMessageWithResult(textMessage(strings.Repeat("z", 300)), WriteOptions{Overwrite: true, Index: -1}); err == nil {
		t.Error("expected oversized write to be rejected")
	}
}

// TestPipeline_RetryRecoversFromTransientFailure injects a single transient NAK
// and confirms the reader retries and still verifies — proving retry works
// against the real driver, not just a mock.
func TestPipeline_RetryRecoversFromTransientFailure(t *testing.T) {
	emu := newNTAGEmulator(DetectedNTAG215)
	emu.failWrites = 1 // first page write NAKs; the retry should recover
	tag := newPCSCNtagTag(emu, "04A1B2C3D4E5F6", DetectedNTAG215)
	reader := newWriteTestReader(t, tag)

	result, err := reader.WriteMessageWithResult(textMessage("retry me"), WriteOptions{Overwrite: true, Index: -1})
	if err != nil {
		t.Fatalf("expected retry to recover, got: %v", err)
	}
	if !result.Verified {
		t.Error("expected verified after retry")
	}
	if result.Attempts < 2 {
		t.Errorf("expected >=2 attempts after a transient failure, got %d", result.Attempts)
	}
}

// TestPipeline_VerificationCatchesBadWrite makes every write land wrong and
// confirms read-back verification refuses to report success — the core safety
// property of the write pipeline.
func TestPipeline_VerificationCatchesBadWrite(t *testing.T) {
	emu := newNTAGEmulator(DetectedNTAG215)
	emu.corrupt = true // writes store inverted bytes
	tag := newPCSCNtagTag(emu, "04A1B2C3D4E5F6", DetectedNTAG215)
	reader := newWriteTestReader(t, tag)

	if _, err := reader.WriteMessageWithResult(textMessage("oops"), WriteOptions{Overwrite: true, Index: -1}); err == nil {
		t.Error("verification should have caught the corrupted write and returned an error")
	}
}

// TestPipeline_WriteAfterLockFails confirms that once a tag is locked, a
// subsequent write through the reader fails rather than silently no-op'ing.
func TestPipeline_WriteAfterLockFails(t *testing.T) {
	emu := newNTAGEmulator(DetectedNTAG215)
	tag := newPCSCNtagTag(emu, "04A1B2C3D4E5F6", DetectedNTAG215)
	reader := newWriteTestReader(t, tag)

	if _, err := reader.WriteMessageWithResult(textMessage("first"), WriteOptions{Overwrite: true, Index: -1, Lock: true}); err != nil {
		t.Fatalf("write+lock: %v", err)
	}
	if _, err := reader.WriteMessageWithResult(textMessage("second"), WriteOptions{Overwrite: true, Index: -1}); err == nil {
		t.Error("expected a write to a locked tag to fail")
	}
}

// TestPipeline_EraseThroughReader writes data, erases via the reader, and
// confirms the erase is verified.
func TestPipeline_EraseThroughReader(t *testing.T) {
	emu := newNTAGEmulator(DetectedNTAG215)
	tag := newPCSCNtagTag(emu, "04A1B2C3D4E5F6", DetectedNTAG215)
	reader := newWriteTestReader(t, tag)

	if _, err := reader.WriteMessageWithResult(textMessage("data"), WriteOptions{Overwrite: true, Index: -1}); err != nil {
		t.Fatalf("initial write: %v", err)
	}
	result, err := reader.EraseCard()
	if err != nil {
		t.Fatalf("EraseCard: %v", err)
	}
	if !result.Verified {
		t.Error("expected erase to be verified")
	}
}

// TestPipeline_AppendRecord exercises the partial-update (append) path: a second
// record is merged into the existing message rather than overwriting it.
func TestPipeline_AppendRecord(t *testing.T) {
	emu := newNTAGEmulator(DetectedNTAG215)
	tag := newPCSCNtagTag(emu, "04A1B2C3D4E5F6", DetectedNTAG215)
	reader := newWriteTestReader(t, tag)

	if _, err := reader.WriteMessageWithResult(textMessage("first"), WriteOptions{Overwrite: true, Index: -1}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if _, err := reader.WriteMessageWithResult(textMessage("second"), WriteOptions{Overwrite: false, Index: -1}); err != nil {
		t.Fatalf("append write: %v", err)
	}

	raw, err := tag.ReadData()
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	msg, err := DecodeNDEF(raw)
	if err != nil {
		t.Fatalf("decode NDEF: %v", err)
	}
	if got := len(msg.Records()); got != 2 {
		t.Errorf("expected 2 records after append, got %d", got)
	}
}
