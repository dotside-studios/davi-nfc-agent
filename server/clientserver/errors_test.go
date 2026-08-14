package clientserver

import (
	"encoding/json"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

// A code reported by the reader or device must survive to the client rather
// than collapsing into the generic per-operation label.
func TestErrorPayloadPrefersReportedCode(t *testing.T) {
	payload := errorPayloadOrDefault(protocol.ErrCodeReadOnly, protocol.ErrCodeWriteFailed)

	if payload.Code != protocol.ErrCodeReadOnly {
		t.Errorf("Code = %s, want READ_ONLY", payload.Code)
	}
	if payload.Retryable {
		t.Error("READ_ONLY should not be retryable")
	}
}

func TestErrorPayloadFallsBack(t *testing.T) {
	payload := errorPayloadOrDefault("", protocol.ErrCodeWriteFailed)

	if payload.Code != protocol.ErrCodeWriteFailed {
		t.Errorf("Code = %s, want WRITE_FAILED", payload.Code)
	}
	if !payload.Retryable {
		t.Error("WRITE_FAILED should be retryable")
	}
}

// Existing clients read payload.code as a string; that must not change shape.
func TestErrorPayloadKeepsCodeKey(t *testing.T) {
	out, err := json.Marshal(errorPayloadOrDefault(protocol.ErrCodeTagRemoved, protocol.ErrCodeWriteFailed))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var round map[string]any
	if err := json.Unmarshal(out, &round); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if round["code"] != "TAG_REMOVED" {
		t.Errorf("code = %v, want TAG_REMOVED", round["code"])
	}
	if round["retryable"] != true {
		t.Errorf("retryable = %v, want true", round["retryable"])
	}
}
