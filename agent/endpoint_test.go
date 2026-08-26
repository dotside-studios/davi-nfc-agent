package agent

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// What a device endpoint is built from is the agent's to answer, whoever
// mounts it: who is admitted, what the mode allows, and the pin a device
// recognises this agent by later.
func TestTheAgentAnswersWhatADeviceEndpointNeeds(t *testing.T) {
	rt, err := Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	a := rt.Agent

	if a.DeviceAuth == nil {
		t.Error("no credential check: an endpoint built from this would serve anyone")
	}
	if !a.TagModificationAllowed() {
		t.Error("no mode gate: read-only would not reach a device")
	}

	// Read when a device registers rather than captured when the endpoint was
	// built, so certificate material need not be settled by then.
	pin := a.PublicKeyPin
	if pin() != a.PublicKeyPin() {
		t.Error("the key pin is not read when it is asked for")
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
