package main

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/settings"
)

// TestStoredSettingCannotWithdrawCommandLineRequirement is the console's path:
// saving any preference re-applies the stored settings, which must not lower a
// requirement the command line set. Startup has the same shape, because
// ApplySettings is the last thing it does.
func TestStoredSettingCannotWithdrawCommandLineRequirement(t *testing.T) {
	agent := NewAgent(nfc.NewMockManager())
	agent.RequirePairedDevice = true
	agent.RequirePairedDeviceLocked = true

	agent.ApplySettings(settings.Settings{RequirePairedDevice: false})

	if !agent.RequiresPairedDevice() {
		t.Error("applying a stored setting withdrew a command-line requirement")
	}

	// And the console is told the truth about it, rather than the file's version.
	if !agent.Settings().RequirePairedDevice {
		t.Error("the agent reports a requirement it is enforcing as withdrawn")
	}
}

// TestCommandLineRequirementSurvivesDirectToggle covers the systray switch,
// which reaches SetRequirePairedDevice without going through ApplySettings.
func TestCommandLineRequirementSurvivesDirectToggle(t *testing.T) {
	agent := NewAgent(nfc.NewMockManager())
	agent.RequirePairedDevice = true
	agent.RequirePairedDeviceLocked = true

	agent.SetRequirePairedDevice(false)

	if !agent.RequiresPairedDevice() {
		t.Error("SetRequirePairedDevice(false) withdrew a command-line requirement")
	}
}

// TestStoredRequirementStaysToggleable is the other side of the guard: a
// requirement that came only from settings is the operator's to turn off,
// which is what the console's switch is for.
func TestStoredRequirementStaysToggleable(t *testing.T) {
	agent := NewAgent(nfc.NewMockManager())
	agent.RequirePairedDevice = true
	agent.RequirePairedDeviceLocked = false

	agent.SetRequirePairedDevice(false)

	if agent.RequiresPairedDevice() {
		t.Error("a requirement that came only from settings should still be toggleable")
	}
}

// TestEitherSourceCanRaiseTheRequirement covers the whole matrix. Either source
// may turn the requirement on; the case that regressed is the command line
// asking for it and a stored preference withdrawing it, which is the one
// direction that costs security rather than convenience.
func TestEitherSourceCanRaiseTheRequirement(t *testing.T) {
	for _, tc := range []struct {
		name       string
		asked      bool
		stored     bool
		want       bool
		wantLocked bool
	}{
		{"neither", false, false, false, false},
		{"stored only", false, true, true, false},
		{"command line only", true, false, true, true},
		{"both", true, true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require, locked := resolveRequirePaired(tc.asked, tc.stored)
			if require != tc.want {
				t.Errorf("require = %v, want %v", require, tc.want)
			}
			if locked != tc.wantLocked {
				t.Errorf("locked = %v, want %v", locked, tc.wantLocked)
			}
		})
	}
}
