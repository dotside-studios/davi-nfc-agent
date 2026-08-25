//go:build !nowebui

package console

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// Quitting is the program's to do, so the console asks whoever owns it. A build
// that supplied no way out stops the agent instead of leaving the page's quit
// control doing nothing.
func TestTheConsoleQuitsThroughTheProgram(t *testing.T) {
	quits := 0
	c := New(Config{Agent: quietAgent(t), Quit: func() { quits++ }})

	c.host.QuitAgent()

	if quits != 1 {
		t.Errorf("the program was asked to quit %d times, want 1", quits)
	}
}

// Selecting a reader restarts the agent on it. It used to be refused without a
// tray attached, which left a headless console unable to pick one.
func TestSelectingAReaderRestartsTheAgentOnIt(t *testing.T) {
	a := quietAgent(t)
	c := New(Config{Agent: a})

	if err := c.host.SelectDevice("ACS ACR122U 00"); err != nil {
		t.Fatalf("SelectDevice: %v", err)
	}
	t.Cleanup(a.Stop)

	if got := a.CurrentDevicePath(); got != "ACS ACR122U 00" {
		t.Errorf("the agent is on %q, want the reader that was chosen", got)
	}
}

// An open page follows the agent, not just its own actions: a preference
// changed from the tray, or by anything else holding the agent, has to wake the
// live connection or the page keeps rendering what it loaded.
func TestAnOpenPageIsWokenByAPreferenceChangedElsewhere(t *testing.T) {
	a := quietAgent(t)
	c := New(Config{Agent: a})

	woken, done := c.subscribe()
	t.Cleanup(done)

	a.SetReaderMode(nfc.ModeReadOnly)

	select {
	case <-woken:
	default:
		t.Fatal("a preference change did not reach the open page")
	}
}

// Every open page is woken, and one that closes stops being woken without
// taking the others with it.
func TestEveryOpenPageIsWoken(t *testing.T) {
	c := New(Config{Agent: quietAgent(t)})

	first, closeFirst := c.subscribe()
	second, closeSecond := c.subscribe()
	t.Cleanup(closeSecond)

	c.NotifyChange()
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("pages woken %d and %d times, want both", len(first), len(second))
	}
	<-first
	<-second

	closeFirst()
	closeFirst() // closing twice is what a deferred close after an early return does

	c.NotifyChange()
	if len(first) != 0 {
		t.Error("a closed page was still woken")
	}
	if len(second) != 1 {
		t.Error("closing one page stopped the others being woken")
	}
}

// A build with no console holds a nil *Server, which console_nowebui.go
// promises every method tolerates. The stubs there do; these are the ones the
// real build has to match, so the promise holds under either tag.
func TestANilConsoleToleratesEveryCall(t *testing.T) {
	var c *Server

	c.NotifyChange()

	if got := c.Endpoints(); got != nil {
		t.Errorf("Endpoints() = %v, want nil", got)
	}
	if got := c.Routes(); got != nil {
		t.Errorf("Routes() = %v, want nil", got)
	}
	if got := c.Assets(); got != nil {
		t.Errorf("Assets() = %v, want nil", got)
	}
	if _, err := c.ConsoleURL(); err == nil {
		t.Error("ConsoleURL() on a nil console returned no error")
	}
}
