package protocol

// TagCapabilities describes what operations a tag supports. It is aliased as
// nfc.TagCapabilities; the canonical definition lives here because it crosses
// the wire in both the device and client protocols.
type TagCapabilities struct {
	// Core capabilities
	CanRead       bool `json:"canRead"`
	CanWrite      bool `json:"canWrite"`
	CanTransceive bool `json:"canTransceive"`

	// Locking capabilities
	CanLock    bool `json:"canLock"`
	IsReadOnly bool `json:"isReadOnly,omitempty"`

	// Memory info
	MemorySize  int `json:"memorySize,omitempty"`  // Total memory in bytes
	MaxNDEFSize int `json:"maxNdefSize,omitempty"` // Max NDEF message size

	// Technology info
	Technology string `json:"technology,omitempty"` // "ISO14443A", "ISO14443B", etc.
	TagFamily  string `json:"tagFamily,omitempty"`  // "MIFARE Classic", "DESFire", "NTAG", etc.

	// Optional features
	SupportsNDEF           bool `json:"supportsNdef"`
	SupportsCrypto         bool `json:"supportsCrypto,omitempty"`
	SupportsAuthentication bool `json:"supportsAuthentication,omitempty"`

	// SupportsPassword indicates the tag supports simple password protection
	// (e.g. NTAG PWD/PACK/AUTH0). This is distinct from SupportsAuthentication,
	// which covers crypto-based mutual authentication (DESFire, Ultralight C).
	SupportsPassword bool `json:"supportsPassword,omitempty"`
}
