package remotenfc

import (
	"fmt"
	"sync"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

// Tag wraps mobile app NFC data in the nfc.Tag interface.
//
// Smartphone tags are read-only (writes go through the WebSocket protocol, not
// the Tag interface), so the connection, write, transceive, and lock methods are
// inherited from nfc.BaseTag as no-ops / "not supported".
type Tag struct {
	nfc.BaseTag

	uid          string
	tagType      string
	technology   string
	ndefData     []byte           // Encoded NDEF message
	ndefMsg      *nfc.NDEFMessage // Parsed NDEF message
	rawData      []byte           // Raw tag data from mobile app
	scannedAt    time.Time
	sourceDevice string                    // Device ID that scanned this tag
	declaredCaps *protocol.TagCapabilities // What the device reported, if anything
	mu           sync.RWMutex
}

// UID returns the tag's unique identifier.
func (t *Tag) UID() string {
	return t.uid
}

// Type returns the tag type as a string.
func (t *Tag) Type() string {
	return t.tagType
}

// NumericType returns a numeric representation of the tag type.
// For smartphone tags, we return 0 as they don't have freefare numeric types.
func (t *Tag) NumericType() int {
	return 0
}

// Capabilities returns the capabilities of this smartphone tag, starting from
// whatever the device declared for it.
//
// The operation bits are forced off regardless of the declaration: writes and
// transceives still route through the device WebSocket protocol rather than the
// Tag interface, so claiming them here would be capability drift.
func (t *Tag) Capabilities() nfc.TagCapabilities {
	t.mu.RLock()
	defer t.mu.RUnlock()

	var caps nfc.TagCapabilities
	if t.declaredCaps != nil {
		caps = *t.declaredCaps
	}

	if caps.TagFamily == "" {
		caps.TagFamily = t.tagType
	}
	if caps.Technology == "" {
		caps.Technology = t.technology
	}
	if !caps.SupportsNDEF {
		caps.SupportsNDEF = t.ndefMsg != nil || t.ndefData != nil
	}

	caps.CanRead = true
	caps.CanWrite = false
	caps.CanTransceive = false
	caps.CanLock = false

	return caps
}

// ReadData returns the tag data (NDEF or raw).
func (t *Tag) ReadData() ([]byte, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.ndefData != nil {
		return t.ndefData, nil
	}

	return t.rawData, nil
}

// GetNDEFMessage returns the parsed NDEF message if available.
func (t *Tag) GetNDEFMessage() (*nfc.NDEFMessage, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.ndefMsg != nil {
		return t.ndefMsg, nil
	}

	return nil, fmt.Errorf("no NDEF message available")
}

// ScannedAt returns the timestamp when this tag was scanned.
func (t *Tag) ScannedAt() time.Time {
	return t.scannedAt
}

// SourceDevice returns the device ID that scanned this tag.
func (t *Tag) SourceDevice() string {
	return t.sourceDevice
}
