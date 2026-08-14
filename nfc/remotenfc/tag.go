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
// Writes and locks route back to the device holding the tag when it declared
// support for them. Connection and transceive methods are inherited from
// nfc.BaseTag as no-ops / "not supported".
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
	writer       TagWriter                 // Route to the holding device; nil when unavailable
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

// Capabilities returns the capabilities of this smartphone tag, combining what
// the device declared for the tag with what the bridge can actually route.
//
// An operation is reported as available only when the tag supports it, the
// device declared it, and the device is still connected — a capability that
// outlives its session would be a promise the Tag cannot keep.
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
	caps.CanWrite = t.canWrite()
	caps.CanLock = t.canLock()
	caps.CanTransceive = t.canTransceive()

	return caps
}

// canWrite reports whether a write would actually reach the tag. Callers must
// hold at least a read lock.
func (t *Tag) canWrite() bool {
	if t.writer == nil || t.declaredCaps == nil || !t.declaredCaps.CanWrite {
		return false
	}
	if t.declaredCaps.IsReadOnly {
		return false
	}
	return t.writer.DeviceCanWrite(t.sourceDevice)
}

func (t *Tag) canLock() bool {
	if t.writer == nil || t.declaredCaps == nil || !t.declaredCaps.CanLock {
		return false
	}
	if t.declaredCaps.IsReadOnly {
		return false
	}
	return t.writer.DeviceCanLock(t.sourceDevice)
}

// canTransceive reports whether a raw exchange would reach the tag. Callers
// must hold at least a read lock.
func (t *Tag) canTransceive() bool {
	if t.declaredCaps == nil || !t.declaredCaps.CanTransceive {
		return false
	}

	tr, ok := t.writer.(TagTransceiver)
	if !ok {
		return false
	}
	return tr.DeviceCanTransceive(t.sourceDevice)
}

// Transceive exchanges raw data with the tag through the device holding it.
//
// This is one network round trip per command. A chatty sequence spends the
// whole time the user is holding the tag against the device, so prefer the NDEF
// path where it will do.
func (t *Tag) Transceive(data []byte) ([]byte, error) {
	t.mu.RLock()
	writer, deviceID, uid, ok := t.writer, t.sourceDevice, t.uid, t.canTransceive()
	t.mu.RUnlock()

	if !ok {
		return nil, nfc.NewNotSupportedError("Transceive")
	}
	return writer.(TagTransceiver).TransceiveTag(deviceID, uid, data, false)
}

// WriteData writes an encoded NDEF message to the tag through the device
// holding it.
func (t *Tag) WriteData(data []byte) error {
	t.mu.RLock()
	writer, deviceID, uid, ok := t.writer, t.sourceDevice, t.uid, t.canWrite()
	t.mu.RUnlock()

	if !ok {
		return nfc.NewNotSupportedError("WriteData")
	}
	return writer.WriteTag(deviceID, uid, data, nfc.WriteOptions{Overwrite: true, Index: -1})
}

// IsWritable reports whether the tag can currently be written.
func (t *Tag) IsWritable() (bool, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.canWrite(), nil
}

// CanMakeReadOnly reports whether the tag can be locked through its device.
func (t *Tag) CanMakeReadOnly() (bool, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.canLock(), nil
}

// MakeReadOnly permanently locks the tag through the device holding it.
func (t *Tag) MakeReadOnly() error {
	t.mu.RLock()
	writer, deviceID, uid, ok := t.writer, t.sourceDevice, t.uid, t.canLock()
	t.mu.RUnlock()

	if !ok {
		return nfc.NewNotSupportedError("MakeReadOnly")
	}
	return writer.LockTag(deviceID, uid)
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
