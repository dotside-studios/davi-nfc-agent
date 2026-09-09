package nfc

import "testing"

// FuzzExplain proves Explain answers any input without panicking, and holds a
// few invariants a caller relies on for safety. It matters because Explain now
// decodes bytes typed by an operator and sent by a client over the raw APDU
// channel: a panic there is a denial of service, and a command that changes a
// tag but is not reported as mutating is a missed warning.
//
// The seed corpus runs on every `go test`; use `go test -fuzz=FuzzExplain` to
// fuzz actively.
func FuzzExplain(f *testing.F) {
	seeds := [][]byte{
		{},
		{0xFF},
		{0x00, 0xEE, 0x00, 0x00},
		{0xFF, 0xCA, 0x00, 0x00, 0x00},
		{0xFF, 0xD6, 0x00, 0x03, 0x04, 0x00, 0x00, 0x00, 0x00},
		{0x00, 0xA4, 0x04, 0x00, 0x07, 0xD2, 0x76, 0x00, 0x00, 0x85, 0x01, 0x01, 0x00},
		{0x00, 0xA4, 0x00, 0x0C, 0x02, 0xE1, 0x03},
		{0x90, 0x60, 0x00, 0x00, 0x00},
		{0xFF, 0x00, 0x00, 0x00, 0x01, 0x60, 0x00},       // direct transmit wrapper
		{0x00, 0xA4, 0x04, 0x00, 0x05, 0xE1, 0x03, 0x00}, // malformed length
		{0x30, 0x00},
		{0xA2, 0x03, 0x00, 0x00, 0x00, 0x00},
	}
	for _, s := range seeds {
		f.Add(s, false)
		f.Add(s, true)
	}

	f.Fuzz(func(t *testing.T, cmd []byte, raw bool) {
		ex := Explain(cmd, raw)

		// Every answer has a human-readable summary; the console and the audit
		// log both render it unconditionally.
		if ex.Summary == "" {
			t.Errorf("empty summary for % X (raw=%v)", cmd, raw)
		}

		// A command that changes or permanently alters the tag must be reported
		// as mutating, and so must one the decoder does not recognise — the
		// safety net errs toward caution. The empty command is the one exception:
		// nothing is sent, so nothing mutates.
		if len(cmd) > 0 {
			switch ex.Class {
			case ClassWrite, ClassLock, ClassUnknown:
				if !ex.Mutating {
					t.Errorf("class %q not marked mutating: % X (raw=%v)", ex.Class, cmd, raw)
				}
			}
			if !ex.Recognized && !ex.Mutating {
				t.Errorf("unrecognised command not marked mutating: % X (raw=%v)", cmd, raw)
			}
		}

		// Rendering must not panic either — it is what a log line and a test
		// failure call.
		_ = ex.String()
	})
}
