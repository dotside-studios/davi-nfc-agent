package agent

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// What a device endpoint is built from is the agent's to answer, whoever
// mounts it: the credentials that admit a device, what the mode allows, and
// the pin a device recognises this agent by later.
//
// The check built from them is [ServerPlugin.Authenticate].
func TestTheAgentAnswersWhatADeviceEndpointNeeds(t *testing.T) {
	rt, err := Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	a := rt.Agent

	if a.APISecret() == "" {
		t.Error("no shared secret: a device endpoint built from this would admit anyone")
	}
	if a.TokenVerifier() == nil {
		t.Error("no token verifier: a paired device could not be recognised")
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
