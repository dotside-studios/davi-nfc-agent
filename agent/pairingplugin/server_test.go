package pairingplugin

import (
	"net/http"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/agent/serverplugin"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/secure/pairing"
)

// testPairingServer is the component under test: the pairing machinery, which
// belongs to whatever owns the credentials, wrapped in the lifetime that binds
// and closes its listener.
func testPairingServer(port int) *Server {
	return NewServer(pairing.NewServer(pairing.ServerOptions{}), port)
}

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
// agent.Setup used to start the pairing listener itself and nothing but the command's
// signal handler ever closed it, so agent.Agent.Stop left it bound.
func TestPairingFollowsTheAgent(t *testing.T) {
	opts := testOptions(t)
	opts.DevicePort = 9487

	rt, err := agent.Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("agent.Setup: %v", err)
	}
	if err := rt.Agent.Use(testPairingServer(9489)); err != nil {
		t.Fatalf("Use: %v", err)
	}

	if reachable("http://localhost:9489/") {
		t.Error("building the pairing server should not bind it")
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

// agent.Setup builds no pairing server: whether an agent pairs devices, and what
// displays the PIN, is the program's decision.
func TestSetupBuildsNoPairingServer(t *testing.T) {
	opts := testOptions(t)
	opts.DevicePort = 9491
	opts.BootstrapPort = 9472

	rt, err := agent.Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("agent.Setup: %v", err)
	}
	if n := len(rt.Agent.Components()); n != 0 {
		t.Errorf("Components() = %d, want none registered", n)
	}
}

// A build without pairing holds a nil *Server, and every caller asking
// it for the PIN would otherwise have to check first.
func TestANilPairingServerReportsItsAbsence(t *testing.T) {
	var absent *Server

	if got := absent.Port(); got != 0 {
		t.Errorf("Port() = %d, want 0", got)
	}
	if got := absent.PIN(); got != "" {
		t.Errorf("PIN() = %q, want empty", got)
	}
	if got := absent.RotatePIN(); got != "" {
		t.Errorf("RotatePIN() = %q, want empty", got)
	}
	if absent.Server() != nil {
		t.Error("Server() should be nil")
	}
}

// The component reports what it runs, so a tray or a console can label its
// entries before the listener is up.
func TestAPairingServerReportsItsPortAndPIN(t *testing.T) {
	server := testPairingServer(9495)

	if got := server.Port(); got != 9495 {
		t.Errorf("Port() = %d, want 9495", got)
	}
	if got := server.PIN(); got == "" {
		t.Error("PIN() is empty; a phone would have nothing to present")
	}
}

// Listed as an endpoint of the server plugin, the pairing server goes through
// the same registration as anything else the agent runs.
func TestPairingRegistersAsAnEndpointComponent(t *testing.T) {
	opts := testOptions(t)
	opts.DevicePort = 9493

	rt, err := agent.Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("agent.Setup: %v", err)
	}

	servers := &serverplugin.Plugin{Endpoints: []serverplugin.Endpoint{{Name: "pairing", Component: testPairingServer(9497)}}}
	if err := rt.Agent.Plugins.Add(servers); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}
	if err := rt.Agent.Activate(nil); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	if !runs(rt.Agent, "pairing") {
		t.Fatalf("Components() = %v, want the pairing server among them", names(rt.Agent))
	}
}
