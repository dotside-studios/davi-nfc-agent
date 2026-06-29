package nfc

import (
	"fmt"
	"strings"
)

// TagCapabilities describes what operations a tag supports.
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

// DeviceCapabilities describes what operations a device supports.
type DeviceCapabilities struct {
	// Communication capabilities
	CanTransceive bool `json:"canTransceive"`
	CanPoll       bool `json:"canPoll"`

	// Supported tag types
	SupportedTagTypes []string `json:"supportedTagTypes,omitempty"`

	// Hardware info
	DeviceType  string `json:"deviceType"`            // "libnfc", "smartphone", etc.
	MaxBaudRate int    `json:"maxBaudRate,omitempty"` // Max baud rate in bps

	// Event capabilities
	SupportsEvents bool `json:"supportsEvents"` // Tag arrival/removal events
}

// TagCapabilityProvider is an optional interface for tags to report their capabilities.
type TagCapabilityProvider interface {
	Capabilities() TagCapabilities
}

// DeviceInfoProvider provides device metadata for capability building.
// Implement this interface to provide device-specific information.
type DeviceInfoProvider interface {
	DeviceType() string
	SupportedTagTypes() []string
}

// DeviceEventEmitter is a marker interface for devices that emit tag events
// (e.g., tag arrival/removal) rather than requiring polling.
type DeviceEventEmitter interface {
	SupportsEvents() bool
}

// DeviceTransceiver is an optional interface that lets a device declare whether
// it actually supports raw Transceive. Devices that do not implement it default
// to CanTransceive=true (the common case for polling hardware readers).
// Implement this to report false when your device's Transceive returns a
// NotSupported error, so GetDeviceCapabilities reflects reality.
type DeviceTransceiver interface {
	SupportsTransceive() bool
}

// GetTagCapabilities returns capabilities for any Tag.
// If the tag implements TagCapabilityProvider, it uses that.
// Otherwise, it infers capabilities from the tag type string.
func GetTagCapabilities(tag Tag) TagCapabilities {
	if provider, ok := tag.(TagCapabilityProvider); ok {
		return provider.Capabilities()
	}
	return InferTagCapabilities(tag.Type())
}

// GetDeviceCapabilities returns capabilities for any Device.
// Capabilities are built by checking which interfaces the device implements.
func GetDeviceCapabilities(device Device) DeviceCapabilities {
	return BuildDeviceCapabilities(device)
}

// BuildDeviceCapabilities constructs a DeviceCapabilities struct by
// checking which interfaces the device implements.
func BuildDeviceCapabilities(device Device) DeviceCapabilities {
	caps := DeviceCapabilities{
		CanTransceive: true, // Default for polling readers; refined below
		CanPoll:       true, // Default for polling readers; refined below
		DeviceType:    "unknown",
	}

	// Get device metadata if available
	if info, ok := device.(DeviceInfoProvider); ok {
		caps.DeviceType = info.DeviceType()
		caps.SupportedTagTypes = info.SupportedTagTypes()
	}

	// Check for event-based device (smartphone-style)
	// Event-based devices typically don't poll and don't support raw transceive
	if emitter, ok := device.(DeviceEventEmitter); ok && emitter.SupportsEvents() {
		caps.SupportsEvents = true
		caps.CanPoll = false       // Event-driven, not polling
		caps.CanTransceive = false // Usually no raw transceive for event-based
	}

	// Let the device explicitly declare transceive support if it implements
	// DeviceTransceiver. This takes precedence over the defaults above so a
	// polling device whose Transceive returns NotSupported can report it.
	if tr, ok := device.(DeviceTransceiver); ok {
		caps.CanTransceive = tr.SupportsTransceive()
	}

	return caps
}

// InferTagCapabilities infers capabilities from a tag type string.
// This is used as a fallback when the tag doesn't implement TagCapabilityProvider.
func InferTagCapabilities(tagType string) TagCapabilities {
	caps := TagCapabilities{
		CanRead:      true, // All tags can read
		SupportsNDEF: true, // Assume NDEF support
	}

	tagTypeLower := strings.ToLower(tagType)

	switch {
	case strings.Contains(tagTypeLower, "mifare classic") || strings.Contains(tagTypeLower, "classic"):
		caps.CanWrite = true
		caps.CanTransceive = false
		caps.CanLock = false // Not implemented (no sector-trailer access-bit locking)
		caps.TagFamily = "MIFARE Classic"
		caps.Technology = "ISO14443A"
		caps.SupportsCrypto = true
		caps.SupportsAuthentication = true
		if strings.Contains(tagTypeLower, "1k") {
			caps.MemorySize = 1024
			caps.MaxNDEFSize = 716 // Approximate usable NDEF space
		} else if strings.Contains(tagTypeLower, "4k") {
			caps.MemorySize = 4096
			caps.MaxNDEFSize = 3356
		}

	case strings.Contains(tagTypeLower, "desfire"):
		caps.CanWrite = true
		caps.CanTransceive = false
		caps.CanLock = false // Not implemented
		caps.TagFamily = "DESFire"
		caps.Technology = "ISO14443A"
		caps.SupportsCrypto = true
		caps.SupportsAuthentication = true
		caps.MemorySize = 8192 // Varies by model

	case strings.Contains(tagTypeLower, "ultralight"):
		caps.CanWrite = true
		caps.CanTransceive = false
		caps.CanLock = true
		caps.TagFamily = "MIFARE Ultralight"
		caps.Technology = "ISO14443A"
		if strings.Contains(tagTypeLower, "c") {
			caps.MemorySize = 192
			caps.MaxNDEFSize = 137
			caps.SupportsCrypto = true
		} else {
			caps.MemorySize = 64
			caps.MaxNDEFSize = 46
		}

	case strings.Contains(tagTypeLower, "ntag2") || tagTypeLower == "ntag" || strings.HasPrefix(tagTypeLower, "ntag "):
		caps.CanWrite = true
		caps.CanTransceive = false
		caps.CanLock = true
		caps.SupportsPassword = true // NTAG21x support PWD/PACK/AUTH0
		caps.TagFamily = "NTAG"
		caps.Technology = "ISO14443A"
		if strings.Contains(tagTypeLower, "213") {
			caps.MemorySize = 180
			caps.MaxNDEFSize = 144
		} else if strings.Contains(tagTypeLower, "215") {
			caps.MemorySize = 540
			caps.MaxNDEFSize = 504
		} else if strings.Contains(tagTypeLower, "216") {
			caps.MemorySize = 924
			caps.MaxNDEFSize = 888
		}

	case strings.Contains(tagTypeLower, "type4") || strings.Contains(tagTypeLower, "iso14443"):
		caps.CanWrite = true
		caps.CanTransceive = true
		caps.CanLock = true
		caps.TagFamily = "Type 4"
		caps.Technology = "ISO14443A"

	default:
		// Conservative defaults for unknown types
		caps.CanWrite = false
		caps.CanTransceive = false
		caps.CanLock = false
		caps.TagFamily = "Unknown"
	}

	return caps
}

// CanTagRead checks if a tag supports read operations.
func CanTagRead(tag Tag) bool {
	return GetTagCapabilities(tag).CanRead
}

// CanTagWrite checks if a tag supports write operations.
func CanTagWrite(tag Tag) bool {
	return GetTagCapabilities(tag).CanWrite
}

// CanTagTransceive checks if a tag supports raw transceive operations.
func CanTagTransceive(tag Tag) bool {
	return GetTagCapabilities(tag).CanTransceive
}

// CanTagLock checks if a tag can be made read-only.
func CanTagLock(tag Tag) bool {
	return GetTagCapabilities(tag).CanLock
}

// AssertCapabilitiesConsistent verifies that a tag's declared TagCapabilities
// agree with the tag's query methods, returning a descriptive error on the
// first mismatch (nil if consistent). It is intended for implementers' unit
// tests to catch capability drift between Capabilities() and behavior.
//
// It performs ONLY non-mutating (read-only) checks and never calls WriteData,
// Transceive, or MakeReadOnly, since those may perform real or irreversible
// I/O on hardware. Specifically it checks:
//
//   - Capabilities().CanLock matches CanMakeReadOnly()
//   - When Capabilities().IsReadOnly is true, IsWritable() reports false
//
// Supported write/transceive/lock behavior cannot be auto-verified without
// device I/O; cover those directly in your own tests.
func AssertCapabilitiesConsistent(tag Tag) error {
	caps := GetTagCapabilities(tag)

	canRO, err := tag.CanMakeReadOnly()
	if err != nil {
		return fmt.Errorf("CanMakeReadOnly returned an error: %w", err)
	}
	if canRO != caps.CanLock {
		return fmt.Errorf("capability drift: Capabilities().CanLock=%t but CanMakeReadOnly()=%t", caps.CanLock, canRO)
	}

	if caps.IsReadOnly {
		writable, err := tag.IsWritable()
		if err != nil {
			return fmt.Errorf("IsWritable returned an error: %w", err)
		}
		if writable {
			return fmt.Errorf("capability drift: Capabilities().IsReadOnly=true but IsWritable() reports the tag is writable")
		}
	}

	return nil
}
