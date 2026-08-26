package agent

import (
	"slices"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// Every preference an operator can change has to reach whatever is displaying
// it. The console redraws open pages from this hook, so a setter that changes
// the agent without raising it leaves a second page showing the old value until
// something unrelated happens to redraw it.
func TestEveryPreferenceChangeIsAnnounced(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Agent)
	}{
		{"reader mode", func(a *Agent) { a.SetReaderMode(nfc.ModeReadOnly) }},
		{"card type filter", func(a *Agent) { a.SetCardTypeFilter([]string{"NTAG215"}) }},
		{"pinned device", func(a *Agent) { a.SetPinnedDevice("ACS ACR122U 01") }},
		{"device port", func(a *Agent) { a.SetDevicePort(9481) }},
		{"require paired", func(a *Agent) { a.SetRequirePairedDevice(true) }},
		{"reader feedback", func(a *Agent) { a.SetReaderFeedback(true) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rt, err := Setup(testOptions(t), nfc.NewMockManager())
			if err != nil {
				t.Fatalf("Setup: %v", err)
			}

			announced := 0
			rt.Agent.Events().Preferences.Connect(func(Preferences) { announced++ })
			announced = 0 // Connecting to a Property replays the current value.

			tc.change(rt.Agent)
			if announced == 0 {
				t.Error("the change was not announced, so an open console page keeps the old value")
			}

			// A setter that raises the hook for a value that did not move
			// redraws every page for nothing.
			was := announced
			tc.change(rt.Agent)
			if announced != was {
				t.Errorf("announced %d times for a repeat of the same value, want %d", announced-was, 0)
			}
		})
	}
}

// A console saving the settings form changes several preferences at once. Doing
// that one setter at a time announced each field separately, so a subscriber
// saw combinations nobody asked for: the new mode beside the old card types.
func TestApplyPreferencesAnnouncesOnceWithEveryFieldInPlace(t *testing.T) {
	rt, err := Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	var seen []Preferences
	rt.Agent.Events().Preferences.Connect(func(p Preferences) { seen = append(seen, p) })
	seen = nil // Connecting to a Property replays the current value.

	want := rt.Agent.ApplyPreferences(func(p *Preferences) {
		p.Mode = nfc.ModeReadOnly
		p.CardTypes = []string{"NTAG215", "MIFARE Classic"}
		p.DevicePath = "ACS ACR122U 01"
		p.Port = 9481
		p.RequirePairedDevice = true
		p.ReaderFeedback = true
	})

	if len(seen) != 1 {
		t.Fatalf("announced %d times, want 1", len(seen))
	}
	if !samePreferences(seen[0], want) {
		t.Errorf("announced %+v, want %+v", seen[0], want)
	}
	if got := rt.Agent.Preferences(); !samePreferences(got, want) {
		t.Errorf("agent holds %+v, want %+v", got, want)
	}
}

// An apply that changes nothing redraws every open page for nothing.
func TestApplyPreferencesIsSilentWithoutAChange(t *testing.T) {
	rt, err := Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	rt.Agent.SetReaderMode(nfc.ModeReadOnly)

	announced := 0
	rt.Agent.Events().Preferences.Connect(func(Preferences) { announced++ })
	announced = 0 // Connecting to a Property replays the current value.

	rt.Agent.ApplyPreferences(func(p *Preferences) { p.Mode = nfc.ModeReadOnly })
	rt.Agent.ApplyPreferences(nil)
	rt.Agent.ApplyPreferences(func(*Preferences) {})

	if announced != 0 {
		t.Errorf("announced %d times for no change, want 0", announced)
	}
}

// What ApplyPreferences answers with is what the agent holds, not what was
// asked for: a port of zero keeps the current one, and the card types come back
// normalized. A console that trusted the request would show a port nothing
// serves on.
func TestApplyPreferencesAnswersWithWhatTheAgentHolds(t *testing.T) {
	rt, err := Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	port := rt.Agent.DevicePort()

	got := rt.Agent.ApplyPreferences(func(p *Preferences) {
		p.Port = 0
		p.CardTypes = []string{"NTAG215", "", "NTAG215", "MIFARE Classic"}
	})

	if got.Port != port {
		t.Errorf("port %d, want the current %d", got.Port, port)
	}
	want := []string{"MIFARE Classic", "NTAG215"}
	if !slices.Equal(got.CardTypes, want) {
		t.Errorf("card types %v, want %v", got.CardTypes, want)
	}
	if !slices.Equal(rt.Agent.CardTypeFilter(), want) {
		t.Errorf("the filter holds %v, want %v", rt.Agent.CardTypeFilter(), want)
	}
}

// The single-field setters are wrappers over ApplyPreferences, so a mutate that
// reads the agent back must not deadlock against the settings lock.
func TestApplyPreferencesMutateMayReadTheAgent(t *testing.T) {
	rt, err := Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		rt.Agent.ApplyPreferences(func(p *Preferences) {
			p.Port = rt.Agent.DevicePort() + 1
		})
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("ApplyPreferences held the settings lock across mutate")
	}
}
