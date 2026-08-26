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

// logging is a plugin that writes one line on the channel it was given.
type logging struct {
	name string
	line string
}

func (p *logging) Name() string { return p.name }

func (p *logging) Activate(ctx AgentContext) error {
	ctx.Logger().Print(p.line)
	return nil
}

// A plugin logs under its own name, which is what Name is for: the console
// shows the source beside every line, and every plugin used to log as the
// agent.
func TestAPluginLogsUnderItsOwnName(t *testing.T) {
	a, ring := ringAgent(t,
		&logging{name: "backups", line: "a line from the backups plugin"},
		&logging{name: "metrics", line: "a line from the metrics plugin"},
	)
	if err := a.Activate(nil); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	if got := find(t, ring, "from the backups plugin").Source; got != "[backups]" {
		t.Errorf("the backups plugin logged as %q, want its own name", got)
	}
	if got := find(t, ring, "from the metrics plugin").Source; got != "[metrics]" {
		t.Errorf("the metrics plugin logged as %q, want its own name", got)
	}

	// The agent's own lines keep the agent's name, so a plugin's channel is
	// told apart from the lifecycle around it.
	if got := find(t, ring, "Plugin activated: backups").Source; got != "[agent]" {
		t.Errorf("the agent logged as %q, want the agent", got)
	}
}

// A plugin that names itself nothing still has a channel, under whatever
// PluginName calls it, rather than logging as "[]".
func TestAPluginThatNamesItselfNothingIsChannelledByType(t *testing.T) {
	p := &logging{name: "", line: "a line from an unnamed plugin"}
	a, ring := ringAgent(t, p)
	if err := a.Activate(nil); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	want := "[" + PluginName(p) + "]"
	if got := find(t, ring, "from an unnamed plugin").Source; got != want {
		t.Errorf("a plugin naming itself nothing logged as %q, want %q", got, want)
	}
}

// The channel is the agent's sink, so a caller that supplied its own logger
// still receives what the plugins write.
func TestAPluginChannelWritesToTheSuppliedLogger(t *testing.T) {
	var lines []string
	logger := log.New(writerFunc(func(p []byte) (int, error) {
		lines = append(lines, string(p))
		return len(p), nil
	}), "", 0)

	a := New(Config{
		Manager: nfc.NewMockManager(),
		Logger:  logger,
		Plugins: []Plugin{&logging{name: "backups", line: "a line from the backups plugin"}},
	})
	if err := a.Activate(nil); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	var found bool
	for _, line := range lines {
		if strings.Contains(line, "[backups] ") && strings.Contains(line, "from the backups plugin") {
			found = true
		}
	}
	if !found {
		t.Errorf("the supplied logger received %v, want the plugin's line under its name", lines)
	}
}

// writerFunc adapts a function to io.Writer.
type writerFunc func(p []byte) (int, error)

func (f writerFunc) Write(p []byte) (int, error) { return f(p) }

var _ io.Writer = writerFunc(nil)
