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
			rt.Agent.OnPreferencesChange(func() { announced++ })

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

// The two hooks report different things and are registered separately, so
// something following one is not woken by the other. The console takes both,
// since either changes what an open page shows.
func TestAPreferenceChangeDoesNotLookLikeAClientChange(t *testing.T) {
	rt, err := Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	clients, preferences := 0, 0
	rt.Agent.OnClientsChange(func() { clients++ })
	rt.Agent.OnPreferencesChange(func() { preferences++ })

	rt.Agent.SetReaderMode(nfc.ModeReadOnly)

	if preferences != 1 {
		t.Errorf("preference hook ran %d times, want 1", preferences)
	}
	if clients != 0 {
		t.Errorf("the client hook ran %d times for a preference change, want 0", clients)
	}
}

// The client hooks are read when they fire, not captured when the client server
// is built, so a hook registered after the agent starts is not left out. They
// were once snapshotted, which would have left a late subscriber seeing
// preference changes but no client ones.
func TestClientHooksAreReadWhenTheyFire(t *testing.T) {
	rt, err := Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// What the client server is handed, taken before any hook exists.
	notify := rt.Agent.fireClientsChanged

	ran := 0
	rt.Agent.OnClientsChange(func() { ran++ })

	notify()
	if ran != 1 {
		t.Errorf("a hook registered after the callback was taken ran %d times, want 1", ran)
	}
}
