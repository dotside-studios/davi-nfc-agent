package agent

import (
	"testing"

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
