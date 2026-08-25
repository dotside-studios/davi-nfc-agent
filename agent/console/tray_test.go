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
