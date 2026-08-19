package pcsc

import (
	"errors"
	"fmt"
	"testing"
)

func TestErrorMatchesSentinelByCode(t *testing.T) {
	// Each backend words its messages differently; only the code should decide.
	err := statusError(0x80100069, fmt.Errorf("scardTransmit() returned 0x80100069 [removed card]"))

	if !errors.Is(err, ErrRemovedCard) {
		t.Errorf("errors.Is(%v, ErrRemovedCard) = false, want true", err)
	}
	if errors.Is(err, ErrTimeout) {
		t.Errorf("errors.Is(%v, ErrTimeout) = true, want false", err)
	}
}

func TestErrorKeepsBackendMessage(t *testing.T) {
	const msg = "scard: the user-specified timeout value has expired"
	err := statusError(0x8010000A, errors.New(msg))

	if err.Error() != msg {
		t.Errorf("Error() = %q, want %q", err.Error(), msg)
	}
}

func TestErrorWrappedStillMatches(t *testing.T) {
	err := fmt.Errorf("pcscDevice.Transceive: %w", statusError(0x80100068, errors.New("reset card")))

	if !errors.Is(err, ErrResetCard) {
		t.Errorf("errors.Is(%v, ErrResetCard) = false, want true", err)
	}
}

func TestStatusErrorPassesThroughWithoutCode(t *testing.T) {
	// A call that failed before it reached the library has no status code, and
	// must stay comparable to whatever the backend returned.
	want := errors.New("scardTransmit() not found in pcsc")

	if got := statusError(0, want); !errors.Is(got, want) {
		t.Errorf("statusError(0, err) = %v, want the original error", got)
	}
	if got := statusError(0x8010000A, nil); got != nil {
		t.Errorf("statusError(code, nil) = %v, want nil", got)
	}
}

func TestSentinelErrorMessage(t *testing.T) {
	// The sentinels carry no backend message of their own.
	if got, want := ErrCancelled.Error(), "scard: status 0x80100002"; got != want {
		t.Errorf("ErrCancelled.Error() = %q, want %q", got, want)
	}
}
