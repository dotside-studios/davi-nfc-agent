//go:build !nowebui

package console

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// fakeTray records what the console asked the tray to do.
type fakeTray struct {
	stopped  int
	quit     int
	switched []string
	synced   []agent.Preferences
}

func (f *fakeTray) StopAgent()                     { f.stopped++ }
func (f *fakeTray) Quit()                          { f.quit++ }
func (f *fakeTray) SwitchDevice(devicePath string) { f.switched = append(f.switched, devicePath) }
func (f *fakeTray) SyncPreferencesToMenu(next agent.Preferences) {
	f.synced = append(f.synced, next)
}

// An action taken in the console has to move the tray's menu, or the two
// surfaces disagree about what the agent is doing. Without a tray the console
// drives the agent directly, as a headless run wants.
func TestTheConsoleActsThroughTheTrayWhenItHasOne(t *testing.T) {
	a := quietAgent(t)
	c := New(Config{Agent: a})

	tray := &fakeTray{}
	c.AttachTray(tray)

	c.host.StopAgent()
	if tray.stopped != 1 {
		t.Errorf("tray saw %d stops, want 1", tray.stopped)
	}

	c.host.QuitAgent()
	if tray.quit != 1 {
		t.Errorf("tray saw %d quits, want 1", tray.quit)
	}

	if err := c.host.SelectDevice("ACS ACR122U 00"); err != nil {
		t.Fatalf("SelectDevice: %v", err)
	}
	if len(tray.switched) != 1 || tray.switched[0] != "ACS ACR122U 00" {
		t.Errorf("tray switched to %v, want one switch to the named reader", tray.switched)
	}

	c.host.ApplyPreferences(func(p *agent.Preferences) { p.Mode = nfc.ModeReadOnly })
	if len(tray.synced) == 0 {
		t.Fatal("a preference change did not reach the tray's menu")
	}
	if got := tray.synced[len(tray.synced)-1].Mode; got != nfc.ModeReadOnly {
		t.Errorf("tray synced mode = %v, want %v", got, nfc.ModeReadOnly)
	}
}

// With no tray attached, selecting a reader is refused rather than silently
// doing nothing: the console cannot move a selection it does not own.
func TestSelectingAReaderNeedsATray(t *testing.T) {
	c := New(Config{Agent: quietAgent(t)})
	if err := c.host.SelectDevice("ACS ACR122U 00"); err == nil {
		t.Error("SelectDevice succeeded with no tray attached")
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
	c.AttachTray(nil)

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
