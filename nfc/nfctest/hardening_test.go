package nfctest

import (
	"bytes"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// TestClassicEmulator_ReadDataWith0xFEInPayload is a regression test for a read
// truncation bug: the Classic driver used to stop reading at the first 0xFE byte
// seen in any block. 0xFE (the TLV terminator value) occurs naturally inside
// NDEF payloads, so a multi-block message whose payload contained one was
// silently truncated and failed to parse. The driver must instead read exactly
// as many blocks as the length-prefixed TLV requires.
func TestClassicEmulator_ReadDataWith0xFEInPayload(t *testing.T) {
	// 48 bytes (spans 3 data blocks) with 0xFE appearing early and repeatedly.
	payload := bytes.Repeat([]byte{0x00, 0xFE, 0x11, 0x22}, 12)

	e := newClassicEmulator()
	tag := nfc.NewEmulatedTag(e, "04112233", nfc.DetectedClassic1K)

	if err := tag.WriteData(payload); err != nil {
		t.Fatalf("WriteData: %v", err)
	}
	got, err := tag.ReadData()
	if err != nil {
		t.Fatalf("ReadData with 0xFE in payload: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Errorf("0xFE payload truncated:\n got  % X\n want % X", got, payload)
	}
}

// TestClassicEmulator_LockReportedUnsupported verifies the Classic driver no
// longer claims it can be made read-only while failing the actual lock. Locking
// is unimplemented for MIFARE Classic, so it must report that honestly through
// every surface: the capabilities, CanMakeReadOnly, and a typed not-supported
// error from MakeReadOnly.
func TestClassicEmulator_LockReportedUnsupported(t *testing.T) {
	e := newClassicEmulator()
	tag := nfc.NewEmulatedTag(e, "04112233", nfc.DetectedClassic1K)

	if nfc.GetTagCapabilities(tag).CanLock {
		t.Error("capabilities should report Classic CanLock=false (locking unimplemented)")
	}

	locker, ok := tag.(nfc.TagLocker)
	if !ok {
		t.Fatal("Classic tag should implement TagLocker")
	}
	can, err := locker.CanMakeReadOnly()
	if err != nil {
		t.Fatalf("CanMakeReadOnly: %v", err)
	}
	if can {
		t.Error("CanMakeReadOnly should be false for MIFARE Classic")
	}
	if err := locker.MakeReadOnly(); !nfc.IsNotSupportedError(err) {
		t.Errorf("MakeReadOnly should return a not-supported error, got %v", err)
	}
}

// TestUltralightCEmulator_LockMakesAllUserPagesReadOnly is a regression test for
// an incomplete lock: the Ultralight driver used to set only the static lock
// bytes (page 2), which cover pages 3-15. An Ultralight C has 48 pages, so pages
// 16-47 (governed by the dynamic lock bytes at page 0x28) stayed writable after
// a "lock". A proper lock must make every user page read-only.
func TestUltralightCEmulator_LockMakesAllUserPagesReadOnly(t *testing.T) {
	e := newUltralightCEmulator()
	tag := nfc.NewEmulatedTag(e, "04AABBCCDDEEFF", nfc.DetectedUltralightC)

	if err := tag.MakeReadOnly(); err != nil {
		t.Fatalf("MakeReadOnly: %v", err)
	}

	// Pages below 16 are covered by the static lock; 16 and above only become
	// read-only once the dynamic lock bytes are set.
	for _, page := range []int{4, 8, 15, 16, 32, 47} {
		if e.tryWrite(page, []byte{1, 2, 3, 4}) {
			t.Errorf("page %d writable after Ultralight C lock", page)
		}
	}
}

// TestUltralightCEmulator_WriteReadRoundTrip sanity-checks that the new UL-C
// emulator backs the real driver for normal NDEF I/O before locking.
func TestUltralightCEmulator_WriteReadRoundTrip(t *testing.T) {
	e := newUltralightCEmulator()
	tag := nfc.NewEmulatedTag(e, "04AABBCCDDEEFF", nfc.DetectedUltralightC)

	if err := tag.WriteData(sampleNDEF); err != nil {
		t.Fatalf("WriteData: %v", err)
	}
	got, err := tag.ReadData()
	if err != nil {
		t.Fatalf("ReadData: %v", err)
	}
	if !bytes.Equal(got, sampleNDEF) {
		t.Errorf("round-trip mismatch: % X", got)
	}
}
