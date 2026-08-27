package protocol

// ErrorCode identifies a failure on the wire. The values are stable strings that
// clients switch on, and split into two families: protocol errors, raised
// by the bridge itself, and NFC errors, which mirror nfc.ErrorCode and describe
// something that happened at the tag.
type ErrorCode string

// Protocol errors. These strings predate the taxonomy and must not change.
const (
	ErrCodeParse              ErrorCode = "PARSE_ERROR"
	ErrCodeInvalidPayload     ErrorCode = "INVALID_PAYLOAD"
	ErrCodeInvalidRequest     ErrorCode = "INVALID_REQUEST"
	ErrCodeInvalidMessageType ErrorCode = "INVALID_MESSAGE_TYPE"
	ErrCodeUnknownType        ErrorCode = "UNKNOWN_TYPE"
	ErrCodeInvalidDevice      ErrorCode = "INVALID_DEVICE"
	ErrCodeRegistrationFailed ErrorCode = "REGISTRATION_FAILED"
	ErrCodeTagSendFailed      ErrorCode = "TAG_SEND_FAILED"
	ErrCodeReadError          ErrorCode = "READ_ERROR"
)

// Client-facing operation failures. These strings predate the taxonomy too.
const (
	ErrCodeLockFailed         ErrorCode = "LOCK_FAILED"
	ErrCodeCapabilitiesFailed ErrorCode = "CAPABILITIES_FAILED"
	ErrCodeSessionLocked      ErrorCode = "SESSION_LOCKED"
	ErrCodeNoCard             ErrorCode = "NO_CARD"
)

// Protocol errors added with the taxonomy.
const (
	// ErrCodeTagMismatch reports that the tag resolved is not the one named.
	// Re-read the tag rather than retrying.
	ErrCodeTagMismatch ErrorCode = "TAG_MISMATCH"

	// ErrCodeTagNotNamed reports that a request named no tag and did not ask
	// for one to be guessed.
	ErrCodeTagNotNamed ErrorCode = "TAG_NOT_NAMED"

	ErrCodeTimeout      ErrorCode = "TIMEOUT"
	ErrCodeDeviceGone   ErrorCode = "DEVICE_GONE"
	ErrCodeInternal     ErrorCode = "INTERNAL_ERROR"
	ErrCodeUnknownError ErrorCode = "UNKNOWN_ERROR"
)

// NFC errors, one per nfc.ErrorCode.
const (
	ErrCodeNotSupported     ErrorCode = "NOT_SUPPORTED"
	ErrCodeTagRemoved       ErrorCode = "TAG_REMOVED"
	ErrCodeAuthFailed       ErrorCode = "AUTH_FAILED"
	ErrCodeReadFailed       ErrorCode = "READ_FAILED"
	ErrCodeWriteFailed      ErrorCode = "WRITE_FAILED"
	ErrCodeTransceiveFailed ErrorCode = "TRANSCEIVE_FAILED"
	ErrCodeTagNotConnected  ErrorCode = "TAG_NOT_CONNECTED"
	ErrCodeReadOnly         ErrorCode = "READ_ONLY"
	ErrCodeCapacityExceeded ErrorCode = "CAPACITY_EXCEEDED"
	ErrCodeInvalidData      ErrorCode = "INVALID_DATA"

	// ErrCodeMultipleTags reports more than one tag in the field where the
	// operation needs exactly one. Not retryable: the user has to separate
	// them first.
	ErrCodeMultipleTags ErrorCode = "MULTIPLE_TAGS"

	// ErrCodeBusy reports that the agent could not start the operation because
	// it is still working on an earlier one: a reader whose previous operation
	// was abandoned but has not finished, or a connection with more requests
	// outstanding than it can queue. Retryable once the earlier work drains.
	ErrCodeBusy ErrorCode = "BUSY"
)

// ErrorPayload is the payload of an error response. `code` carries the same
// strings as before the taxonomy existed; everything else is additive, so a
// client that only reads `code` is unaffected.
type ErrorPayload struct {
	Code      ErrorCode `json:"code"`
	Retryable bool      `json:"retryable"`
	Op        string    `json:"op,omitempty"`     // Operation that failed, e.g. "WriteData"
	TagUID    string    `json:"tagUID,omitempty"` // Tag involved, when there is one
}

// retryableCodes are failures where the same request could succeed if repeated:
// transient I/O, a tag that moved, a full queue. Everything else is a decision
// (malformed input, an unsupported operation, a locked tag) and repeating it
// only wastes a round trip.
var retryableCodes = map[ErrorCode]bool{
	ErrCodeTagRemoved:       true,
	ErrCodeReadFailed:       true,
	ErrCodeWriteFailed:      true,
	ErrCodeTransceiveFailed: true,
	ErrCodeTagNotConnected:  true,
	ErrCodeTagSendFailed:    true,
	ErrCodeReadError:        true,
	ErrCodeNoCard:           true,
	ErrCodeTimeout:          true,
	ErrCodeInternal:         true,
	ErrCodeBusy:             true,
}

// Retryable reports whether repeating the request could plausibly succeed.
func (c ErrorCode) Retryable() bool {
	return retryableCodes[c]
}

// NewErrorPayload builds the payload for a bare protocol error.
func NewErrorPayload(code ErrorCode) ErrorPayload {
	return ErrorPayload{Code: code, Retryable: code.Retryable()}
}
