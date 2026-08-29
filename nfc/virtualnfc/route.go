package virtualnfc

import (
	"fmt"
	"sync"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// Route performs a routed tag's operations against wherever the tag actually
// lives — a phone across a WebSocket, a peer machine, an in-process store — and
// reports the capability bounds the backend imposes on every tag it carries.
//
// The tag's UID is passed on every call so a single Route can serve every tag a
// device holds.
type Route interface {
	CapabilitySource
	Write(uid string, ndef []byte, lock bool) error
	Lock(uid string) error
	Transceive(uid string, data []byte, raw bool) ([]byte, error)
}

// RoutedTagConfig configures a RoutedTag. Only UID and Route are required.
type RoutedTagConfig struct {
	UID        string
	Type       string
	Technology string

	// Declared is what the source reported about this specific tag when it
	// scanned it, or nil if it reported nothing. Three-valued; see
	// MergeCapabilities.
	Declared *nfc.TagCapabilities

	// Snapshot is the tag data captured at scan time, returned by ReadData.
	Snapshot []byte

	// NDEF is the parsed message captured at scan time, if any.
	NDEF *nfc.NDEFMessage

	// Route reaches the tag for write/lock/transceive. A nil Route yields a
	// read-only tag.
	Route Route
}

// RoutedTag is an nfc.Tag whose writes, locks and raw exchanges are routed to
// wherever the tag lives, with reads served from the scan-time snapshot. An
// operation the merged capabilities do not permit fails with a typed
// not-supported error rather than reaching the Route.
type RoutedTag struct {
	nfc.BaseTag

	uid        string
	tagType    string
	technology string
	declared   *nfc.TagCapabilities
	snapshot   []byte
	ndefMsg    *nfc.NDEFMessage
	route      Route
	mu         sync.RWMutex
}

// NewRoutedTag builds a routed tag from cfg.
func NewRoutedTag(cfg RoutedTagConfig) *RoutedTag {
	return &RoutedTag{
		uid:        cfg.UID,
		tagType:    cfg.Type,
		technology: cfg.Technology,
		declared:   cfg.Declared,
		snapshot:   cfg.Snapshot,
		ndefMsg:    cfg.NDEF,
		route:      cfg.Route,
	}
}

// UID returns the tag's unique identifier.
func (t *RoutedTag) UID() string { return t.uid }

// Type returns the tag type as a string.
func (t *RoutedTag) Type() string { return t.tagType }

// NumericType returns 0: a routed tag has no freefare numeric type.
func (t *RoutedTag) NumericType() int { return 0 }

// Capabilities merges what the source declared for this tag with what its Route
// can carry.
func (t *RoutedTag) Capabilities() nfc.TagCapabilities {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return MergeCapabilities(t.declared, t.route, t.tagType, t.technology, t.ndefMsg != nil || t.snapshot != nil)
}

// ReadData returns the scan-time snapshot.
func (t *RoutedTag) ReadData() ([]byte, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.snapshot, nil
}

// GetNDEFMessage returns the parsed message captured at scan time, if any.
func (t *RoutedTag) GetNDEFMessage() (*nfc.NDEFMessage, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	if t.ndefMsg != nil {
		return t.ndefMsg, nil
	}
	return nil, fmt.Errorf("no NDEF message available")
}

// Transceive exchanges raw data with the tag through its Route.
func (t *RoutedTag) Transceive(data []byte) ([]byte, error) {
	t.mu.RLock()
	route, uid, ok := t.route, t.uid, TransceiveAllowed(t.declared, t.route)
	t.mu.RUnlock()
	if !ok {
		return nil, nfc.NewNotSupportedError("Transceive")
	}
	return route.Transceive(uid, data, false)
}

// WriteData writes an encoded NDEF message to the tag through its Route.
func (t *RoutedTag) WriteData(data []byte) error {
	t.mu.RLock()
	route, uid, ok := t.route, t.uid, WriteAllowed(t.declared, t.route)
	t.mu.RUnlock()
	if !ok {
		return nfc.NewNotSupportedError("WriteData")
	}
	return route.Write(uid, data, false)
}

// WriteDataAndLock writes and locks in one routed exchange, so a failure cannot
// leave the data written and the lock not applied. This is what
// nfc.AtomicLockWriter exists for.
func (t *RoutedTag) WriteDataAndLock(data []byte) error {
	t.mu.RLock()
	route, uid := t.route, t.uid
	canWrite, canLock := WriteAllowed(t.declared, t.route), LockAllowed(t.declared, t.route)
	t.mu.RUnlock()
	if !canWrite {
		return nfc.NewNotSupportedError("WriteData")
	}
	if !canLock {
		return nfc.NewNotSupportedError("MakeReadOnly")
	}
	return route.Write(uid, data, true)
}

// IsWritable reports whether a write would currently reach the tag.
func (t *RoutedTag) IsWritable() (bool, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return WriteAllowed(t.declared, t.route), nil
}

// CanMakeReadOnly reports whether the tag can be locked through its Route.
func (t *RoutedTag) CanMakeReadOnly() (bool, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return LockAllowed(t.declared, t.route), nil
}

// MakeReadOnly permanently locks the tag through its Route.
func (t *RoutedTag) MakeReadOnly() error {
	t.mu.RLock()
	route, uid, ok := t.route, t.uid, LockAllowed(t.declared, t.route)
	t.mu.RUnlock()
	if !ok {
		return nfc.NewNotSupportedError("MakeReadOnly")
	}
	return route.Lock(uid)
}

// RoutedTag is a full nfc.Tag and folds a lock into one exchange.
var (
	_ nfc.Tag              = (*RoutedTag)(nil)
	_ nfc.AtomicLockWriter = (*RoutedTag)(nil)
)
