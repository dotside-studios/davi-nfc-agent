package logbuf

import (
	"bytes"
	"log"
	"strings"
	"testing"
)

// A channel records under its name at its level, so a driver's failure reads as
// one without anything guessing it from the words.
func TestAChannelRecordsUnderItsNameAtItsLevel(t *testing.T) {
	r := New(16)
	Install(r)
	t.Cleanup(func() { Install(nil) })

	notes := Channel("device", LevelInfo)
	failures := Channel("device", LevelError)

	notes.Print("connected to Mock NFC Reader")
	failures.Print("device I/O error: closing device")

	got := r.Entries()
	if len(got) != 2 {
		t.Fatalf("the ring holds %d entries, want 2", len(got))
	}
	for _, e := range got {
		if e.Source != "[device]" {
			t.Errorf("entry %q is sourced %q, want the channel's name", e.Message, e.Source)
		}
	}
	if got[0].Level != LevelInfo {
		t.Errorf("the note is %q, want info", got[0].Level)
	}
	if got[1].Level != LevelError {
		t.Errorf("the failure is %q, want error", got[1].Level)
	}
}

// A channel built before the ring is installed still writes into it, which is
// what a package-level channel does.
func TestAChannelBuiltBeforeInstallStillLands(t *testing.T) {
	early := Channel("device", LevelError)

	r := New(8)
	Install(r)
	t.Cleanup(func() { Install(nil) })

	early.Print("a failure reported by a channel built at package load")

	if got := r.Entries(); len(got) != 1 || got[0].Level != LevelError {
		t.Errorf("the ring holds %v, want the failure at error level", got)
	}
}

// With nothing installed a channel still writes, so a program that shows its
// log nowhere is not a program that crashes.
func TestAChannelWithNoRingInstalledStillWrites(t *testing.T) {
	Install(nil)

	if _, err := Channel("device", LevelError).Writer().Write([]byte("a line\n")); err != nil {
		t.Errorf("writing with no ring installed: %v", err)
	}
}

// A channel follows the standard logger's output, so a test or a program that
// redirects the process log gets the channels with it.
func TestAChannelFollowsTheStandardLoggersOutput(t *testing.T) {
	Install(nil)

	var buf bytes.Buffer
	was := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(was) })

	Channel("device", LevelError).Print("a redirected failure")

	if !strings.Contains(buf.String(), "a redirected failure") {
		t.Errorf("the redirected log holds %q, want the channel's line", buf.String())
	}
	if !strings.Contains(buf.String(), "[device]") {
		t.Errorf("the redirected line %q does not carry the channel's name", buf.String())
	}
}
