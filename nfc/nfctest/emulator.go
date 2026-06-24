// Package nfctest provides in-memory NFC tag emulators and a high-level façade
// for testing NFC code without hardware.
//
// Two layers:
//
//   - Low-level emulators speak the real PC/SC and DESFire wire protocols and
//     back the production tag drivers (via nfc.NewEmulatedTag), so real driver
//     I/O — page/block math, TLV framing, lock bytes, status words, frame
//     chaining — runs against emulated silicon.
//
//   - A façade (EmulatedCard, EmulatedReader) lets a test declare a card with
//     NDEF entries and "present" it to a reader as if it were real hardware,
//     keeping the low-level details out of test code:
//
//     reader := nfctest.NewEmulatedReader(t, nfctest.NTAG215("04A1B2C3").WithText("hello"))
//     res, _ := reader.WriteMessageWithResult(msg, nfc.WriteOptions{Overwrite: true, Index: -1})
//
// Fidelity is modeled from the NXP / DESFire datasheets and wants a real-tag
// cross-check; the emulators validate self-consistency and the driver logic,
// not silicon.
package nfctest

import (
	"fmt"
	"sync"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// TB is the subset of *testing.T the façade needs, so this non-test package
// doesn't import "testing".
type TB interface {
	Helper()
	Fatalf(format string, args ...any)
	Cleanup(func())
}

// DESFire native status + framing constants (datasheet-modeled).
const (
	dfStatusOK              = 0x00
	dfStatusAdditionalFrame = 0xAF
	dfFrameData             = 59
)

// emuFail mimics the reader returning a non-success status (e.g. a NAK'd write
// to a locked page).
func emuFail() []byte { return []byte{0x63, 0x00} }

// memEmulator is an in-memory NTAG/Ultralight tag speaking the PC/SC pseudo-APDU
// protocol (READ = FF B0, WRITE = FF D6) with static/dynamic lock-byte rules.
// Safe for concurrent use (the reader poll touches it alongside writes).
type memEmulator struct {
	mu          sync.Mutex
	pages       [][4]byte
	dynLockPage int // 0 = no dynamic lock area (original Ultralight)
	present     bool

	failWrites int  // NAK the next N writes (retry testing)
	corrupt    bool // store inverted bytes (verification testing)
}

func newNTAGEmulator(model nfc.DetectedTagType) *memEmulator {
	maxPages, dynLock := 135, 130 // NTAG215
	switch model {
	case nfc.DetectedNTAG213:
		maxPages, dynLock = 45, 40
	case nfc.DetectedNTAG216:
		maxPages, dynLock = 231, 226
	}
	return &memEmulator{pages: make([][4]byte, maxPages), dynLockPage: dynLock, present: true}
}

func newUltralightEmulator() *memEmulator {
	return &memEmulator{pages: make([][4]byte, 16), present: true}
}

func newUltralightCEmulator() *memEmulator {
	// Ultralight C: 48 pages, with dynamic lock bytes at page 0x28 (40)
	// governing the user pages above the static-lock range.
	return &memEmulator{pages: make([][4]byte, 48), dynLockPage: 0x28, present: true}
}

func (e *memEmulator) IsCardPresent() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.present
}

func (e *memEmulator) Transceive(cmd []byte) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(cmd) < 5 || cmd[0] != nfc.CLAPCSC {
		return emuFail(), nil
	}
	ins, page := cmd[1], cmd[3]
	switch ins {
	case nfc.INSReadBinary:
		data, ok := e.readMem(int(page), int(cmd[4]))
		if !ok {
			return emuFail(), nil
		}
		return append(data, nfc.SW1Success, nfc.SW2Success), nil
	case nfc.INSUpdateBin:
		lc := int(cmd[4])
		if len(cmd) < 5+lc {
			return emuFail(), nil
		}
		if e.failWrites > 0 {
			e.failWrites--
			return emuFail(), nil
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
		return []byte{nfc.SW1Success, nfc.SW2Success}, nil
	}
	return emuFail(), nil
}

// tryWrite attempts a raw page write under the lock and reports success.
func (e *memEmulator) tryWrite(page int, data []byte) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.writeMem(page, data)
}

// readMem/writeMem/lockedMem assume the caller holds e.mu.
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

func (e *memEmulator) writeMem(page int, data []byte) bool {
	if page < 0 || page >= len(e.pages) || len(data) != 4 {
		return false
	}
	switch {
	case page == 0 || page == 1:
		return false // UID / serial: read-only
	case page == 2:
		e.pages[2][2] |= data[2] // static lock bytes are OR-only
		e.pages[2][3] |= data[3]
		return true
	case e.dynLockPage != 0 && page == e.dynLockPage:
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

func (e *memEmulator) lockedMem(page int) bool {
	lock0, lock1 := e.pages[2][2], e.pages[2][3]
	switch {
	case page == 3:
		return lock0&(1<<3) != 0
	case page >= 4 && page <= 7:
		return lock0&(1<<uint(page)) != 0
	case page >= 8 && page <= 15:
		return lock1&(1<<uint(page-8)) != 0
	case page >= 16 && e.dynLockPage != 0:
		block := (page - 16) / 16
		byteIdx, bitIdx := block/8, block%8
		if byteIdx > 2 {
			return false
		}
		return e.pages[e.dynLockPage][byteIdx]&(1<<uint(bitIdx)) != 0
	}
	return false
}

// classicEmulator is an in-memory Classic 1K speaking load-key/authenticate/
// read/update with per-sector key enforcement AND sector-trailer access-bit
// enforcement: reads/writes are checked against the access conditions for the
// authenticated key, and a trailer written with inconsistent access bits bricks
// the sector (no key can access it), mirroring real silicon. This lets the NDEF
// formatting and recovery paths be validated in software.
type classicEmulator struct {
	mu        sync.Mutex
	blocks    [][16]byte
	loadedKey []byte
	authed    int          // authenticated sector, -1 = none
	authedKey byte         // key type used for the current auth (MIFAREKeyA/B)
	bricked   map[int]bool // sectors invalidated by inconsistent access bits
	present   bool
}

func newClassicEmulator() *classicEmulator {
	e := &classicEmulator{
		blocks:  make([][16]byte, 64),
		authed:  -1,
		bricked: make(map[int]bool),
		present: true,
	}
	for sector := 0; sector < 16; sector++ {
		tr := sector*4 + 3
		for i := 0; i < 6; i++ {
			e.blocks[tr][i] = 0xFF    // key A
			e.blocks[tr][10+i] = 0xFF // key B
		}
		// Transport access bits FF 07 80 (data blocks read/write with either
		// key; trailer rewritable with key A), GPB 0x69.
		e.blocks[tr][6], e.blocks[tr][7] = 0xFF, 0x07
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

	if len(cmd) < 5 || cmd[0] != nfc.CLAPCSC {
		return emuFail(), nil
	}
	switch cmd[1] {
	case nfc.INSLoadKey:
		lc := int(cmd[4])
		if len(cmd) < 5+lc {
			return emuFail(), nil
		}
		e.loadedKey = append([]byte(nil), cmd[5:5+lc]...)
		return []byte{nfc.SW1Success, nfc.SW2Success}, nil
	case nfc.INSAuth:
		if len(cmd) < 10 {
			return emuFail(), nil
		}
		if e.authenticate(int(cmd[7]), cmd[8]) {
			return []byte{nfc.SW1Success, nfc.SW2Success}, nil
		}
		return emuFail(), nil
	case nfc.INSReadBinary:
		block := int(cmd[3])
		if block < 0 || block >= len(e.blocks) || !e.isAuthedFor(block) {
			return emuFail(), nil
		}
		if !e.accessAllows(block, false) {
			return emuFail(), nil
		}
		return append(append([]byte(nil), e.blocks[block][:]...), nfc.SW1Success, nfc.SW2Success), nil
	case nfc.INSUpdateBin:
		block, lc := int(cmd[3]), int(cmd[4])
		if lc != 16 || len(cmd) < 5+lc || block <= 0 || block >= len(e.blocks) || !e.isAuthedFor(block) {
			return emuFail(), nil
		}
		if !e.accessAllows(block, true) {
			return emuFail(), nil
		}
		copy(e.blocks[block][:], cmd[5:5+lc])
		// A trailer written with inconsistent access bits invalidates the
		// sector on real silicon — model that so formatting mistakes are
		// observable in software.
		if block%4 == 3 {
			if _, ok := accessConditions(e.blocks[block]); !ok {
				e.bricked[block/4] = true
			}
		}
		return []byte{nfc.SW1Success, nfc.SW2Success}, nil
	}
	return emuFail(), nil
}

// block returns a copy of the given absolute block, holding the lock so tests
// can inspect emulator state without racing the reader's poll.
func (e *classicEmulator) block(i int) [16]byte {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.blocks[i]
}

// rekeyAll sets Key A and Key B of every sector trailer to the given 6-byte key,
// simulating a card provisioned with non-default keys.
func (e *classicEmulator) rekeyAll(key []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for s := 0; s < 16; s++ {
		tr := s*4 + 3
		copy(e.blocks[tr][0:6], key)
		copy(e.blocks[tr][10:16], key)
	}
}

// rekeySector sets Key A and Key B of a single sector trailer to the given
// 6-byte key, simulating a card where one sector uses an unknown key.
func (e *classicEmulator) rekeySector(sector int, key []byte) {
	e.mu.Lock()
	defer e.mu.Unlock()
	tr := sector*4 + 3
	copy(e.blocks[tr][0:6], key)
	copy(e.blocks[tr][10:16], key)
}

func (e *classicEmulator) authenticate(block int, keyType byte) bool {
	sector := block / 4
	tr := sector*4 + 3
	if e.loadedKey == nil || tr >= len(e.blocks) || e.bricked[sector] {
		return false
	}
	stored := e.blocks[tr][0:6]
	if keyType == nfc.MIFAREKeyB {
		stored = e.blocks[tr][10:16]
	}
	if string(e.loadedKey) == string(stored) {
		e.authed = sector
		e.authedKey = keyType
		return true
	}
	return false
}

func (e *classicEmulator) isAuthedFor(block int) bool { return e.authed == block/4 }

// accessAllows reports whether the current authentication permits the given
// read (write=false) or write (write=true) of an absolute block, per the
// sector trailer's access bits. A sector whose access bits are inconsistent (or
// already bricked) denies everything.
func (e *classicEmulator) accessAllows(block int, write bool) bool {
	sector := block / 4
	if e.bricked[sector] {
		return false
	}
	cond, ok := accessConditions(e.blocks[sector*4+3])
	if !ok {
		return false
	}
	keyB := e.authedKey == nfc.MIFAREKeyB
	ci := condIndex(cond[block%4])
	if block%4 == 3 {
		// Trailer: model the permission to rewrite it (Key A write column).
		if write {
			return classicTrailerWritePerm(ci).allows(keyB)
		}
		return true // reading the trailer is allowed (keys read back as stored)
	}
	if write {
		return classicDataWritePerm(ci).allows(keyB)
	}
	return classicDataReadPerm(ci).allows(keyB)
}

// classicPerm models who may perform a MIFARE Classic operation under a given
// access condition.
type classicPerm int

const (
	permNever classicPerm = iota
	permKeyA
	permKeyB
	permKeyAB
)

func (p classicPerm) allows(keyB bool) bool {
	switch p {
	case permKeyAB:
		return true
	case permKeyA:
		return !keyB
	case permKeyB:
		return keyB
	default:
		return false
	}
}

// accessConditions parses the (C1,C2,C3) triple for each of the 4 blocks in a
// sector from the trailer's access bytes (6-8). ok is false if the bytes fail
// the complement-integrity check, i.e. an invalid/bricked sector.
// Layout: byte6 = [~C2 | ~C1], byte7 = [C1 | ~C3], byte8 = [C3 | C2].
func accessConditions(trailer [16]byte) (cond [4][3]byte, ok bool) {
	b6, b7, b8 := trailer[6], trailer[7], trailer[8]
	c1 := (b7 >> 4) & 0x0F
	c2 := b8 & 0x0F
	c3 := (b8 >> 4) & 0x0F
	if (b6&0x0F) != (^c1&0x0F) || ((b6>>4)&0x0F) != (^c2&0x0F) || (b7&0x0F) != (^c3&0x0F) {
		return cond, false
	}
	for i := 0; i < 4; i++ {
		cond[i][0] = (c1 >> uint(i)) & 1
		cond[i][1] = (c2 >> uint(i)) & 1
		cond[i][2] = (c3 >> uint(i)) & 1
	}
	return cond, true
}

// condIndex packs a (C1,C2,C3) triple into a 3-bit index C1<<2|C2<<1|C3.
func condIndex(c [3]byte) byte { return c[0]<<2 | c[1]<<1 | c[2] }

// classicDataReadPerm / classicDataWritePerm / classicTrailerWritePerm encode
// the MIFARE Classic access-condition tables (NXP MF1S50yyX datasheet).
func classicDataReadPerm(ci byte) classicPerm {
	switch ci {
	case 0b000, 0b010, 0b100, 0b110, 0b001:
		return permKeyAB
	case 0b011, 0b101:
		return permKeyB
	default: // 111
		return permNever
	}
}

func classicDataWritePerm(ci byte) classicPerm {
	switch ci {
	case 0b000:
		return permKeyAB
	case 0b100, 0b110, 0b011:
		return permKeyB
	default: // 010, 001, 101, 111
		return permNever
	}
}

// classicTrailerWritePerm models the permission to rewrite a sector trailer,
// using the Key A write column (formatting always rewrites Key A).
func classicTrailerWritePerm(ci byte) classicPerm {
	switch ci {
	case 0b000, 0b001:
		return permKeyA
	case 0b100, 0b011:
		return permKeyB
	default: // 010, 110, 101, 111
		return permNever
	}
}

// desfireEmulator is an in-memory DESFire NDEF tag speaking the ISO-wrapped
// native protocol with real 91-xx status words and frame chaining.
type desfireEmulator struct {
	mu           sync.Mutex
	selectedNDEF bool
	file2        []byte
	present      bool

	readOff, readRemain   int
	writeOff, writeRemain int
}

func newDESFireEmulator() *desfireEmulator {
	return &desfireEmulator{file2: make([]byte, 256), present: true}
}

func (e *desfireEmulator) IsCardPresent() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.present
}

func dfResp(data []byte, status byte) []byte {
	return append(append([]byte(nil), data...), 0x91, status)
}

func (e *desfireEmulator) Transceive(cmd []byte) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if len(cmd) < 5 || cmd[0] != nfc.CLADESFire {
		return dfResp(nil, 0xA0), nil
	}
	lc := int(cmd[4])
	if len(cmd) < 5+lc {
		return dfResp(nil, 0x7E), nil
	}
	body := cmd[5 : 5+lc]
	switch cmd[1] {
	case nfc.DFCmdSelectApplication:
		if len(body) == 3 && body[0] == 0x00 && body[1] == 0x00 && body[2] == 0x01 {
			e.selectedNDEF = true
			return dfResp(nil, dfStatusOK), nil
		}
		return dfResp(nil, 0xA0), nil
	case nfc.DFCmdReadData:
		off, length, ok := e.fileRange(body)
		if !ok {
			return dfResp(nil, 0xBE), nil
		}
		e.readOff, e.readRemain = off, length
		return e.nextReadFrame(), nil
	case nfc.DFCmdWriteData:
		off, total, ok := e.fileRange(body)
		if !ok {
			return dfResp(nil, 0xBE), nil
		}
		n := len(body) - 7
		if n > total {
			n = total
		}
		copy(e.file2[off:off+n], body[7:7+n])
		e.writeOff, e.writeRemain = off+n, total-n
		if e.writeRemain > 0 {
			return dfResp(nil, dfStatusAdditionalFrame), nil
		}
		return dfResp(nil, dfStatusOK), nil
	case nfc.DFCmdAdditionalFrame:
		if e.writeRemain > 0 {
			n := len(body)
			if n > e.writeRemain {
				n = e.writeRemain
			}
			copy(e.file2[e.writeOff:e.writeOff+n], body[:n])
			e.writeOff += n
			e.writeRemain -= n
			if e.writeRemain > 0 {
				return dfResp(nil, dfStatusAdditionalFrame), nil
			}
			return dfResp(nil, dfStatusOK), nil
		}
		if e.readRemain > 0 {
			return e.nextReadFrame(), nil
		}
		return dfResp(nil, 0xA0), nil
	}
	return dfResp(nil, 0xA0), nil
}

func (e *desfireEmulator) nextReadFrame() []byte {
	n := e.readRemain
	if n > dfFrameData {
		n = dfFrameData
	}
	chunk := e.file2[e.readOff : e.readOff+n]
	e.readOff += n
	e.readRemain -= n
	if e.readRemain > 0 {
		return dfResp(chunk, dfStatusAdditionalFrame)
	}
	return dfResp(chunk, dfStatusOK)
}

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

// EmulatedCard is a tag of a given kind, optionally preloaded with NDEF content,
// backed by an emulator running the production driver.
type EmulatedCard struct {
	uid       string
	kind      nfc.DetectedTagType
	transport nfc.CardTransport
	tag       nfc.Tag
}

func newCard(kind nfc.DetectedTagType, uid string, transport nfc.CardTransport) *EmulatedCard {
	return &EmulatedCard{
		uid:       uid,
		kind:      kind,
		transport: transport,
		tag:       nfc.NewEmulatedTag(transport, uid, kind),
	}
}

// NTAG213/NTAG215/NTAG216/Ultralight/Classic1K/DESFire construct a blank card of
// that kind. Chain WithText/WithURI/WithRecords/WithNDEF to preload content.
func NTAG213(uid string) *EmulatedCard {
	return newCard(nfc.DetectedNTAG213, uid, newNTAGEmulator(nfc.DetectedNTAG213))
}
func NTAG215(uid string) *EmulatedCard {
	return newCard(nfc.DetectedNTAG215, uid, newNTAGEmulator(nfc.DetectedNTAG215))
}
func NTAG216(uid string) *EmulatedCard {
	return newCard(nfc.DetectedNTAG216, uid, newNTAGEmulator(nfc.DetectedNTAG216))
}
func Ultralight(uid string) *EmulatedCard {
	return newCard(nfc.DetectedUltralight, uid, newUltralightEmulator())
}
func UltralightC(uid string) *EmulatedCard {
	return newCard(nfc.DetectedUltralightC, uid, newUltralightCEmulator())
}
func Classic1K(uid string) *EmulatedCard {
	return newCard(nfc.DetectedClassic1K, uid, newClassicEmulator())
}
func DESFire(uid string) *EmulatedCard {
	return newCard(nfc.DetectedDESFire, uid, newDESFireEmulator())
}

// UID returns the card's UID.
func (c *EmulatedCard) UID() string { return c.uid }

// Tag returns the underlying driver-backed tag (escape hatch for low-level use).
func (c *EmulatedCard) Tag() nfc.Tag { return c.tag }

// WithRecords preloads the card with an NDEF message built from the records,
// written through the real driver into the emulator. Panics on failure (the
// payload doesn't fit, etc.) so test setup fails loudly.
func (c *EmulatedCard) WithRecords(records ...nfc.NDEFRecordBuilder) *EmulatedCard {
	msg, err := (&nfc.NDEFMessageBuilder{Records: records}).Build()
	if err != nil {
		panic(fmt.Sprintf("nfctest: build NDEF for %s: %v", c.uid, err))
	}
	return c.WithNDEF(msg)
}

// WithNDEF preloads the card with an already-built NDEF message.
func (c *EmulatedCard) WithNDEF(msg *nfc.NDEFMessage) *EmulatedCard {
	data, err := msg.Encode()
	if err != nil {
		panic(fmt.Sprintf("nfctest: encode NDEF for %s: %v", c.uid, err))
	}
	if err := c.tag.WriteData(data); err != nil {
		panic(fmt.Sprintf("nfctest: preload %s: %v", c.uid, err))
	}
	return c
}

// WithText preloads a single text record.
func (c *EmulatedCard) WithText(text string) *EmulatedCard {
	return c.WithRecords(&nfc.NDEFText{Content: text, Language: "en"})
}

// WithURI preloads a single URI record.
func (c *EmulatedCard) WithURI(uri string) *EmulatedCard {
	return c.WithRecords(&nfc.NDEFURI{Content: uri})
}

// Locked makes the card read-only (where the tag kind supports it). Panics if
// locking fails.
func (c *EmulatedCard) Locked() *EmulatedCard {
	if err := c.tag.MakeReadOnly(); err != nil {
		panic(fmt.Sprintf("nfctest: lock %s: %v", c.uid, err))
	}
	return c
}

// EmulatedReader is an NFCReader wired to an emulated device, onto which cards
// can be presented and removed as if tapped on a real reader.
type EmulatedReader struct {
	*nfc.NFCReader
	dev   *nfc.MockDevice
	mu    sync.Mutex
	cards []*EmulatedCard
}

// NewEmulatedReader builds a reader with the given cards already in the field.
// The reader is closed automatically when the test ends.
func NewEmulatedReader(tb TB, cards ...*EmulatedCard) *EmulatedReader {
	tb.Helper()
	mgr := nfc.NewMockManager()
	dev := nfc.NewMockDevice()
	mgr.MockDevice = dev

	r := &EmulatedReader{dev: dev, cards: append([]*EmulatedCard(nil), cards...)}
	r.sync()

	reader, err := nfc.NewNFCReader("mock:usb:001", mgr, 5*time.Second)
	if err != nil {
		tb.Fatalf("nfctest: create reader: %v", err)
	}
	r.NFCReader = reader
	tb.Cleanup(reader.Close)

	// Give the reader time to establish the initial connection.
	time.Sleep(100 * time.Millisecond)
	return r
}

// Present taps additional cards onto the reader.
func (r *EmulatedReader) Present(cards ...*EmulatedCard) {
	r.mu.Lock()
	r.cards = append(r.cards, cards...)
	r.mu.Unlock()
	r.sync()
}

// Remove takes the card with the given UID off the reader.
func (r *EmulatedReader) Remove(uid string) {
	r.mu.Lock()
	kept := r.cards[:0]
	for _, c := range r.cards {
		if c.uid != uid {
			kept = append(kept, c)
		}
	}
	r.cards = kept
	r.mu.Unlock()
	r.sync()
}

func (r *EmulatedReader) sync() {
	r.mu.Lock()
	tags := make([]nfc.Tag, len(r.cards))
	for i, c := range r.cards {
		tags[i] = c.tag
	}
	r.mu.Unlock()
	r.dev.SetTags(tags)
}
