package nfc

// tagProfile is what this package knows about one kind of tag: how it is
// named, and what the driver backing it can actually do.
//
// It is the single source for Type(), NumericType() and Capabilities(), so a
// driver cannot report a name implying one thing and behaviour doing another.
// Recovering capabilities from a display name cannot work: every Ultralight
// variant shares one name, and a name says nothing about whether the driver
// implements a lock or a raw exchange. A profile is declared next to the driver
// that has to honour it.
//
// A kind with no driver has no profile. Capabilities for a tag this package
// did not build come from the tag itself; see InferTagCapabilities for the one
// case where only a name is left to go on.
type tagProfile struct {
	name        string // display name, e.g. "NTAG215"
	numericType int    // SAK-like numeric identifier, -1 when there is none
	family      string // "NTAG", "MIFARE Classic", ...
	technology  string // "ISO14443A", ...

	memorySize  int // total memory in bytes, 0 when unknown
	maxNDEFSize int // largest NDEF message the driver will write, 0 when unbounded

	canWrite      bool
	canLock       bool
	canTransceive bool

	// supportsNDEF says the driver reads and writes NDEF on this kind.
	supportsNDEF bool

	supportsCrypto         bool
	supportsAuthentication bool
	supportsPassword       bool
}

// capabilities projects the profile onto the wire-facing capability struct.
// Anything that varies per tag rather than per kind, such as a tag found to be
// read-only, is layered on by the driver.
func (p tagProfile) capabilities() TagCapabilities {
	return TagCapabilities{
		CanRead:                true, // every driver here can read
		SupportsNDEF:           p.supportsNDEF,
		CanWrite:               p.canWrite,
		CanLock:                p.canLock,
		CanTransceive:          p.canTransceive,
		MemorySize:             p.memorySize,
		MaxNDEFSize:            p.maxNDEFSize,
		Technology:             p.technology,
		TagFamily:              p.family,
		SupportsCrypto:         p.supportsCrypto,
		SupportsAuthentication: p.supportsAuthentication,
		SupportsPassword:       p.supportsPassword,
	}
}

// ultralightProfile builds the profile for an Ultralight-family kind from the
// memory layout its driver reads and writes, so the capacity a tag advertises
// cannot exceed the pages the driver is willing to touch.
func ultralightProfile(name string, kind DetectedTagType, memorySize, maxNDEFSize int, crypto bool) tagProfile {
	layout := ultralightLayoutFor(kind)
	return tagProfile{
		name:                   name,
		numericType:            0x00,
		family:                 "MIFARE Ultralight",
		technology:             "ISO14443A",
		memorySize:             memorySize,
		maxNDEFSize:            maxNDEFSize,
		canWrite:               true,
		canLock:                layout.lockable,
		supportsNDEF:           true,
		canTransceive:          false, // the driver refuses raw exchange
		supportsCrypto:         crypto,
		supportsAuthentication: crypto,
	}
}

func classicProfile(name string, memorySize, maxNDEFSize int) tagProfile {
	return tagProfile{
		name:         name,
		numericType:  0x08,
		family:       "MIFARE Classic",
		technology:   "ISO14443A",
		memorySize:   memorySize,
		maxNDEFSize:  maxNDEFSize,
		canWrite:     true,
		supportsNDEF: true,
		// Writing access bits into the sector trailers is not implemented.
		canLock:                false,
		canTransceive:          false,
		supportsCrypto:         true,
		supportsAuthentication: true,
	}
}

func ntagProfile(name string, memorySize, maxNDEFSize int) tagProfile {
	return tagProfile{
		name:             name,
		numericType:      0x00,
		family:           "NTAG",
		technology:       "ISO14443A",
		memorySize:       memorySize,
		maxNDEFSize:      maxNDEFSize,
		canWrite:         true,
		canLock:          true,
		supportsNDEF:     true,
		canTransceive:    false, // the driver refuses raw exchange
		supportsPassword: true,  // NTAG21x carry PWD/PACK/AUTH0
	}
}

// desfireProfile builds the profile a DESFire generation shares. The generation
// is carried in the family rather than the name, so a card-type filter naming
// "DESFire" keeps matching every one of them.
//
// Memory size and NDEF capacity are per-card rather than per-kind, and the
// driver reads them off the card; see pcscDESFireTag.probe.
func desfireProfile(family string) tagProfile {
	return tagProfile{
		name:         CardTypeDesfire,
		numericType:  0x20,
		family:       family,
		technology:   "ISO14443A",
		canWrite:     true,
		supportsNDEF: true,
		// Locking means rewriting the file's access rights to deny writing and
		// deny changing them again. Whether a given card allows it depends on
		// its change right, which the driver reads off the card.
		canLock: true,
		// The driver forwards APDUs to the card, which is how a DESFire
		// application is meant to be driven.
		canTransceive:          true,
		supportsCrypto:         true,
		supportsAuthentication: true,
	}
}

// tagProfiles covers every kind NewTagForType can build a driver for.
var tagProfiles = map[DetectedTagType]tagProfile{
	DetectedClassic1K: classicProfile(CardTypeMifareClassic1K, 1024, 716),
	DetectedClassic4K: func() tagProfile {
		p := classicProfile(CardTypeMifareClassic4K, 4096, 3356)
		p.numericType = 0x18
		return p
	}(),

	DetectedUltralight:  ultralightProfile(CardTypeMifareUltralight, DetectedUltralight, 64, 46, false),
	DetectedUltralightC: ultralightProfile(CardTypeMifareUltralight, DetectedUltralightC, 192, 137, true),
	// MF0UL11 is laid out like the original Ultralight, with more config pages.
	DetectedUltralightEV1: ultralightProfile(CardTypeMifareUltralight, DetectedUltralightEV1, 80, 46, false),
	// MF0UL21 carries 128 bytes of user memory across 41 pages.
	DetectedUltralightEV1_128: ultralightProfile(CardTypeMifareUltralight, DetectedUltralightEV1_128, 164, 126, false),

	DetectedNTAG213: ntagProfile(CardTypeNtag213, 180, 144),
	DetectedNTAG215: ntagProfile(CardTypeNtag215, 540, 504),
	DetectedNTAG216: ntagProfile(CardTypeNtag216, 924, 888),

	DetectedDESFire:    desfireProfile("DESFire"),
	DetectedDESFireEV1: desfireProfile("DESFire EV1"),
	DetectedDESFireEV2: desfireProfile("DESFire EV2"),
	DetectedDESFireEV3: desfireProfile("DESFire EV3"),

	// The NTAG 424 DNA is driven as a Type 4 card. It differs from the generic
	// profile below only in what is known about it: a fixed layout, so the
	// write path has a capacity to check, and AES keys behind the files.
	DetectedNTAG424: {
		name:        CardTypeNtag424,
		numericType: 0x20, // ISO14443-4, as every Type 4 card reports
		family:      "NTAG",
		technology:  "ISO14443A",
		// 416 bytes across the three standard files: 32 for the Capability
		// Container, 256 for NDEF, 128 proprietary.
		memorySize: 416,
		// The NDEF file is 256 bytes, of which the first two hold NLEN.
		maxNDEFSize:  254,
		canWrite:     true,
		supportsNDEF: true,
		// Locking means rewriting the CC file's WriteAccess byte or changing
		// file settings under AES, neither of which is implemented.
		canLock:       false,
		canTransceive: true,
		// AES-128 across five keys. This is what the card supports; no driver
		// here authenticates yet.
		supportsCrypto:         true,
		supportsAuthentication: true,
	},

	DetectedISO14443_4: {
		name:         CardTypeType4,
		numericType:  0x20,
		family:       "Type 4",
		technology:   "ISO14443A/B", // a Type 4 tag may be either
		canWrite:     true,
		supportsNDEF: true,
		// Rewriting the CC file's WriteAccess byte is not implemented.
		canLock:       false,
		canTransceive: true,
	},
}

// profileFor returns the profile for a tag kind. The second result is false for
// a kind this package has no driver for, such as MIFARE Mini or Plus: detection
// can name them, but nothing here can drive one, so nothing can honestly say
// what it supports.
func profileFor(kind DetectedTagType) (tagProfile, bool) {
	p, ok := tagProfiles[kind]
	return p, ok
}
