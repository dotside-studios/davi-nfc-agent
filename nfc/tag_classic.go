package nfc

import (
	"fmt"
)

// Default MIFARE keys to try during authentication
var classicDefaultKeys = [][]byte{
	{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, // Factory default
	{0xD3, 0xF7, 0xD3, 0xF7, 0xD3, 0xF7}, // NFC Forum public key
	{0xA0, 0xA1, 0xA2, 0xA3, 0xA4, 0xA5}, // MAD key
	{0x00, 0x00, 0x00, 0x00, 0x00, 0x00}, // Zero key
}

type pcscClassicTag struct {
	pcscBaseTag
	is4K bool
}

func newPCSCClassicTag(dev CardTransport, uid string, tagType DetectedTagType) *pcscClassicTag {
	return &pcscClassicTag{
		pcscBaseTag: pcscBaseTag{
			device:       dev,
			uid:          uid,
			detectedType: tagType,
		},
		is4K: tagType == DetectedClassic4K,
	}
}

func (t *pcscClassicTag) Type() string {
	if t.is4K {
		return CardTypeMifareClassic4K
	}
	return CardTypeMifareClassic1K
}

func (t *pcscClassicTag) NumericType() int {
	return detectedTypeNumeric(t.detectedType)
}

func (t *pcscClassicTag) Transceive(data []byte) ([]byte, error) {
	return nil, fmt.Errorf("Transceive not supported for MIFARE Classic")
}

func (t *pcscClassicTag) Capabilities() TagCapabilities {
	return InferTagCapabilities(t.Type())
}

// authenticateSector attempts to authenticate to a sector using multiple keys.
// Card removal is detected at the device layer via Transceive().
func (t *pcscClassicTag) authenticateSector(sector int) error {
	authBlock := sector*4 + 3 // Sector trailer block

	for _, key := range classicDefaultKeys {
		// Load key into reader's key slot 0
		loadCmd := LoadKeyAPDU(0x00, key)
		resp, err := t.transmitRaw(loadCmd)
		if err != nil {
			// Device layer detects card removal - propagate immediately
			if IsCardRemovedError(err) {
				return err
			}
			continue
		}
		parsed, _ := ParseAPDUResponse(resp)
		if !parsed.IsSuccess() {
			continue
		}

		// Try Key A authentication
		authCmd := MIFAREAuthAPDU(byte(authBlock), MIFAREKeyA, 0x00)
		resp, err = t.transmitRaw(authCmd)
		if err != nil {
			if IsCardRemovedError(err) {
				return err
			}
			continue
		}
		parsed, _ = ParseAPDUResponse(resp)
		if parsed.IsSuccess() {
			return nil
		}

		// Try Key B authentication
		authCmd = MIFAREAuthAPDU(byte(authBlock), MIFAREKeyB, 0x00)
		resp, err = t.transmitRaw(authCmd)
		if err != nil {
			if IsCardRemovedError(err) {
				return err
			}
			continue
		}
		parsed, _ = ParseAPDUResponse(resp)
		if parsed.IsSuccess() {
			return nil
		}
	}

	// All keys failed - check if card was removed
	// This catches cases where APDU succeeds but returns error status (SW1=63)
	// because the card is no longer in the RF field
	if !t.device.IsCardPresent() {
		return NewCardRemovedError(fmt.Errorf("card removed during authentication"))
	}

	return fmt.Errorf("authentication failed for sector %d: no valid key found", sector)
}

// readBlock reads 16 bytes from the specified block, authenticating if needed
func (t *pcscClassicTag) readBlock(block int, lastAuthSector *int) ([]byte, error) {
	sector := block / 4
	if *lastAuthSector != sector {
		if err := t.authenticateSector(sector); err != nil {
			return nil, err
		}
		*lastAuthSector = sector
	}

	cmd := ReadBinaryAPDU(byte(block), 16)
	resp, err := t.transmitRaw(cmd)
	if err != nil {
		return nil, err
	}

	parsed, err := ParseAPDUResponse(resp)
	if err != nil {
		return nil, err
	}

	if !parsed.IsSuccess() {
		return nil, parsed.Error()
	}

	return parsed.Data, nil
}

// writeBlock writes 16 bytes to the specified block, authenticating if needed
func (t *pcscClassicTag) writeBlock(block int, data []byte, lastAuthSector *int) error {
	if len(data) != 16 {
		return fmt.Errorf("block data must be 16 bytes, got %d", len(data))
	}

	sector := block / 4
	if *lastAuthSector != sector {
		if err := t.authenticateSector(sector); err != nil {
			return err
		}
		*lastAuthSector = sector
	}

	cmd := UpdateBinaryAPDU(byte(block), data)
	resp, err := t.transmitRaw(cmd)
	if err != nil {
		return err
	}

	parsed, err := ParseAPDUResponse(resp)
	if err != nil {
		return err
	}

	if !parsed.IsSuccess() {
		return parsed.Error()
	}

	return nil
}

func (t *pcscClassicTag) ReadData() ([]byte, error) {
	var allData []byte
	lastAuthSector := -1

	// Determine max blocks based on card type
	maxBlocks := 64 // MIFARE Classic 1K: 16 sectors × 4 blocks
	if t.is4K {
		maxBlocks = 256 // MIFARE Classic 4K: 32 sectors × 4 blocks + 8 sectors × 16 blocks
	}

	// Read from block 4 onwards (skip sector 0/MAD)
	var lastError error
	for blockNum := 4; blockNum < maxBlocks; blockNum++ {
		// Skip sector trailers (last block in each sector)
		// For 1K: blocks 3, 7, 11, 15, ... (every 4th block starting at 3)
		// For 4K: same for first 32 sectors, then every 16th for large sectors
		if t.isSectorTrailer(blockNum) {
			continue
		}

		blockData, err := t.readBlock(blockNum, &lastAuthSector)
		if err != nil {
			// If card was removed, propagate that error immediately
			if IsCardRemovedError(err) {
				return nil, err
			}
			// For other errors, record and stop reading
			lastError = err
			break
		}
		allData = append(allData, blockData...)

		// Check for NDEF terminator (0xFE)
		for _, b := range blockData {
			if b == 0xFE {
				goto done
			}
		}
	}
done:

	if len(allData) == 0 {
		// Check if error was due to card removal (APDU errors when card is gone)
		if lastError != nil && !t.device.IsCardPresent() {
			return nil, NewCardRemovedError(fmt.Errorf("card removed during read"))
		}
		if lastError != nil {
			return nil, fmt.Errorf("failed to read any data from tag: %w", lastError)
		}
		return nil, fmt.Errorf("failed to read any data from tag")
	}

	// Parse TLV to extract NDEF message
	ndefData, found := TLVFindNDEF(allData)
	if !found {
		return nil, fmt.Errorf("no NDEF message found")
	}

	return ndefData, nil
}

func (t *pcscClassicTag) WriteData(data []byte) error {
	// Wrap NDEF data in TLV structure
	tlvPayload := TLVEncode(data, TLVNDEF)

	// Pad to 16-byte blocks
	for len(tlvPayload)%16 != 0 {
		tlvPayload = append(tlvPayload, 0x00)
	}

	// Determine max usable blocks
	maxBlocks := 64
	if t.is4K {
		maxBlocks = 256
	}

	// Calculate blocks needed (excluding sector trailers)
	usableBlocks := 0
	for blockNum := 4; blockNum < maxBlocks; blockNum++ {
		if !t.isSectorTrailer(blockNum) {
			usableBlocks++
		}
	}

	blocksNeeded := len(tlvPayload) / 16
	if blocksNeeded > usableBlocks {
		return fmt.Errorf("data too large: need %d blocks, have %d usable blocks", blocksNeeded, usableBlocks)
	}

	blockNum := 4 // Start at sector 1
	lastAuthSector := -1

	for offset := 0; offset < len(tlvPayload); offset += 16 {
		// Skip sector trailers
		for t.isSectorTrailer(blockNum) {
			blockNum++
		}

		if err := t.writeBlock(blockNum, tlvPayload[offset:offset+16], &lastAuthSector); err != nil {
			return fmt.Errorf("failed to write block %d: %w", blockNum, err)
		}
		blockNum++
	}

	return nil
}

// isSectorTrailer returns true if the block is a sector trailer
func (t *pcscClassicTag) isSectorTrailer(block int) bool {
	if t.is4K && block >= 128 {
		// Large sectors (sectors 32-39) have 16 blocks each
		// Sector trailers are at blocks 143, 159, 175, 191, 207, 223, 239, 255
		return (block+1)%16 == 0
	}
	// Small sectors (sectors 0-31) have 4 blocks each
	return (block+1)%4 == 0
}

func (t *pcscClassicTag) IsWritable() (bool, error) {
	// Try to authenticate to sector 1 to check if we can access it
	lastAuthSector := -1
	_, err := t.readBlock(4, &lastAuthSector)
	return err == nil, nil
}

func (t *pcscClassicTag) CanMakeReadOnly() (bool, error) {
	return true, nil
}

func (t *pcscClassicTag) MakeReadOnly() error {
	return fmt.Errorf("MIFARE Classic MakeReadOnly not yet implemented")
}

// authenticateWithKey authenticates to a sector using a specific key and key type
func (t *pcscClassicTag) authenticateWithKey(sector int, key []byte, keyType int) error {
	if len(key) != 6 {
		return fmt.Errorf("key must be 6 bytes, got %d", len(key))
	}
	if keyType != KeyTypeA && keyType != KeyTypeB {
		return fmt.Errorf("invalid key type: must be KeyTypeA (0x60) or KeyTypeB (0x61)")
	}

	// Calculate the sector trailer block (authentication block)
	var authBlock int
	if t.is4K && sector >= 32 {
		// Large sectors (32-39) have 16 blocks each
		authBlock = 128 + (sector-32)*16 + 15
	} else {
		// Small sectors have 4 blocks each
		authBlock = sector*4 + 3
	}

	// Load key into reader's key slot 0
	loadCmd := LoadKeyAPDU(0x00, key)
	resp, err := t.transmitRaw(loadCmd)
	if err != nil {
		return fmt.Errorf("failed to load key: %w", err)
	}
	parsed, _ := ParseAPDUResponse(resp)
	if !parsed.IsSuccess() {
		return fmt.Errorf("failed to load key: %w", parsed.Error())
	}

	// Authenticate with the specified key type
	authCmd := MIFAREAuthAPDU(byte(authBlock), byte(keyType), 0x00)
	resp, err = t.transmitRaw(authCmd)
	if err != nil {
		return fmt.Errorf("authentication failed: %w", err)
	}
	parsed, _ = ParseAPDUResponse(resp)
	if !parsed.IsSuccess() {
		return fmt.Errorf("authentication failed for sector %d: %w", sector, parsed.Error())
	}

	return nil
}

// sectorToAbsoluteBlock converts a sector and relative block to an absolute block number
func (t *pcscClassicTag) sectorToAbsoluteBlock(sector, block uint8) (int, error) {
	var absoluteBlock int
	var maxBlock uint8

	if t.is4K && sector >= 32 {
		// Large sectors (32-39) have 16 blocks each
		if sector > 39 {
			return 0, fmt.Errorf("sector %d out of range for 4K card (max 39)", sector)
		}
		absoluteBlock = 128 + int(sector-32)*16 + int(block)
		maxBlock = 15
	} else {
		// Small sectors have 4 blocks each
		maxSector := uint8(15)
		if t.is4K {
			maxSector = 31
		}
		if sector > maxSector {
			return 0, fmt.Errorf("sector %d out of range (max %d)", sector, maxSector)
		}
		absoluteBlock = int(sector)*4 + int(block)
		maxBlock = 3
	}

	if block > maxBlock {
		return 0, fmt.Errorf("block %d out of range for sector (max %d)", block, maxBlock)
	}

	return absoluteBlock, nil
}

// Read reads a 16-byte block from the specified sector using the provided key.
// This implements the ClassicTag interface.
func (t *pcscClassicTag) Read(sector, block uint8, key []byte, keyType int) ([]byte, error) {
	// Validate and convert to absolute block number
	absoluteBlock, err := t.sectorToAbsoluteBlock(sector, block)
	if err != nil {
		return nil, err
	}

	// Authenticate to the sector
	if err := t.authenticateWithKey(int(sector), key, keyType); err != nil {
		return nil, err
	}

	// Read the block
	cmd := ReadBinaryAPDU(byte(absoluteBlock), 16)
	resp, err := t.transmitRaw(cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to read block: %w", err)
	}

	parsed, err := ParseAPDUResponse(resp)
	if err != nil {
		return nil, err
	}

	if !parsed.IsSuccess() {
		return nil, fmt.Errorf("read failed: %w", parsed.Error())
	}

	return parsed.Data, nil
}

// Write writes 16 bytes to the specified block using the provided key.
// This implements the ClassicTag interface.
func (t *pcscClassicTag) Write(sector, block uint8, data []byte, key []byte, keyType int) error {
	if len(data) != 16 {
		return fmt.Errorf("data must be exactly 16 bytes, got %d", len(data))
	}

	// Validate and convert to absolute block number
	absoluteBlock, err := t.sectorToAbsoluteBlock(sector, block)
	if err != nil {
		return err
	}

	// Check if trying to write to sector trailer (block 3 in small sectors, 15 in large)
	// This is allowed but dangerous - warn via error if it's the trailer
	if t.isSectorTrailer(absoluteBlock) {
		// Allow it but the caller should know what they're doing
	}

	// Authenticate to the sector
	if err := t.authenticateWithKey(int(sector), key, keyType); err != nil {
		return err
	}

	// Write the block
	cmd := UpdateBinaryAPDU(byte(absoluteBlock), data)
	resp, err := t.transmitRaw(cmd)
	if err != nil {
		return fmt.Errorf("failed to write block: %w", err)
	}

	parsed, err := ParseAPDUResponse(resp)
	if err != nil {
		return err
	}

	if !parsed.IsSuccess() {
		return fmt.Errorf("write failed: %w", parsed.Error())
	}

	return nil
}

// Ensure pcscClassicTag implements ClassicTag interface
var _ ClassicTag = (*pcscClassicTag)(nil)
