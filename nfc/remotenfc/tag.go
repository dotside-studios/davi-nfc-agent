package remotenfc

import (
	"fmt"
	"sync"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
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
	sourceDevice string // Device ID that scanned this tag
	mu           sync.RWMutex
}

func (t *Tag) UID() string {
	return t.uid
}

func (t *Tag) Type() string {
	return t.tagType
}

// NumericType returns 0: smartphone tags have no freefare numeric type.
func (t *Tag) NumericType() int {
	return 0
}

func (t *Tag) Capabilities() nfc.TagCapabilities {
	return nfc.TagCapabilities{
		CanRead:       true,
		CanWrite:      false,
		CanTransceive: false,
		CanLock:       false,
		TagFamily:     t.tagType,
		Technology:    t.technology,
		SupportsNDEF:  t.ndefMsg != nil || t.ndefData != nil,
	}
}

// ReadData returns the tag data, preferring NDEF over raw bytes.
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

func (t *Tag) ScannedAt() time.Time {
	return t.scannedAt
}

func (t *Tag) SourceDevice() string {
	return t.sourceDevice
}
