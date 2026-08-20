package protocol

import "fmt"

// CodedError is an error that names its own wire code.
//
// Failures reach a client two ways: from the tag, as an *nfc.NFCError carrying
// an nfc code, and from the agent itself, as a refusal that never touched a tag
// -- no source is holding it, the reader is in read-only mode, the request
// named no tag. The second kind had no way to carry a code through an ordinary
// error, so it travelled beside one in a response struct, which is why an
// operation could only be reported across a channel rather than returned.
type CodedError struct {
	Code    ErrorCode
	Message string
	Cause   error
}

func (e *CodedError) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s: %v", e.Message, e.Cause)
	}
	return e.Message
}

func (e *CodedError) Unwrap() error { return e.Cause }

// Errorf builds a CodedError.
func Errorf(code ErrorCode, format string, args ...any) error {
	return &CodedError{Code: code, Message: fmt.Sprintf(format, args...)}
}

// WrapError builds a CodedError preserving what caused it.
func WrapError(code ErrorCode, cause error, format string, args ...any) error {
	return &CodedError{Code: code, Message: fmt.Sprintf(format, args...), Cause: cause}
}
