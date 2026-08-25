package nfc

import (
	"errors"
	"strings"
	"time"
)

// NFCData represents the data read from an NFC tag including any potential errors.
// ScannedTag is a tag as a source reported it, before anything has been read
// off it. It is what a manager publishes; what consumers get is [NFCData],
// which the supervisor makes from this.
type ScannedTag struct {
	// Device names what reported the tag.
	Device string

	// Tag is what was scanned, nil for a tag that has left.
	Tag Tag

	// Err is what went wrong instead, if anything.
	Err error
}

type NFCData struct {
	// Device names what produced the scan: the reader it was presented to, or
	// the phone that reported it. Empty for a scan from a source that does not
	// say, such as one decoded from the wire.
	Device string

	Card *Card // The detected card, nil if no card is present
	Err  error // Error that occurred during detection/reading
}

// DeviceStatus represents the status of the NFC device.
// This type might be used by the main application to display status.
type DeviceStatus struct {
	// Device names the reader this describes.
	Device string

	Connected   bool
	Message     string
	CardPresent bool
}

// Constants for NFC operations
const (
	MaxRetries          = 5
	BaseDelay           = 500 * time.Millisecond
	MaxReconnectTries   = 10
	ReconnectDelay      = time.Second * 2
	DeviceCheckInterval = time.Second * 2 // Interval to check for new devices
	DeviceEnumRetries   = 3               // Number of retries for device enumeration
)

// TagType represents the type of NFC tag as a string.
type TagType string

// Constants for common tag types
const (
	TagTypeMifareClassic TagType = "MIFARE_Classic"
	TagTypeType4         TagType = "Type4"
	TagTypeUnknown       TagType = "Unknown"
)

// Sentinel errors for device operations
var (
	// ErrTimeout indicates a timeout occurred during device communication
	ErrTimeout = errors.New("device operation timed out")

	// ErrDeviceClosed indicates the device connection was closed
	ErrDeviceClosed = errors.New("device closed")

	// ErrTagUIDMismatch indicates the tag presented is not the one the request
	// named. Not retryable: a blind retry would reach whichever card is next.
	ErrTagUIDMismatch = errors.New("tag UID mismatch")

	// ErrIO indicates an input/output error with the device
	ErrIO = errors.New("device I/O error")

	// ErrDeviceConfig indicates a device configuration error
	ErrDeviceConfig = errors.New("device configuration error")

	// ErrCooldownRequired indicates the device needs a cooldown period
	ErrCooldownRequired = errors.New("device cooldown required")

	// ErrACR122Specific indicates an ACR122-specific error requiring cooldown
	ErrACR122Specific = errors.New("ACR122 device error")
)

// noCardError is returned when attempting to connect to a reader with no card present.
// This is a normal condition for NFC readers and should not be treated as a device error.
type noCardError struct {
	ReaderName string
}

func (e *noCardError) Error() string {
	return "no card present in reader " + e.ReaderName
}

// NewNoCardError reports that a reader has no card on it, which IsNoCardError
// recognises. Reader backends return it instead of a device error, because an
// empty reader is an ordinary state rather than a fault.
func NewNoCardError(readerName string) error {
	return &noCardError{ReaderName: readerName}
}

// IsNoCardError checks if an error indicates no card is present in the reader.
// This is a normal condition and should not be logged as a device error.
func IsNoCardError(err error) bool {
	if err == nil {
		return false
	}
	// Check for typed error
	var noCard *noCardError
	if errors.As(err, &noCard) {
		return true
	}
	// Fallback to string matching for legacy errors
	errLower := strings.ToLower(err.Error())
	return strings.Contains(errLower, "no card present") ||
		strings.Contains(errLower, "no smart card") ||
		strings.Contains(errLower, "card is not present")
}

// unsupportedTagError is returned when a tag is present but its type is not supported.
// This allows the system to wait for the card to be removed rather than retrying.
type unsupportedTagError struct {
	ATR string
}

func (e *unsupportedTagError) Error() string {
	return "unsupported tag type (ATR: " + e.ATR + ")"
}

// NewUnsupportedTagError creates an unsupported tag error.
func NewUnsupportedTagError(atr string) error {
	return &unsupportedTagError{ATR: atr}
}

// IsUnsupportedTagError checks if an error indicates the tag type is not supported.
func IsUnsupportedTagError(err error) bool {
	if err == nil {
		return false
	}
	var unsupported *unsupportedTagError
	return errors.As(err, &unsupported)
}

// cardRemovedError indicates the card was removed during operation.
// This requires the device connection to be closed and reopened.
type cardRemovedError struct {
	Cause error
}

func (e *cardRemovedError) Error() string {
	if e.Cause != nil {
		return "card was removed: " + e.Cause.Error()
	}
	return "card was removed"
}

func (e *cardRemovedError) Unwrap() error {
	return e.Cause
}

// NewCardRemovedError creates a card removed error.
func NewCardRemovedError(cause error) error {
	return &cardRemovedError{Cause: cause}
}

// IsCardRemovedError checks if an error indicates the card was removed during operation.
// This requires device reconnection to detect new cards.
// All card removal errors are created via NewCardRemovedError() at the device layer.
func IsCardRemovedError(err error) bool {
	if err == nil {
		return false
	}
	var cardRemoved *cardRemovedError
	return errors.As(err, &cardRemoved)
}

// Error checking helper functions
// These functions check for both typed errors and legacy string-based errors
// for backward compatibility during the transition period.

func IsTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	// Check for typed error first
	if errors.Is(err, ErrTimeout) {
		return true
	}
	// Fallback to string matching for legacy errors
	errStr := err.Error()
	return strings.Contains(errStr, "Operation timed out") ||
		strings.Contains(errStr, "operation timed out") ||
		strings.Contains(errStr, "Unable to write to USB") ||
		strings.Contains(errStr, "timeout")
}

func IsDeviceClosedError(err error) bool {
	if err == nil {
		return false
	}
	// Check for typed error first
	if errors.Is(err, ErrDeviceClosed) {
		return true
	}
	// Fallback to string matching for legacy errors. Both spellings, because a
	// classifier that recognizes only the one its own sentinel happens to use
	// silently declines every error phrased the other way, which is what the
	// smartphone device did, leaving its closure unhandled.
	errStr := err.Error()
	return strings.Contains(errStr, "device closed") ||
		strings.Contains(errStr, "device is closed")
}

func IsIOError(err error) bool {
	if err == nil {
		return false
	}
	// Check for typed error first
	if errors.Is(err, ErrIO) {
		return true
	}
	// Fallback to string matching for legacy errors
	errStr := err.Error()
	return strings.Contains(errStr, "input / output error") ||
		strings.Contains(errStr, "Input/output error") ||
		strings.Contains(errStr, "i/o error") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "Operation not permitted")
}

func IsDeviceConfigError(err error) bool {
	if err == nil {
		return false
	}
	// Check for typed error first
	if errors.Is(err, ErrDeviceConfig) {
		return true
	}
	// Fallback to string matching for legacy errors
	errStr := err.Error()
	return strings.Contains(errStr, "device not configured") ||
		strings.Contains(errStr, "Device not configured") ||
		strings.Contains(errStr, "Unable to write to USB") ||
		strings.Contains(errStr, "RDR_to_PC_DataBlock")
}

func IsACR122Error(err error) bool {
	if err == nil {
		return false
	}
	// Check for typed error first
	if errors.Is(err, ErrACR122Specific) {
		return true
	}
	// Fallback to string matching for ACR122-specific patterns
	errStr := err.Error()
	return strings.Contains(errStr, "Operation not permitted") ||
		strings.Contains(errStr, "broken pipe") ||
		strings.Contains(errStr, "RDR_to_PC_DataBlock")
}
