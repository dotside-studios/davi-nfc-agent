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
