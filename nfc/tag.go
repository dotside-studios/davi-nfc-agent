package nfc

// TagIdentifier provides basic tag identification.
// All tags implement this interface.
type TagIdentifier interface {
	// UID returns the unique identifier of the tag.
	UID() string
	// Type returns a human-readable string describing the tag type.
	Type() string
	// NumericType returns a numeric type identifier (implementation-specific).
	NumericType() int
}

// TagConnection manages the connection lifecycle to a tag.
// Tags that require explicit connection management implement this interface.
type TagConnection interface {
	// Connect establishes a connection to the tag.
	Connect() error
	// Disconnect closes the connection to the tag.
	Disconnect() error
}

// TagReader provides read capability for NDEF data.
// Tags that support reading implement this interface.
type TagReader interface {
	// ReadData reads NDEF data from the tag.
	ReadData() ([]byte, error)
}

// TagWriter provides write capability for NDEF data.
// Tags that support writing implement this interface.
// Use GetTagCapabilities(tag).CanWrite to check if a tag supports this.
type TagWriter interface {
	// WriteData writes NDEF data to the tag.
	WriteData(data []byte) error
}

// TagTransceiver provides raw data exchange with the tag.
// Only some tag types (e.g., Type 4) support this.
// Use GetTagCapabilities(tag).CanTransceive to check if a tag supports this.
type TagTransceiver interface {
	// Transceive sends raw data to the tag and returns the response.
	Transceive(data []byte) ([]byte, error)
}

// TagLocker provides read-only locking capability.
// Tags that can be made permanently read-only implement this interface.
// Use GetTagCapabilities(tag).CanLock to check if a tag supports this.
type TagLocker interface {
	// IsWritable checks if the tag can be written to.
	IsWritable() (bool, error)
	// CanMakeReadOnly checks if the tag supports being made read-only.
	CanMakeReadOnly() (bool, error)
	// MakeReadOnly permanently locks the tag to prevent further writes.
	MakeReadOnly() error
}

// Tag represents an NFC tag at the hardware protocol level.
//
// Tag provides a unified interface for reading and writing NDEF data
// regardless of the underlying tag technology (MIFARE Classic, ISO14443-4, etc.).
//
// Not all tags support all operations. Use GetTagCapabilities(tag) to check
// what operations a specific tag supports before calling methods that may
// return "not supported" errors.
//
// For most use cases, prefer using Card which provides a higher-level,
// io.Reader/Writer compatible API.
//
// Example:
//
//	tags, _ := manager.GetTags(device)
//	for _, tag := range tags {
//	    caps := nfc.GetTagCapabilities(tag)
//	    if caps.CanRead {
//	        data, _ := tag.ReadData()
//	    }
//	}
type Tag interface {
	TagIdentifier
	TagConnection
	TagReader
	TagWriter
	TagTransceiver
	TagLocker
	TagCapabilityProvider
}

// TagWriteOptions defines options for tag write operations.
type TagWriteOptions struct {
	// ForceInitialize forces reinitialization of the tag even if it contains existing data.
	// WARNING: This will erase all existing data on the tag.
	// Only use this if you explicitly want to wipe and reinitialize the tag.
	ForceInitialize bool
}

// AdvancedWriter is an optional interface that tags can implement to support
// write operations with options. If a tag implements this interface, the reader
// will use WriteDataWithOptions instead of WriteData when options are provided.
type AdvancedWriter interface {
	WriteDataWithOptions(data []byte, opts TagWriteOptions) error
}

// ClassicTag provides MIFARE Classic specific operations.
// This interface extends Tag with sector/block-level access using authentication keys.
//
// MIFARE Classic tags have a sector-based memory structure:
//   - Classic 1K: 16 sectors × 4 blocks (64 blocks total)
//   - Classic 4K: 32 sectors × 4 blocks + 8 sectors × 16 blocks (256 blocks total)
//
// Each sector has a trailer block containing keys and access conditions.
// Blocks are 16 bytes each.
//
// Example:
//
//	if classic, ok := tag.(nfc.ClassicTag); ok {
//	    key := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
//	    data, err := classic.Read(1, 0, key, nfc.KeyTypeA)
//	    if err != nil {
//	        log.Fatal(err)
//	    }
//	}
type ClassicTag interface {
	Tag

	// Read reads a 16-byte block from the specified sector using the provided key.
	// sector: sector number (0-15 for 1K, 0-39 for 4K)
	// block: block within sector (0-2 for data blocks, 3 is sector trailer)
	// key: 6-byte authentication key
	// keyType: KeyTypeA or KeyTypeB
	Read(sector, block uint8, key []byte, keyType int) ([]byte, error)

	// Write writes 16 bytes to the specified block using the provided key.
	// sector: sector number (0-15 for 1K, 0-39 for 4K)
	// block: block within sector (0-2 for data blocks, 3 is sector trailer)
	// data: exactly 16 bytes to write
	// key: 6-byte authentication key
	// keyType: KeyTypeA or KeyTypeB
	Write(sector, block uint8, data []byte, key []byte, keyType int) error
}

// NewTagForType returns the Tag implementation for a detected tag type, driven
// over transport, or nil if the type has no dedicated implementation. Reader
// backends use it to turn a detection result into a usable tag without knowing
// which concrete type implements it.
func NewTagForType(tagType DetectedTagType, transport CardTransport, uid string) Tag {
	switch tagType {
	case DetectedClassic1K, DetectedClassic4K:
		return newPCSCClassicTag(transport, uid, tagType)
	case DetectedUltralight, DetectedUltralightC, DetectedUltralightEV1, DetectedUltralightEV1_128:
		return newPCSCUltralightTag(transport, uid, tagType)
	case DetectedNTAG213, DetectedNTAG215, DetectedNTAG216:
		return newPCSCNtagTag(transport, uid, tagType)
	case DetectedDESFire:
		return newPCSCDESFireTag(transport, uid)
	case DetectedISO14443_4:
		return newPCSCISO14443Tag(transport, uid)
	default:
		return nil
	}
}
