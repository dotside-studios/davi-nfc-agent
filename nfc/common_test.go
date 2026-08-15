package nfc

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsNoCardError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "typed noCardError",
			err:      &noCardError{ReaderName: "ACR122U"},
			expected: true,
		},
		{
			name:     "wrapped noCardError",
			err:      fmt.Errorf("failed: %w", &noCardError{ReaderName: "ACR122U"}),
			expected: true,
		},
		{
			name:     "string match - no card present",
			err:      errors.New("no card present in reader"),
			expected: true,
		},
		{
			name:     "string match - No smart card (uppercase)",
			err:      errors.New("scard: No smart card inserted"),
			expected: true,
		},
		{
			name:     "string match - card is not present",
			err:      errors.New("Card is not present"),
			expected: true,
		},
		{
			name:     "unrelated error",
			err:      errors.New("connection lost"),
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsNoCardError(tt.err); got != tt.expected {
				t.Errorf("IsNoCardError() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestNoCardError_Error(t *testing.T) {
	err := &noCardError{ReaderName: "ACR122U PICC Interface"}
	expected := "no card present in reader ACR122U PICC Interface"

	if got := err.Error(); got != expected {
		t.Errorf("noCardError.Error() = %q, want %q", got, expected)
	}
}

func TestIsCardRemovedError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "typed cardRemovedError",
			err:      NewCardRemovedError(nil),
			expected: true,
		},
		{
			name:     "typed cardRemovedError with cause",
			err:      NewCardRemovedError(errors.New("underlying")),
			expected: true,
		},
		{
			name:     "wrapped cardRemovedError",
			err:      fmt.Errorf("failed: %w", NewCardRemovedError(nil)),
			expected: true,
		},
		{
			name:     "raw string - not detected (must use typed error)",
			err:      errors.New("scard: Card was removed"),
			expected: false, // Only typed errors are detected now
		},
		{
			name:     "raw string reset - not detected",
			err:      errors.New("scard: Reset card"),
			expected: false, // Only typed errors are detected now
		},
		{
			name:     "raw string transaction - not detected",
			err:      errors.New("scard: Transaction failed"),
			expected: false, // Only typed errors are detected now
		},
		{
			name:     "raw string no smart card - not detected",
			err:      errors.New("scard: No smart card"),
			expected: false, // Only typed errors are detected now
		},
		{
			name:     "unrelated error",
			err:      errors.New("connection lost"),
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsCardRemovedError(tt.err); got != tt.expected {
				t.Errorf("IsCardRemovedError() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestCardRemovedError_Error(t *testing.T) {
	// Without cause
	err1 := NewCardRemovedError(nil)
	if got := err1.Error(); got != "card was removed" {
		t.Errorf("Error() = %q, want %q", got, "card was removed")
	}

	// With cause
	cause := errors.New("underlying error")
	err2 := NewCardRemovedError(cause)
	expected := "card was removed: underlying error"
	if got := err2.Error(); got != expected {
		t.Errorf("Error() = %q, want %q", got, expected)
	}
}

func TestIsTimeoutError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "typed ErrTimeout",
			err:      ErrTimeout,
			expected: true,
		},
		{
			name:     "wrapped ErrTimeout",
			err:      fmt.Errorf("failed: %w", ErrTimeout),
			expected: true,
		},
		{
			name:     "string match - operation timed out",
			err:      errors.New("operation timed out"),
			expected: true,
		},
		{
			name:     "string match - timeout",
			err:      errors.New("some timeout error"),
			expected: true,
		},
		{
			name:     "unrelated error",
			err:      errors.New("connection lost"),
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsTimeoutError(tt.err); got != tt.expected {
				t.Errorf("IsTimeoutError() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsIOError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "typed ErrIO",
			err:      ErrIO,
			expected: true,
		},
		{
			name:     "wrapped ErrIO",
			err:      fmt.Errorf("failed: %w", ErrIO),
			expected: true,
		},
		{
			name:     "string match - input/output error",
			err:      errors.New("Input/output error"),
			expected: true,
		},
		{
			name:     "string match - broken pipe",
			err:      errors.New("broken pipe"),
			expected: true,
		},
		{
			name:     "unrelated error",
			err:      errors.New("connection lost"),
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsIOError(tt.err); got != tt.expected {
				t.Errorf("IsIOError() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestIsDeviceClosedError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "typed ErrDeviceClosed",
			err:      ErrDeviceClosed,
			expected: true,
		},
		{
			name:     "wrapped ErrDeviceClosed",
			err:      fmt.Errorf("failed: %w", ErrDeviceClosed),
			expected: true,
		},
		{
			name:     "string match - device closed",
			err:      errors.New("device closed"),
			expected: true,
		},
		{
			// The smartphone device said this, the classifier looked for the
			// other spelling, and so its closure was handled by nothing.
			name:     "string match - device is closed",
			err:      errors.New("getTags: error from device.GetTags: device is closed"),
			expected: true,
		},
		{
			name:     "unrelated error",
			err:      errors.New("connection lost"),
			expected: false,
		},
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsDeviceClosedError(tt.err); got != tt.expected {
				t.Errorf("IsDeviceClosedError() = %v, want %v", got, tt.expected)
			}
		})
	}
}
