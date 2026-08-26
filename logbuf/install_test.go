package logbuf

import (
	"log"
	"testing"
)

// A line reaches the ring once, at the level its channel states. Sending the
// standard logger's output to the ring as well recorded every channel line
// twice: once at info through that path, once at its own level.
func TestAChannelLineIsRecordedOnce(t *testing.T) {
	r := New(16)
	Install(r)
	t.Cleanup(func() { Install(nil) })

	was := log.Writer()
	log.SetOutput(discard{})
	t.Cleanup(func() { log.SetOutput(was) })

	Channel("device", LevelError).Print("one failure")

	got := r.Entries()
	if len(got) != 1 {
		t.Fatalf("one logged line was recorded %d times: %v", len(got), got)
	}
	if got[0].Level != LevelError {
		t.Errorf("the line is %q, want the level its channel states", got[0].Level)
	}
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }
