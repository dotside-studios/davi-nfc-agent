package nfc

import (
	"errors"
	"strings"
	"testing"
)

func TestTraceOutcome(t *testing.T) {
	// A success carries the status word and the data length, never the data.
	got := traceOutcome([]byte{0xDE, 0xAD, 0x90, 0x00}, nil)
	if !strings.Contains(got, "9000") {
		t.Errorf("outcome %q does not report the status word", got)
	}
	if !strings.Contains(got, "2 data byte") {
		t.Errorf("outcome %q does not report the data length", got)
	}
	if strings.Contains(strings.ToUpper(got), "DEAD") {
		t.Errorf("outcome %q echoes the response data", got)
	}

	// An error is reported as a failure.
	if got := traceOutcome(nil, errors.New("card removed")); !strings.Contains(got, "card removed") {
		t.Errorf("outcome %q does not carry the error", got)
	}
}

func TestTraceUID(t *testing.T) {
	if got := traceUID(""); got == "" {
		t.Error("an empty UID should render as a placeholder, not blank")
	}
	if got := traceUID("04A1B2C3"); got != "04A1B2C3" {
		t.Errorf("traceUID = %q, want the UID unchanged", got)
	}
}
