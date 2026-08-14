package nfc

import (
	"errors"
	"fmt"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

func TestWireErrorMapsEveryCode(t *testing.T) {
	for code := ErrCodeNotSupported; code <= ErrCodeInvalidData; code++ {
		wire, ok := wireErrorCodes[code]
		if !ok {
			t.Errorf("internal code %d has no wire mapping", code)
			continue
		}
		if wire == protocol.ErrCodeUnknownError {
			t.Errorf("internal code %d maps to UNKNOWN_ERROR", code)
		}
	}
}

func TestWireErrorPreservesContext(t *testing.T) {
	err := NewCapacityExceededError("WriteData", "04:A1:B2:C3", 900, 504)

	payload := WireError(err)

	if payload.Code != protocol.ErrCodeCapacityExceeded {
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

func TestWireErrorThroughWrapping(t *testing.T) {
	err := fmt.Errorf("sending to bridge: %w", NewTagRemovedError("ReadData", errors.New("gone")))

	payload := WireError(err)

	if payload.Code != protocol.ErrCodeTagRemoved {
		t.Errorf("Code = %s, want TAG_REMOVED through the wrap", payload.Code)
	}
	if !payload.Retryable {
		t.Error("TAG_REMOVED should be retryable — the tag can be presented again")
	}
}

// An error we cannot classify must not be advertised as worth retrying.
func TestWireErrorUnclassified(t *testing.T) {
	payload := WireError(errors.New("something went wrong"))

	if payload.Code != protocol.ErrCodeUnknownError {
		t.Errorf("Code = %s, want UNKNOWN_ERROR", payload.Code)
	}
	if payload.Retryable {
		t.Error("an unclassified error should not be retryable")
	}
}
