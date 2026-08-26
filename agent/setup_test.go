package agent

import (
	"log"
	"path/filepath"
	"slices"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/buildinfo"

	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
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

// TestSetupWithoutLogRing confirms the ring is optional: an embedder that
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

// TestCustomIdentityReachesTheAgent covers Config.Info: a program built on the
// agent carries its own name and keeps its configuration out of this agent's
// directory.
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

// Which certificate a listener should serve: the one Options named, else the
// one Setup manages, else none. This is where -cert and -key land, so a
// certificate named on the command line has to win over the managed one.
func TestSetupResolvesTheCertificateToServe(t *testing.T) {
	managedOpts := testOptions(t)
	managedOpts.AutoTLS = true

	managed, err := Setup(managedOpts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if managed.Certificates == nil {
		t.Fatal("Setup managed no certificate, so there is nothing to fall back to")
	}
	if managed.CertFile != managed.Certificates.GetCertFile() {
		t.Errorf("CertFile = %q, want the managed one (%q)", managed.CertFile, managed.Certificates.GetCertFile())
	}

	namedOpts := testOptions(t)
	namedOpts.CertFile, namedOpts.KeyFile = "/tmp/named.pem", "/tmp/named.key"

	named, err := Setup(namedOpts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if named.CertFile != "/tmp/named.pem" || named.KeyFile != "/tmp/named.key" {
		t.Errorf("CertFile/KeyFile = %q/%q, want the pair Options named", named.CertFile, named.KeyFile)
	}

	noneOpts := testOptions(t)
	noneOpts.AutoTLS = false

	none, err := Setup(noneOpts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if none.CertFile != "" || none.KeyFile != "" {
		t.Errorf("CertFile/KeyFile = %q/%q, want empty", none.CertFile, none.KeyFile)
	}
}

// Setup hands the agent what Options named. Each of these is copied field by
// field into the Config that New takes, so a field added to both structs and
// forgotten in that copy leaves the agent on its zero value, silently.
func TestSetupPassesOptionsThroughToTheAgent(t *testing.T) {
	opts := testOptions(t)
	opts.Mode = nfc.ModeWriteOnly
	opts.CardTypes = []string{"NTAG215"}
	opts.DevicePath = "ACS ACR122U 07"
	opts.DevicePort = 9487
	opts.ReaderFeedback = true
	opts.RequirePairedDevice = true
	opts.AllowedOrigins = "console.example, shop.example"
	opts.Logs = logbuf.New(16)

	rt, err := Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	a := rt.Agent

	if got := a.CurrentReaderMode(); got != opts.Mode {
		t.Errorf("mode = %v, want %v", got, opts.Mode)
	}
	if got := a.CardTypeFilter(); len(got) != 1 || got[0] != "NTAG215" {
		t.Errorf("card types = %v, want [NTAG215]", got)
	}
	if got := a.CurrentPinnedDevice(); got != opts.DevicePath {
		t.Errorf("pinned device = %q, want %q", got, opts.DevicePath)
	}
	if got := rt.DevicePath; got != opts.DevicePath {
		t.Errorf("Runtime.DevicePath = %q, want %q", got, opts.DevicePath)
	}
	if got := a.DevicePort(); got != opts.DevicePort {
		t.Errorf("port = %d, want %d", got, opts.DevicePort)
	}
	if got := a.ReaderFeedback(); got != opts.ReaderFeedback {
		t.Errorf("reader feedback = %v, want %v", got, opts.ReaderFeedback)
	}
	if got := a.RequirePairedDevice(); got != opts.RequirePairedDevice {
		t.Errorf("require paired = %v, want %v", got, opts.RequirePairedDevice)
	}
	if a.Logs() != opts.Logs {
		t.Error("the log ring did not reach the agent")
	}
	if got := a.ConfigDir(); got != opts.ConfigDir {
		t.Errorf("config dir = %q, want %q", got, opts.ConfigDir)
	}

	// The allowlist is the server plugin's, so Setup parses what the flags
	// named and leaves it on the Runtime for the plugin to serve behind.
	want := []string{"console.example", "shop.example"}
	if got := rt.AllowedOrigins; !slices.Equal(got, want) {
		t.Errorf("Runtime.AllowedOrigins = %v, want %v", got, want)
	}
}
