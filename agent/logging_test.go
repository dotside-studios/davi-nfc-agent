package agent

import (
	"io"
	"log"
	"strings"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// ringAgent is an agent whose log the console could read back.
func ringAgent(t *testing.T, plugins ...Plugin) (*Agent, *logbuf.Ring) {
	t.Helper()

	ring := logbuf.New(64)
	return New(Config{
		Manager: nfc.NewMockManager(),
		Logs:    ring,
		Plugins: plugins,
	}), ring
}

// find returns the first captured entry whose message contains want.
func find(t *testing.T, ring *logbuf.Ring, want string) logbuf.Entry {
	t.Helper()

	var seen []string
	for _, entry := range ring.Entries() {
		if strings.Contains(entry.Message, want) {
			return entry
		}
		seen = append(seen, entry.Source+" "+entry.Message)
	}
	t.Fatalf("no entry containing %q; the ring holds %v", want, seen)
	return logbuf.Entry{}
}

// What the agent logs reaches the ring the console reads. The ring was stored
// on the agent and connected to nothing, so an agent started from a desktop
// launcher wrote its diagnostics to a stderr nobody sees, and the console's log
// showed the drivers' output and none of the agent's.
func TestWhatTheAgentLogsReachesTheConsole(t *testing.T) {
	a, ring := ringAgent(t)

	a.Logger().Printf("a line from the agent")

	entry := find(t, ring, "a line from the agent")
	if entry.Source != "[agent]" {
		t.Errorf("the entry is sourced %q, want the agent", entry.Source)
	}
}

// A caller that brings its own logger keeps it, ring or no ring: where it
// writes is then that caller's to arrange.
func TestASuppliedLoggerIsLeftAlone(t *testing.T) {
	var wrote bool
	logger := log.New(writerFunc(func(p []byte) (int, error) {
		wrote = true
		return len(p), nil
	}), "", 0)

	ring := logbuf.New(8)
	a := New(Config{Manager: nfc.NewMockManager(), Logs: ring, Logger: logger})

	a.Logger().Printf("a line")

	if !wrote {
		t.Error("the supplied logger did not receive the line")
	}
	if got := len(ring.Entries()); got != 0 {
		t.Errorf("the ring captured %d entries from a supplied logger, want 0", got)
	}
}

// writerFunc adapts a function to io.Writer.
type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

var _ io.Writer = writerFunc(nil)
