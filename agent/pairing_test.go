package agent

import (
	"net/http"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

func reachable(url string) bool {
	c := &http.Client{Timeout: 2 * time.Second}
	resp, err := c.Get(url)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return true
}

// TestPairingFollowsTheAgent is the ownership split this component closes.
// Setup used to start the pairing listener itself and nothing but the command's
// signal handler ever closed it, so Agent.Stop left it bound.
func TestPairingFollowsTheAgent(t *testing.T) {
	opts := testOptions(t)
	opts.DevicePort = 9487
	opts.Explicit.Port = true
	opts.BootstrapPort = 9489

	rt, err := Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	if reachable("http://localhost:9489/") {
		t.Error("Setup should build the pairing server, not bind it")
	}

	if err := rt.Agent.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for !reachable("http://localhost:9489/") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if !reachable("http://localhost:9489/") {
		t.Fatal("Start should bring the pairing server up")
	}

	rt.Agent.Stop()
	for reachable("http://localhost:9489/") && time.Now().Before(deadline.Add(2*time.Second)) {
		time.Sleep(20 * time.Millisecond)
	}
	if reachable("http://localhost:9489/") {
		t.Error("Stop left the pairing server listening; that was the whole bug")
	}
}

// TestPairingDisabled keeps the zero-port path working: no component, no
// listener, and the accessors report the absence rather than panicking.
func TestPairingDisabled(t *testing.T) {
	opts := testOptions(t)
	opts.DevicePort = 9491
	opts.Explicit.Port = true
	opts.BootstrapPort = 0

	rt, err := Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if rt.Agent.Pairing() != nil {
		t.Error("Pairing() should be nil when the port is 0")
	}
	if rt.Agent.Bootstrap() != nil {
		t.Error("Bootstrap() should be nil when pairing is disabled")
	}
	if got := rt.Agent.BootstrapPort(); got != 0 {
		t.Errorf("BootstrapPort() = %d, want 0", got)
	}
	if n := len(rt.Agent.Components()); n != 0 {
		t.Errorf("Components() = %d, want none registered", n)
	}
}

// TestPairingRegisteredAsComponent checks it goes through the same path as
// anything else the agent runs. It is an endpoint of the server plugin, so it
// is registered when the plugins are activated rather than when Setup returns.
func TestPairingRegisteredAsComponent(t *testing.T) {
	opts := testOptions(t)
	opts.DevicePort = 9493
	opts.Explicit.Port = true
	opts.BootstrapPort = 9495

	rt, err := Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if n := len(rt.Agent.Components()); n != 0 {
		t.Errorf("Components() = %d before activation, want none", n)
	}

	if err := rt.Agent.Activate(nil); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	comps := rt.Agent.Components()
	if len(comps) != 1 || comps[0].Name() != "pairing" {
		t.Fatalf("Components() = %v, want one named pairing", comps)
	}
	if rt.Agent.BootstrapPort() != 9495 {
		t.Errorf("BootstrapPort() = %d, want 9495", rt.Agent.BootstrapPort())
	}
}
