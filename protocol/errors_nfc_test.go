package protocol

import (
	"errors"
	"fmt"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

func TestErrorPayloadMapsEveryCode(t *testing.T) {
	for code := nfc.ErrCodeNotSupported; code <= nfc.ErrCodeInvalidData; code++ {
		wire, ok := wireErrorCodes[code]
		if !ok {
			t.Errorf("internal code %d has no wire mapping", code)
			continue
		}
		if wire == ErrCodeUnknownError {
			t.Errorf("internal code %d maps to UNKNOWN_ERROR", code)
		}
	}
}

func TestErrorPayloadPreservesContext(t *testing.T) {
	err := nfc.NewCapacityExceededError("WriteData", "04:A1:B2:C3", 900, 504)

	payload := ErrorPayloadFor(err)

	if payload.Code != ErrCodeCapacityExceeded {
		t.Errorf("Code = %s, want CAPACITY_EXCEEDED", payload.Code)
	}
	if payload.Retryable {
		t.Error("CAPACITY_EXCEEDED should not be retryable")
	}
	if payload.Op != "WriteData" {
		t.Errorf("Op = %q, want WriteData", payload.Op)
	}
	if payload.TagUID != "04:A1:B2:C3" {
		t.Errorf("TagUID = %q, want the tag UID", payload.TagUID)
	}
}

func TestErrorPayloadThroughWrapping(t *testing.T) {
	err := fmt.Errorf("sending to bridge: %w", nfc.NewTagRemovedError("ReadData", errors.New("gone")))

	payload := ErrorPayloadFor(err)

	if payload.Code != ErrCodeTagRemoved {
		t.Errorf("Code = %s, want TAG_REMOVED through the wrap", payload.Code)
	}
	if !payload.Retryable {
		t.Error("TAG_REMOVED should be retryable, the tag can be presented again")
	}
}

// An error we cannot classify must not be advertised as worth retrying.
func TestErrorPayloadUnclassified(t *testing.T) {
	payload := ErrorPayloadFor(errors.New("something went wrong"))

	if payload.Code != ErrCodeUnknownError {
		t.Errorf("Code = %s, want UNKNOWN_ERROR", payload.Code)
	}
	if payload.Retryable {
		t.Error("an unclassified error should not be retryable")
	}
}
