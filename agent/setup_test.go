package agent

import (
	"log"
	"path/filepath"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/buildinfo"

	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/settings"
)

// testOptions returns options that keep Setup off the network and out of the
// user's real config directory.
func testOptions(t *testing.T) *Options {
	t.Helper()

	opts := DefaultOptions()
	opts.ConfigDir = t.TempDir()
	opts.AutoTLS = false
	opts.BootstrapPort = 0
	return opts
}

// TestSetupLeavesTheProcessLoggerAlone guards the boundary that lets the agent
// be embedded: redirecting the standard logger is the program's decision, not a
// library's, and a caller with its own logging must not have it taken over.
func TestSetupLeavesTheProcessLoggerAlone(t *testing.T) {
	before := log.Writer()
	t.Cleanup(func() { log.SetOutput(before) })

	if _, err := Setup(testOptions(t), nfc.NewMockManager()); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	if log.Writer() != before {
		t.Error("Setup redirected the process logger; that belongs to the command")
	}
}

// TestSetupCarriesTheSuppliedLogRing checks the other half: the caller installs
// the ring, and Setup hands back the same one for the console to read.
func TestSetupCarriesTheSuppliedLogRing(t *testing.T) {
	opts := testOptions(t)
	ring := logbuf.New(logbuf.DefaultCapacity)
	opts.Logs = ring

	rt, err := Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if rt.Logs != ring {
		t.Error("Runtime.Logs is not the ring passed in through Options")
	}
}

// TestSetupWithoutLogRing confirms the ring is optional -- an embedder that
// wants none says nothing and gets none.
func TestSetupWithoutLogRing(t *testing.T) {
	rt, err := Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if rt.Logs != nil {
		t.Error("Runtime.Logs should be nil when Options.Logs is")
	}
}

// TestAnExplicitPortOutranksStoredPort covers the flag-precedence bit that moved
// into the API when the flags left this package: a port the caller means must
// survive a port persisted in settings.
func TestAnExplicitPortOutranksStoredPort(t *testing.T) {
	dir := t.TempDir()

	// Persist a port, the way the console would.
	seed := testOptions(t)
	seed.ConfigDir = dir
	rt, err := Setup(seed, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if _, err := rt.Settings.Update(func(s *settings.Settings) { s.Port = 9999 }); err != nil {
		t.Fatalf("save settings: %v", err)
	}

	t.Run("unset yields to the stored port", func(t *testing.T) {
		opts := testOptions(t)
		opts.ConfigDir = dir
		opts.DevicePort = DefaultDevicePort
		opts.Explicit.Port = false

		rt, err := Setup(opts, nfc.NewMockManager())
		if err != nil {
			t.Fatalf("Setup: %v", err)
		}
		if rt.Agent.DevicePort() != 9999 {
			t.Errorf("DevicePort = %d, want the stored 9999", rt.Agent.DevicePort())
		}
	})

	t.Run("set outranks the stored port", func(t *testing.T) {
		opts := testOptions(t)
		opts.ConfigDir = dir
		opts.DevicePort = 9123
		opts.Explicit.Port = true

		rt, err := Setup(opts, nfc.NewMockManager())
		if err != nil {
			t.Fatalf("Setup: %v", err)
		}
		if rt.Agent.DevicePort() != 9123 {
			t.Errorf("DevicePort = %d, want the requested 9123", rt.Agent.DevicePort())
		}
	})
}

// TestCustomIdentityReachesTheAgent is the point of Config.Info: a program
// built on the agent carries its own name, and — the part that matters
// operationally — keeps its configuration out of this agent's directory.
func TestCustomIdentityReachesTheAgent(t *testing.T) {
	opts := testOptions(t)
	opts.Info = buildinfo.Info{
		Name:        "turnstile",
		DirName:     "turnstile",
		DisplayName: "Gate Reader",
		Version:     "2.1.0",
	}

	rt, err := Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	got := rt.Agent.Info()
	if got.Name != "turnstile" || got.DisplayName != "Gate Reader" || got.DirName != "turnstile" {
		t.Errorf("Info = %+v, want the supplied identity", got)
	}
	if got.FullVersion() != "2.1.0" {
		t.Errorf("FullVersion = %q, want 2.1.0", got.FullVersion())
	}
	if got.IsDev() {
		t.Error("a versioned build should not report itself as dev")
	}

	// Description was left blank, so it falls back rather than emptying out.
	if got.Description != buildinfo.Default().Description {
		t.Errorf("Description = %q, want the fallback", got.Description)
	}
}

// TestBlankIdentityKeepsTheAgentsOwn guards the shipped binary: options that
// say nothing about identity must behave exactly as before Info existed.
func TestBlankIdentityKeepsTheAgentsOwn(t *testing.T) {
	rt, err := Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if rt.Agent.Info() != buildinfo.Default() {
		t.Errorf("Info = %+v, want buildinfo.Default() %+v", rt.Agent.Info(), buildinfo.Default())
	}
}

// TestDefaultConfigDirUsesTheGivenName checks the directory an unconfigured
// embedder would otherwise share with this agent.
func TestDefaultConfigDirUsesTheGivenName(t *testing.T) {
	mine := DefaultConfigDir("turnstile")
	theirs := DefaultConfigDir(buildinfo.Default().DirName)
	if mine == theirs {
		t.Fatal("a different DirName must give a different config directory")
	}
	if filepath.Base(mine) != "turnstile" {
		t.Errorf("DefaultConfigDir(%q) = %q", "turnstile", mine)
	}
}
