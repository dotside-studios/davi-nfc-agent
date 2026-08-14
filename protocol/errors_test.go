package protocol

import (
	"encoding/json"
	"testing"
)

func TestRetryableClassification(t *testing.T) {
	retryable := []ErrorCode{
		ErrCodeTagRemoved,
		ErrCodeReadFailed,
		ErrCodeWriteFailed,
		ErrCodeTransceiveFailed,
		ErrCodeTagNotConnected,
		ErrCodeTagSendFailed,
		ErrCodeTimeout,
	}
	for _, code := range retryable {
		if !code.Retryable() {
			t.Errorf("%s should be retryable", code)
		}
	}

	permanent := []ErrorCode{
		ErrCodeNotSupported,
		ErrCodeReadOnly,
		ErrCodeCapacityExceeded,
		ErrCodeInvalidData,
		ErrCodeParse,
		ErrCodeInvalidPayload,
		ErrCodeInvalidRequest,
		ErrCodeInvalidMessageType,
		ErrCodeUnknownType,
		ErrCodeInvalidDevice,
		ErrCodeAuthFailed,
		ErrCodeUnknownError,
	}
	for _, code := range permanent {
		if code.Retryable() {
			t.Errorf("%s should not be retryable", code)
		}
	}
}

// Clients switch on `code`, so an error payload must still present the same
// string in the same place it always has.
func TestErrorPayloadKeepsCodeShape(t *testing.T) {
	out, err := json.Marshal(NewErrorPayload(ErrCodeInvalidPayload))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var round map[string]any
	if err := json.Unmarshal(out, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if round["code"] != "INVALID_PAYLOAD" {
		t.Errorf("code = %v, want INVALID_PAYLOAD", round["code"])
	}
	if round["retryable"] != false {
		t.Errorf("retryable = %v, want false", round["retryable"])
	}
	if _, ok := round["op"]; ok {
		t.Error("op should be omitted when empty")
	}
}

func TestErrorPayloadCarriesContext(t *testing.T) {
	out, err := json.Marshal(ErrorPayload{
		Code:      ErrCodeCapacityExceeded,
		Retryable: ErrCodeCapacityExceeded.Retryable(),
		Op:        "WriteData",
		TagUID:    "04:A1:B2:C3",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var round map[string]any
	if err := json.Unmarshal(out, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if round["op"] != "WriteData" {
		t.Errorf("op = %v, want WriteData", round["op"])
	}
	if round["tagUID"] != "04:A1:B2:C3" {
		t.Errorf("tagUID = %v, want the tag UID", round["tagUID"])
	}
	if round["retryable"] != false {
		t.Error("a tag too small for the data will not get bigger on retry")
	}
}
