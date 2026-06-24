package nfc

import (
	"bytes"
	"testing"
)

// memEmulator is an in-memory NTAG/Ultralight tag that speaks the PC/SC
// pseudo-APDU wire protocol the real tag I/O emits (READ = FF B0 00 <page> <Le>,
// WRITE = FF D6 00 <page> <Lc> <data>), returning real SW1/SW2 framing. It
// enforces the page memory map and lock-byte rules from the NXP NTAG21x /
// Ultralight datasheets, so the production pcscNtagTag / pcscUltralightTag logic
// (page math, TLV, lock bytes) runs against emulated silicon — no hardware.
//
// Lock granularity is modeled per the datasheets: the static lock bytes
// (page 2, bytes 2-3) cover pages 3-15, and the NTAG dynamic lock bytes cover
// pages >=16 in 16-page blocks. The block granularity is a datasheet-derived
// model and should be cross-checked against real hardware.
type memEmulator struct {
	pages       [][4]byte
	dynLockPage int // 0 means no dynamic lock area (original Ultralight)
	present     bool
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

func (e *memEmulator) IsCardPresent() bool { return e.present }

// Transceive decodes the PC/SC pseudo-APDU and applies memory/lock rules.
func (e *memEmulator) Transceive(cmd []byte) ([]byte, error) {
	if len(cmd) < 5 || cmd[0] != CLAPCSC {
		return emuFail(), nil
	}
	ins, page := cmd[1], cmd[3]
	switch ins {
	case INSReadBinary: // FF B0 00 <page> <Le>
		data, ok := e.read(int(page), int(cmd[4]))
		if !ok {
			return emuFail(), nil
		}
		return append(data, SW1Success, SW2Success), nil
	case INSUpdateBin: // FF D6 00 <page> <Lc> <data...>
		lc := int(cmd[4])
		if len(cmd) < 5+lc {
			return emuFail(), nil
		}
		if !e.write(int(page), cmd[5:5+lc]) {
			return emuFail(), nil
		}
		return []byte{SW1Success, SW2Success}, nil
	}
	return emuFail(), nil
}

// emuFail mimics the reader returning a non-success status (e.g. on a NAK'd
// write to a locked page).
func emuFail() []byte { return []byte{0x63, 0x00} }

// read returns le bytes starting at the given page. Reads are always permitted
// (no read-password is modeled).
func (e *memEmulator) read(page, le int) ([]byte, bool) {
	out := make([]byte, 0, le+4)
	for p := page; len(out) < le; p++ {
		if p < 0 || p >= len(e.pages) {
			return nil, false
		}
		out = append(out, e.pages[p][:]...)
	}
	return out[:le], true
}

// write applies a 4-byte page write, honoring read-only and lock-byte rules.
func (e *memEmulator) write(page int, data []byte) bool {
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
		if e.locked(page) {
			return false
		}
		copy(e.pages[page][:], data)
		return true
	}
}

// locked reports whether a page is write-locked given the current lock bytes.
func (e *memEmulator) locked(page int) bool {
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
		if e.write(page, []byte{1, 2, 3, 4}) {
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
			if e.write(page, []byte{1, 2, 3, 4}) {
				t.Errorf("model %d: page %d writable after lock (expected locked)", tc.model, page)
			}
		}
	}
}

// classicEmulator is an in-memory MIFARE Classic 1K that speaks the PC/SC
// pseudo-APDU protocol the real pcscClassicTag emits: load key (FF 82),
// authenticate (FF 86), read block (FF B0), update block (FF D6). It models the
// 16-sector / 4-block layout with per-sector key authentication enforced before
// block access, so the production auth + block I/O logic runs against it.
type classicEmulator struct {
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

func (e *classicEmulator) IsCardPresent() bool { return e.present }

func (e *classicEmulator) Transceive(cmd []byte) ([]byte, error) {
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
// key A (0x60) or key B (0x61).
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
