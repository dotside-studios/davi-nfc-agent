package nfc

import "fmt"

// MIFARE Classic NFC-Forum formatting.
//
// To make a factory-blank MIFARE Classic 1K readable as an NFC tag by phones,
// the card must be formatted per the NFC Forum Type "MIFARE Classic" mapping
// (NXP AN1304 / AN10787):
//
//   - Sector 0 holds the MIFARE Application Directory (MAD), marking which
//     sectors contain NDEF (AID 0x03E1), and its trailer uses the MAD key A.
//   - Each data sector's trailer is switched to the NFC Forum public key A
//     (0xD3F7...) with the standard NDEF access bits, so phones can read it.
//
// WARNING: this rewrites sector trailers (keys + access bits). On real hardware
// a structurally invalid or wrongly-configured trailer can permanently lock a
// sector. Two safeguards keep this RECOVERABLE rather than irreversible:
//
//   - Every trailer is validated before being written (validateNDEFTrailer):
//     the access bits must be internally consistent (the card stores each
//     condition bit with its complement; a mismatch invalidates the sector),
//     and the access condition must keep the trailer rewritable with Key B.
//   - Key B is left at the known factory value (0xFF). Combined with the
//     trailer-writable-with-Key-B access condition, any misconfiguration can be
//     undone by authenticating with Key B and rewriting the trailer.
//
// The byte values below are the canonical NDEF-format constants used by
// established tools; this path is gated behind WriteOptions.ForceInitialize.
// The test emulator (nfctest) enforces access-bit semantics and models sector
// bricking, so the formatting and recovery logic is validated in software; a
// final confirmation that a phone reads the formatted tag is still worthwhile.
const (
	// NFC Forum NDEF AID as stored in the MAD (low byte, high byte).
	madAIDNDEFLo = 0x03
	madAIDNDEFHi = 0xE1
	// MAD info byte: MAD version 1, no publisher sector.
	madInfoByte = 0x01
)

// Sector trailers are 16 bytes: KeyA(6) | AccessBits(3) | GPB(1) | KeyB(6).
var (
	// madSectorTrailer configures sector 0: MAD key A, MAD access bits, GPB 0xC1.
	madSectorTrailer = []byte{
		0xA0, 0xA1, 0xA2, 0xA3, 0xA4, 0xA5, // Key A = MAD key
		0x78, 0x77, 0x88, // access bits (MAD)
		0xC1,                               // GPB: MAD v1 present, multi-application
		0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, // Key B
	}
	// ndefSectorTrailer configures a data sector: NFC Forum key A, NDEF access
	// bits, GPB 0x40.
	ndefSectorTrailer = []byte{
		0xD3, 0xF7, 0xD3, 0xF7, 0xD3, 0xF7, // Key A = NFC Forum public key
		0x7F, 0x07, 0x88, // access bits (NDEF read/write)
		0x40,                               // GPB: NDEF
		0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, // Key B
	}
)

// WriteDataWithOptions implements AdvancedWriter. When ForceInitialize is set it
// formats the card for NDEF (writing the MAD and switching sector trailers to
// the NFC Forum configuration) before writing the data; otherwise it behaves
// like WriteData.
func (t *pcscClassicTag) WriteDataWithOptions(data []byte, opts TagWriteOptions) error {
	if opts.ForceInitialize {
		return t.formatAndWriteNDEF(data)
	}
	return t.WriteData(data)
}

// formatAndWriteNDEF formats a blank/transport MIFARE Classic 1K as an NDEF tag
// and writes the given NDEF message. It authenticates each sector with the
// configured/default keys (factory 0xFF on a blank card), writes the data, then
// switches the trailer to the NFC Forum configuration.
func (t *pcscClassicTag) formatAndWriteNDEF(data []byte) error {
	if t.is4K {
		// 4K needs a second directory (MAD2) for sectors 16-39; not implemented.
		return NewNotSupportedError("ForceInitialize formatting for MIFARE Classic 4K")
	}

	tlv := TLVEncode(data, TLVNDEF)
	for len(tlv)%16 != 0 {
		tlv = append(tlv, 0x00)
	}

	const dataSectors = 15 // sectors 1..15 on a 1K card (sector 0 holds the MAD)
	const dataBlocksPerSector = 3
	capacity := dataSectors * dataBlocksPerSector * 16
	if len(tlv) > capacity {
		return NewCapacityExceededError("FormatNDEF", t.uid, len(tlv), capacity)
	}

	// Safety: never write a sector trailer that could brick the sector. Both
	// trailers must be internally consistent and must keep the trailer
	// rewritable with Key B (so a misconfiguration is recoverable).
	if err := validateNDEFTrailer(madSectorTrailer); err != nil {
		return fmt.Errorf("formatNDEF: refusing to write MAD trailer: %w", err)
	}
	if err := validateNDEFTrailer(ndefSectorTrailer); err != nil {
		return fmt.Errorf("formatNDEF: refusing to write NDEF trailer: %w", err)
	}

	// Preflight: confirm every sector we are about to rewrite is accessible with
	// the current keys before modifying anything, so an inaccessible card aborts
	// cleanly instead of being left half-formatted.
	for sector := 0; sector <= dataSectors; sector++ {
		if err := t.authenticateSector(sector); err != nil {
			return fmt.Errorf("formatNDEF (UID: %s): sector %d not accessible, aborting before any write: %w", t.uid, sector, err)
		}
	}

	// 1. Write the MAD into sector 0 and switch its trailer to the MAD key.
	if err := t.writeMAD(); err != nil {
		return fmt.Errorf("formatNDEF (UID: %s): writing MAD: %w", t.uid, err)
	}

	// 2. Write the NDEF data across the data sectors, zero-filling the rest, and
	//    switch each trailer to the NFC Forum configuration.
	lastAuthSector := -1
	offset := 0
	for sector := 1; sector <= dataSectors; sector++ {
		for b := 0; b < dataBlocksPerSector; b++ {
			block := sector*4 + b
			chunk := make([]byte, 16)
			if offset < len(tlv) {
				offset += copy(chunk, tlv[offset:])
			}
			if err := t.writeBlock(block, chunk, &lastAuthSector); err != nil {
				return fmt.Errorf("formatNDEF (UID: %s): writing block %d: %w", t.uid, block, err)
			}
		}
		if err := t.writeBlock(sector*4+3, ndefSectorTrailer, &lastAuthSector); err != nil {
			return fmt.Errorf("formatNDEF (UID: %s): writing trailer for sector %d: %w", t.uid, sector, err)
		}
	}

	return nil
}

// writeMAD writes the MIFARE Application Directory into sector 0, marking all
// data sectors (1..15) as NFC Forum NDEF, and switches the sector 0 trailer to
// the MAD key.
func (t *pcscClassicTag) writeMAD() error {
	block1 := make([]byte, 16) // [CRC][info][AID sectors 1..7]
	block2 := make([]byte, 16) // [AID sectors 8..15]

	block1[1] = madInfoByte
	for i := 0; i < 7; i++ { // sectors 1..7
		block1[2+i*2] = madAIDNDEFLo
		block1[2+i*2+1] = madAIDNDEFHi
	}
	for i := 0; i < 8; i++ { // sectors 8..15
		block2[i*2] = madAIDNDEFLo
		block2[i*2+1] = madAIDNDEFHi
	}

	// The MAD CRC (byte 0 of block 1) covers the info byte and all 15 AID
	// entries: bytes 1..31 of the 32-byte MAD.
	crcInput := append(append([]byte(nil), block1[1:]...), block2...)
	block1[0] = madCRC(crcInput)

	lastAuthSector := -1
	if err := t.writeBlock(1, block1, &lastAuthSector); err != nil {
		return fmt.Errorf("write MAD block 1: %w", err)
	}
	if err := t.writeBlock(2, block2, &lastAuthSector); err != nil {
		return fmt.Errorf("write MAD block 2: %w", err)
	}
	if err := t.writeBlock(3, madSectorTrailer, &lastAuthSector); err != nil {
		return fmt.Errorf("write MAD sector trailer: %w", err)
	}
	return nil
}

// madCRC computes the MIFARE Application Directory CRC (CRC-8, polynomial 0x1D,
// preset 0xC7), as specified by NXP AN10787.
func madCRC(data []byte) byte {
	const poly = 0x1D
	crc := byte(0xC7)
	for _, b := range data {
		crc ^= b
		for i := 0; i < 8; i++ {
			if crc&0x80 != 0 {
				crc = (crc << 1) ^ poly
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// trailerCondRecoverable is the sector-trailer access condition (C1=0, C2=1,
// C3=1) used by NDEF formatting. It keeps the trailer rewritable with Key B —
// Key B can rewrite Key A, the access bits, and Key B itself — so a
// misconfiguration can be undone by authenticating with the known Key B.
const trailerCondRecoverable = 0b011

// accessBitsConsistent verifies the integrity of a sector trailer's three
// access-condition bytes. Each Cx nibble is stored alongside its bit-complement;
// the card rejects (and may permanently invalidate the sector for) a trailer
// whose complements don't match.
//
// Layout: byte6 = [~C2 | ~C1], byte7 = [C1 | ~C3], byte8 = [C3 | C2]
// (high nibble | low nibble).
func accessBitsConsistent(b6, b7, b8 byte) bool {
	c1 := (b7 >> 4) & 0x0F
	c2 := b8 & 0x0F
	c3 := (b8 >> 4) & 0x0F
	c1inv := b6 & 0x0F
	c2inv := (b6 >> 4) & 0x0F
	c3inv := b7 & 0x0F
	return c1inv == (^c1&0x0F) && c2inv == (^c2&0x0F) && c3inv == (^c3&0x0F)
}

// trailerAccessCondition returns the 3-bit access condition (C1<<2 | C2<<1 | C3)
// for the sector trailer block (block index 3).
func trailerAccessCondition(b6, b7, b8 byte) byte {
	c1 := (b7 >> 4) & 0x0F
	c2 := b8 & 0x0F
	c3 := (b8 >> 4) & 0x0F
	bit := func(n, i byte) byte { return (n >> i) & 1 }
	return bit(c1, 3)<<2 | bit(c2, 3)<<1 | bit(c3, 3)
}

// validateNDEFTrailer reports whether a 16-byte sector trailer is safe to write
// during formatting: the access bits must be internally consistent (so the card
// doesn't reject/invalidate the sector) and must keep the trailer rewritable
// with Key B (so any misconfiguration is recoverable).
func validateNDEFTrailer(trailer []byte) error {
	if len(trailer) != 16 {
		return fmt.Errorf("sector trailer must be 16 bytes, got %d", len(trailer))
	}
	b6, b7, b8 := trailer[6], trailer[7], trailer[8]
	if !accessBitsConsistent(b6, b7, b8) {
		return fmt.Errorf("access bits %02X %02X %02X are inconsistent (would invalidate the sector)", b6, b7, b8)
	}
	if trailerAccessCondition(b6, b7, b8) != trailerCondRecoverable {
		return fmt.Errorf("access bits %02X %02X %02X do not keep the trailer rewritable with Key B (not recoverable)", b6, b7, b8)
	}
	return nil
}

// Ensure pcscClassicTag implements AdvancedWriter.
var _ AdvancedWriter = (*pcscClassicTag)(nil)
