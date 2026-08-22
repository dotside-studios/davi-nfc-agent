package main

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/settings"
)

// The paired-device requirement is where a launcher's choice costs security to
// get wrong, and it is governed by the same rule as every other setting: what
// the launcher set, the run keeps.

// The console's path: saving any preference re-applies the stored settings,
// which must not lower a requirement the launcher set.
func TestStoredSettingCannotWithdrawALaunchedRequirement(t *testing.T) {
	agent := NewAgent(nfc.NewMockManager())
	agent.RequirePairedDevice = true
	agent.SetExplicit(settings.Explicit{RequirePairedDevice: true})

	agent.ApplySettings(settings.Settings{RequirePairedDevice: false})

	if !agent.RequiresPairedDevice() {
		t.Error("applying a stored setting withdrew a launched requirement")
	}

	// And the console is told the truth about it, rather than the file's version.
	if !agent.Settings().RequirePairedDevice {
		t.Error("the agent reports a requirement it is enforcing as withdrawn")
	}
	if !agent.Explicit().RequirePairedDevice {
		t.Error("the agent does not report the requirement as the launcher's")
	}
}

// The tray's switch, which reaches SetRequirePairedDevice directly.
func TestLaunchedRequirementSurvivesDirectToggle(t *testing.T) {
	agent := NewAgent(nfc.NewMockManager())
	agent.RequirePairedDevice = true
	agent.SetExplicit(settings.Explicit{RequirePairedDevice: true})

	agent.SetRequirePairedDevice(false)

	if !agent.RequiresPairedDevice() {
		t.Error("SetRequirePairedDevice(false) withdrew a launched requirement")
	}
}

// The other side of the guard: a requirement that came from the file is the
// operator's to turn off, which is what the switches are for.
func TestStoredRequirementStaysToggleable(t *testing.T) {
	agent := NewAgent(nfc.NewMockManager())
	agent.ApplySettings(settings.Settings{RequirePairedDevice: true})

	if !agent.RequiresPairedDevice() {
		t.Fatal("a stored requirement was not applied")
	}

	agent.SetRequirePairedDevice(false)

	if agent.RequiresPairedDevice() {
		t.Error("a requirement that came only from settings should still be toggleable")
	}
}

// The whole matrix. Launching without the flag leaves the file in charge, which
// is how a requirement stored by a previous run comes back; launching with it
// settles the question either way, including the case the old resolution could
// not express, -require-paired-devices=false against a stored true.
func TestWhoDecidesTheRequirement(t *testing.T) {
	for _, tc := range []struct {
		name     string
		explicit bool
		launched bool
		stored   bool
		want     bool
	}{
		{"neither", false, false, false, false},
		{"stored only", false, false, true, true},
		{"launched on", true, true, false, true},
		{"launched on over a stored no", true, true, false, true},
		{"launched off over a stored yes", true, false, true, false},
		{"both on", true, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent := NewAgent(nfc.NewMockManager())
			agent.RequirePairedDevice = tc.launched
			agent.SetExplicit(settings.Explicit{RequirePairedDevice: tc.explicit})

			agent.ApplySettings(settings.Settings{RequirePairedDevice: tc.stored})

			if got := agent.RequiresPairedDevice(); got != tc.want {
				t.Errorf("requirement = %v, want %v", got, tc.want)
			}
		})
	}
}
