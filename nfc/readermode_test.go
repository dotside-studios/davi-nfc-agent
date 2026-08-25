package nfc

import "testing"

// A mode's name is what it travels under, so the pair has to round trip: a mode
// that comes back as something else silently changes what the reader will do.
func TestReaderModeNamesRoundTrip(t *testing.T) {
	for _, mode := range []ReaderMode{ModeReadWrite, ModeReadOnly, ModeWriteOnly} {
		if got := ParseReaderMode(mode.String()); got != mode {
			t.Errorf("ParseReaderMode(%q) = %v, want %v", mode.String(), got, mode)
		}
	}
}

// An unknown name leaves the reader able to work.
func TestUnknownModeNameReadsAsReadWrite(t *testing.T) {
	for _, name := range []string{"", "sideways", "READ"} {
		if got := ParseReaderMode(name); got != ModeReadWrite {
			t.Errorf("ParseReaderMode(%q) = %v, want ModeReadWrite", name, got)
		}
	}
}
