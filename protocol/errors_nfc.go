package protocol

import (
	"errors"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// wireErrorCodes is the whole of the translation between the two taxonomies.
// It lives here rather than in nfc so that the domain does not have to know a
// wire exists.
var wireErrorCodes = map[nfc.ErrorCode]ErrorCode{
	nfc.ErrCodeNotSupported:     ErrCodeNotSupported,
	nfc.ErrCodeTagRemoved:       ErrCodeTagRemoved,
	nfc.ErrCodeAuthFailed:       ErrCodeAuthFailed,
	nfc.ErrCodeReadFailed:       ErrCodeReadFailed,
	nfc.ErrCodeWriteFailed:      ErrCodeWriteFailed,
	nfc.ErrCodeTransceiveFailed: ErrCodeTransceiveFailed,
	nfc.ErrCodeTagNotConnected:  ErrCodeTagNotConnected,
	nfc.ErrCodeReadOnly:         ErrCodeReadOnly,
	nfc.ErrCodeCapacityExceeded: ErrCodeCapacityExceeded,
	nfc.ErrCodeInvalidData:      ErrCodeInvalidData,
}

// InternalErrorCode maps a wire code back to the nfc code it came from, for
// outcomes reported by a remote device. A wire code with no counterpart (a
// protocol fault, or a device sending something we do not know) falls back to
// the caller's own notion of what failed.
func InternalErrorCode(wire ErrorCode, fallback nfc.ErrorCode) nfc.ErrorCode {
	for internal, mapped := range wireErrorCodes {
		if mapped == wire {
			return internal
		}
	}
	return fallback
}

// ErrorPayloadFor projects an error from the nfc package onto the wire
// taxonomy. An NFCError carries its code, operation and tag through; anything
// else lands on UNKNOWN_ERROR, which is not retryable, because an error we
// cannot classify is not one we should encourage a device to repeat.
func ErrorPayloadFor(err error) ErrorPayload {
	var nfcErr *nfc.NFCError
	if !errors.As(err, &nfcErr) {
		return NewErrorPayload(ErrCodeUnknownError)
	}

	code, ok := wireErrorCodes[nfcErr.Code]
	if !ok {
		code = ErrCodeUnknownError
	}

	return ErrorPayload{
		Code:      code,
		Retryable: code.Retryable(),
		Op:        nfcErr.Op,
		TagUID:    nfcErr.TagUID,
	}
}
