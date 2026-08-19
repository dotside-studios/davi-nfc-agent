package agent

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/settings"
)

// storedRequirePaired writes a settings file in dir with the given value, the
// way a previous run or the console would have left one.
func storedRequirePaired(t *testing.T, dir string, on bool) {
	t.Helper()

	opts := testOptions(t)
	opts.ConfigDir = dir
	rt, err := Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("seed Setup: %v", err)
	}
	if _, err := rt.Settings.Update(func(s *settings.Settings) { s.RequirePairedDevice = on }); err != nil {
		t.Fatalf("seed settings: %v", err)
	}
}

// TestPairingRequirementPrecedence covers the whole matrix. Either source may
// turn the requirement on; the case that regressed is the command line asking
// for it and a stored preference withdrawing it, which is the one direction
// that costs security rather than convenience.
func TestPairingRequirementPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		flag   bool
		stored bool
		want   bool
	}{
		{"neither", false, false, false},
		{"stored only", false, true, true},
		{"command line only", true, false, true},
		{"both", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			storedRequirePaired(t, dir, tc.stored)

			opts := testOptions(t)
			opts.ConfigDir = dir
			opts.RequirePaired = tc.flag

			rt, err := Setup(opts, nfc.NewMockManager())
			if err != nil {
				t.Fatalf("Setup: %v", err)
			}
			if got := rt.Agent.RequirePairedDevice(); got != tc.want {
				t.Errorf("RequirePairedDevice() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestPairingRequirementFromEnvironment checks the other way of asking, which
// is what an unattended install uses.
func TestPairingRequirementFromEnvironment(t *testing.T) {
	dir := t.TempDir()
	storedRequirePaired(t, dir, false)
	t.Setenv("DAVI_NFC_REQUIRE_PAIRED_DEVICES", "1")

	opts := testOptions(t)
	opts.ConfigDir = dir

	rt, err := Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if !rt.Agent.RequirePairedDevice() {
		t.Error("the environment asked for paired devices and a stored setting withdrew it")
	}
	if !rt.Agent.RequirePairedDeviceLocked() {
		t.Error("a requirement from the environment should be locked")
	}
}

// TestCommandLineRequirementCannotBeWithdrawn is the console's path: saving any
// setting re-applies the stored ones, which must not lower a requirement the
// command line set.
func TestCommandLineRequirementCannotBeWithdrawn(t *testing.T) {
	dir := t.TempDir()
	storedRequirePaired(t, dir, false)

	opts := testOptions(t)
	opts.ConfigDir = dir
	opts.RequirePaired = true

	rt, err := Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// What SaveSettings does after the console writes a preference.
	rt.Agent.ApplySettings(settings.Settings{RequirePairedDevice: false})
	if !rt.Agent.RequirePairedDevice() {
		t.Error("applying stored settings withdrew a command-line requirement")
	}

	// And the direct toggle.
	rt.Agent.SetRequirePairedDevice(false)
	if !rt.Agent.RequirePairedDevice() {
		t.Error("SetRequirePairedDevice(false) withdrew a command-line requirement")
	}
}

// TestStoredRequirementStaysToggleable guards the other side: a requirement
// that only came from settings is the operator's to turn off in the console.
func TestStoredRequirementStaysToggleable(t *testing.T) {
	dir := t.TempDir()
	storedRequirePaired(t, dir, true)

	opts := testOptions(t)
	opts.ConfigDir = dir

	rt, err := Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if !rt.Agent.RequirePairedDevice() {
		t.Fatal("stored setting should have turned the requirement on")
	}
	if rt.Agent.RequirePairedDeviceLocked() {
		t.Error("a requirement from settings alone must not be locked")
	}

	rt.Agent.SetRequirePairedDevice(false)
	if rt.Agent.RequirePairedDevice() {
		t.Error("a requirement that came only from settings should be toggleable off")
	}
}
