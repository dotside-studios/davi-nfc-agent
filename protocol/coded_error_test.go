package protocol

import (
	"errors"
	"testing"
)

// A refusal that never reached a tag still has to reach the client with a code
// it can act on.
func TestErrorPayloadForCodedError(t *testing.T) {
	err := Errorf(ErrCodeTagNotNamed, "request must name the tag it applies to")

	payload := ErrorPayloadFor(err)
	if payload.Code != ErrCodeTagNotNamed {
		t.Errorf("code = %q, want %q", payload.Code, ErrCodeTagNotNamed)
	}
	if payload.Retryable {
		t.Error("TAG_NOT_NAMED must not be advertised as retryable")
	}
}

func TestCodedErrorWrapsItsCause(t *testing.T) {
	cause := errors.New("underlying")
	err := WrapError(ErrCodeDeviceGone, cause, "device stopped answering")

	if !errors.Is(err, cause) {
		t.Error("the cause must survive wrapping")
	}
	if ErrorPayloadFor(err).Code != ErrCodeDeviceGone {
		t.Errorf("code = %q, want %q", ErrorPayloadFor(err).Code, ErrCodeDeviceGone)
	}
}

// An error carrying no code of its own is still unknown, which is what the
// caller's fallback is for.
func TestErrorPayloadForPlainError(t *testing.T) {
	if got := ErrorPayloadFor(errors.New("plain")).Code; got != ErrCodeUnknownError {
		t.Errorf("code = %q, want %q", got, ErrCodeUnknownError)
	}
}
