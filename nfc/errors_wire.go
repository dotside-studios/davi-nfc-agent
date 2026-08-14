package nfc

import (
	"errors"

	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

var wireErrorCodes = map[ErrorCode]protocol.ErrorCode{
	ErrCodeNotSupported:     protocol.ErrCodeNotSupported,
	ErrCodeTagRemoved:       protocol.ErrCodeTagRemoved,
	ErrCodeAuthFailed:       protocol.ErrCodeAuthFailed,
	ErrCodeReadFailed:       protocol.ErrCodeReadFailed,
	ErrCodeWriteFailed:      protocol.ErrCodeWriteFailed,
	ErrCodeTransceiveFailed: protocol.ErrCodeTransceiveFailed,
	ErrCodeTagNotConnected:  protocol.ErrCodeTagNotConnected,
	ErrCodeReadOnly:         protocol.ErrCodeReadOnly,
	ErrCodeCapacityExceeded: protocol.ErrCodeCapacityExceeded,
	ErrCodeInvalidData:      protocol.ErrCodeInvalidData,
}

// InternalErrorCode maps a wire code back to an internal one, for outcomes
// reported by a remote device. Codes with no internal equivalent — protocol
// faults, or a device sending something we do not know — fall back to the
// caller's own notion of what failed.
func InternalErrorCode(wire protocol.ErrorCode, fallback ErrorCode) ErrorCode {
	for internal, mapped := range wireErrorCodes {
		if mapped == wire {
			return internal
		}
	}
	return fallback
}

// WireError projects an error onto the wire taxonomy. An NFCError carries its
// code, operation, and tag through; anything else lands on UNKNOWN_ERROR, which
// is not retryable — an error we cannot classify is not one we should encourage
// a device to repeat.
func WireError(err error) protocol.ErrorPayload {
	var nfcErr *NFCError
	if !errors.As(err, &nfcErr) {
		return protocol.NewErrorPayload(protocol.ErrCodeUnknownError)
	}

	code, ok := wireErrorCodes[nfcErr.Code]
	if !ok {
		code = protocol.ErrCodeUnknownError
	}

	return protocol.ErrorPayload{
		Code:      code,
		Retryable: code.Retryable(),
		Op:        nfcErr.Op,
		TagUID:    nfcErr.TagUID,
	}
}
