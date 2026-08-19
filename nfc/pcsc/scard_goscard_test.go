//go:build !cgopcsc

package pcsc

import (
	"testing"
	"time"
)

func TestNewTimeout(t *testing.T) {
	const infinite = 0xFFFFFFFF

	tests := []struct {
		name string
		in   time.Duration
		want uint64
	}{
		{"poll", 0, 0},
		{"bounded", 500 * time.Millisecond, 500},
		{"sub-millisecond rounds down", 500 * time.Nanosecond, 0},
		{"infinite", infiniteTimeout, infinite},
		{"any negative is infinite", -3 * time.Second, infinite},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			timeout := newTimeout(tt.in)
			if got := uint64(timeout.Milliseconds()); got != tt.want {
				t.Errorf("newTimeout(%v) = %d ms, want %d ms", tt.in, got, tt.want)
			}
		})
	}
}
