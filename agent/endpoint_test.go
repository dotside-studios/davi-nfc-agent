package agent

import (
	"net/http"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// The agent decides what a device is told and permitted; the caller only builds
// the handler. These used to be passed inside the agent, so the move could have
// dropped them silently.
func TestDeviceEndpointOptionsCarryTheAgentsPolicies(t *testing.T) {
	opts := testOptions(t)

	var got DeviceEndpointOptions
	opts.DeviceEndpoint = func(o DeviceEndpointOptions) http.Handler {
		got = o
		return http.NotFoundHandler()
	}

	if _, err := Setup(opts, nfc.NewMockManager()); err != nil {
		t.Fatalf("Setup: %v", err)
	}

	if got.Authenticate == nil {
		t.Error("no credential check reached the endpoint: it would serve anyone")
	}
	if got.CheckOrigin == nil {
		t.Error("no origin check reached the endpoint")
	}
	if got.AllowTagModification == nil {
		t.Error("no mode gate reached the endpoint: read-only would not reach a device")
	}
	if got.PublicKeyPin == nil {
		t.Fatal("no key pin reached the endpoint: a device could not recognise this agent later")
	}
}

// The pin is asked for when a device registers rather than captured when the
// endpoint is built, so certificate material need not be settled by then.
func TestDeviceEndpointReadsTheKeyPinWhenAsked(t *testing.T) {
	opts := testOptions(t)

	var got DeviceEndpointOptions
	opts.DeviceEndpoint = func(o DeviceEndpointOptions) http.Handler {
		got = o
		return http.NotFoundHandler()
	}

	rt, err := Setup(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	if want := rt.Agent.PublicKeyPin(); got.PublicKeyPin() != want {
		t.Errorf("PublicKeyPin() = %q, want the agent's %q", got.PublicKeyPin(), want)
	}
}

// The mode gate is read per operation, so a mode change while running takes
// effect rather than being fixed when the endpoint was built.
func TestTagModificationFollowsTheReaderMode(t *testing.T) {
	rt, err := Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	a := rt.Agent

	if !a.TagModificationAllowed() {
		t.Error("an agent with no reader open should permit modification")
	}

	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	a.Supervisor().SetMode(nfc.ModeReadOnly)
	if a.TagModificationAllowed() {
		t.Error("read-only mode did not reach the gate")
	}

	a.Supervisor().SetMode(nfc.ModeReadWrite)
	if !a.TagModificationAllowed() {
		t.Error("returning to read/write did not reach the gate")
	}
}
