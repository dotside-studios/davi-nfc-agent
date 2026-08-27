package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// The client server asks the agent for the tag a request names, so the agent
// has to answer for one while it serves and to refuse rather than panic when it
// does not: a client can ask before Start and after Stop.
func TestTheAgentAnswersForTagsOnlyWhileServing(t *testing.T) {
	m := nfc.NewMockManager()
	tag := nfc.NewMockTag("04A1B2C3")
	if err := tag.Connect(); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	m.MockDevice.SetTags([]nfc.Tag{tag})

	rt, err := Setup(testOptions(t), m)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	a := rt.Agent

	assertNotServing(t, a)

	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}

	device, uid := awaitTagOnTheAgent(t, a)
	if device != "mock:usb:001" || uid != "04A1B2C3" {
		t.Errorf("TagOn = %q, %q; want the reader and the tag it scanned", device, uid)
	}
	if got := a.DevicesHoldingTags(); len(got) != 1 || got[0] != "mock:usb:001" {
		t.Errorf("DevicesHoldingTags = %v, want the reader holding the tag", got)
	}
	if _, err := a.TagCapabilities(context.Background(), "mock:usb:001", "04A1B2C3"); err != nil {
		t.Errorf("TagCapabilities while serving: %v", err)
	}

	a.Stop()
	assertNotServing(t, a)
}

// assertNotServing checks that every question about a tag is answered rather
// than reaching for a supervisor that is not there.
func assertNotServing(t *testing.T, a *Agent) {
	t.Helper()

	if device, uid, ok := a.TagOn(""); ok {
		t.Errorf("TagOn = %q, %q, %v; want nothing held while the agent is not serving", device, uid, ok)
	}
	if got := a.DevicesHoldingTags(); len(got) != 0 {
		t.Errorf("DevicesHoldingTags = %v, want none while the agent is not serving", got)
	}

	if _, err := a.WriteTag(context.Background(), "mock:usb:001", "04A1B2C3", nfc.NewNDEFMessage(), false, "key-1"); !errors.Is(err, errNotServing) {
		t.Errorf("WriteTag err = %v, want %v", err, errNotServing)
	}
	if _, err := a.LockTag(context.Background(), "mock:usb:001", "04A1B2C3", "key-1"); !errors.Is(err, errNotServing) {
		t.Errorf("LockTag err = %v, want %v", err, errNotServing)
	}
	if _, err := a.TransceiveTag(context.Background(), "mock:usb:001", "04A1B2C3", []byte{0x30, 0x00}, true); !errors.Is(err, errNotServing) {
		t.Errorf("TransceiveTag err = %v, want %v", err, errNotServing)
	}
	if _, err := a.TagCapabilities(context.Background(), "mock:usb:001", "04A1B2C3"); !errors.Is(err, errNotServing) {
		t.Errorf("TagCapabilities err = %v, want %v", err, errNotServing)
	}
}

// awaitTagOnTheAgent waits for the reader to report the tag on it, so a
// question does not overtake the poll it depends on.
func awaitTagOnTheAgent(t *testing.T, a *Agent) (device, uid string) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if device, uid, ok := a.TagOn(""); ok {
			return device, uid
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("the agent never reported a tag while serving")
	return "", ""
}

// The agent remembers what was last presented to it. The client server used to
// hold that, so a restart built a new one and the tray and console went blank
// while the card was still sitting on the reader.
func TestTheAgentRemembersTheLastScanAcrossARestart(t *testing.T) {
	rt, err := Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	a := rt.Agent

	if card := a.LastCard(); card != nil {
		t.Errorf("LastCard = %+v, want nothing scanned yet", card)
	}

	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	a.forwardScan(nfc.NFCData{Card: nfc.NewCard(nfc.NewMockTag("04DEADBE"))})

	awaitLastCard(t, a, "04DEADBE")

	// A scan that carries no card is a refusal or a read failure. It reaches
	// the clients as one, and leaves the card the agent last saw alone.
	a.forwardScan(nfc.NFCData{Device: "mock:usb:001", Err: context.DeadlineExceeded})
	if card := a.LastCard(); card == nil || card.UID != "04DEADBE" {
		t.Errorf("LastCard = %+v after a scan with no card, want the card that was scanned", card)
	}

	a.Stop()
	if card := a.LastCard(); card == nil || card.UID != "04DEADBE" {
		t.Errorf("LastCard = %+v after a stop, want the card that was scanned", card)
	}

	if err := a.Start(""); err != nil {
		t.Fatalf("Start again: %v", err)
	}
	defer a.Stop()
	if card := a.LastCard(); card == nil || card.UID != "04DEADBE" {
		t.Errorf("LastCard = %+v after a restart, want the card that was scanned", card)
	}
}

func awaitLastCard(t *testing.T, a *Agent, uid string) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if card := a.LastCard(); card != nil && card.UID == uid {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the agent never reported %s as the last scan", uid)
}
