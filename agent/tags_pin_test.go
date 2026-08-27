package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// pinnedAgent runs two readers and pins the second, so the first is one the
// operator is not asking for and no client is shown.
func pinnedAgent(t *testing.T) *Agent {
	t.Helper()

	m := nfc.NewMockManager()
	m.DevicesList = []string{"mock:usb:001", "mock:usb:002"}
	m.MockDevice.SetTags([]nfc.Tag{nfc.NewMockTag("04A1B2C3")})

	rt, err := Setup(testOptions(t), m)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	a := rt.Agent

	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(a.Stop)

	a.SetPinnedDevice("mock:usb:002")

	// Both readers are polled either way; wait until they have something to
	// hold, or the pin has nothing to exclude.
	deadline := time.Now().Add(3 * time.Second)
	for len(a.supervisor.Load().DevicesHoldingTags()) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("the readers never picked up a tag")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return a
}

// An operation naming no device used to take the first reader holding a tag,
// which is the one the pin excludes whenever that reader is listed first. A
// client shown only the pinned reader's scans could write, and lock, a tag it
// had never been shown.
func TestAnUntargetedOperationStaysOnThePinnedReader(t *testing.T) {
	a := pinnedAgent(t)

	device, _, ok := a.TagOn("")
	if !ok {
		t.Fatal("nothing was holding a tag")
	}
	if device != "mock:usb:002" {
		t.Errorf("an untargeted operation resolved to %q, want the pinned reader", device)
	}
}

// Naming the excluded reader is not a way around the pin. A client only learns
// a reader's name from a scan, and it was never shown one from this reader.
func TestNamingTheExcludedReaderIsRefused(t *testing.T) {
	a := pinnedAgent(t)

	if _, _, ok := a.TagOn("mock:usb:001"); ok {
		t.Error("the excluded reader reported a tag")
	}

	if _, err := a.WriteTag(context.Background(), "mock:usb:001", "04A1B2C3", nfc.NewNDEFMessage(), false, ""); err == nil {
		t.Error("a write to the excluded reader was accepted")
	} else if !strings.Contains(err.Error(), "mock:usb:001") {
		t.Errorf("the refusal reads %q, want it to name the reader", err)
	}

	if _, err := a.LockTag(context.Background(), "mock:usb:001", "04A1B2C3", ""); err == nil {
		t.Error("a lock on the excluded reader was accepted")
	}
	if _, err := a.TransceiveTag(context.Background(), "mock:usb:001", "04A1B2C3", []byte{0x30, 0x00}, true); err == nil {
		t.Error("a transceive on the excluded reader was accepted")
	}
	if _, err := a.TagCapabilities(context.Background(), "mock:usb:001", "04A1B2C3"); err == nil {
		t.Error("capabilities of a tag on the excluded reader were reported")
	}
}

// The excluded reader is not offered for a client to name, and a UID lookup
// walks this list, so a tag known only to that reader is not reachable by UID
// either.
func TestTheExcludedReaderIsNotListed(t *testing.T) {
	a := pinnedAgent(t)

	holding := a.DevicesHoldingTags()
	for _, device := range holding {
		if device == "mock:usb:001" {
			t.Errorf("DevicesHoldingTags = %v, want the excluded reader left out", holding)
		}
	}
	if len(holding) == 0 {
		t.Error("the pinned reader was left out too")
	}
}

// An operation naming no device resolves to a reader the pin admits, rather
// than to whichever one the readers happen to list first. The client server
// always names a device, so this is the path an embedder calling the agent as
// an nfc.TagHolder takes.
func TestAnUnnamedOperationResolvesToAnAdmittedReader(t *testing.T) {
	a := pinnedAgent(t)

	device, err := a.operateOn("")
	if err != nil {
		t.Fatalf("operateOn: %v", err)
	}
	if device != "mock:usb:002" {
		t.Errorf("an operation naming no device resolved to %q, want the pinned reader", device)
	}
}

// With the pin naming a reader that is not attached, nothing is admitted, and
// an operation naming no device is refused rather than falling back to a reader
// the operator excluded.
func TestAnUnnamedOperationIsRefusedWhenNothingIsAdmitted(t *testing.T) {
	a := pinnedAgent(t)
	a.SetPinnedDevice("mock:usb:missing")

	if _, _, ok := a.TagOn(""); ok {
		t.Error("a tag was reported with no admitted reader holding one")
	}
	if _, err := a.operateOn(""); err == nil {
		t.Error("an operation naming no device was accepted with nothing admitted")
	}
}

// phoneManager holds a tag the way a phone driver does: it reports its own
// scans and answers for what its devices hold, and the agent never opened it as
// a reader.
// nfc.TagsHeldBy asserts TagHolder at runtime, so a fake that drifts out of the
// interface stops standing in for a phone driver without failing to build.
var _ nfc.TagHolder = (*phoneManager)(nil)

type phoneManager struct {
	nfc.Manager
	device string
	uid    string
}

func (m *phoneManager) TagOn(device string) (string, string, bool) {
	if device == "" || device == m.device {
		return m.device, m.uid, true
	}
	return "", "", false
}

func (m *phoneManager) DevicesHoldingTags() []string { return []string{m.device} }

func (m *phoneManager) WriteTag(_ context.Context, device, uid string, _ *nfc.NDEFMessage, lock bool, _ string) (*nfc.WriteResult, error) {
	if device != m.device {
		return nil, errors.New("not this device")
	}
	return &nfc.WriteResult{UID: uid, Locked: lock}, nil
}

func (m *phoneManager) LockTag(_ context.Context, device, uid, _ string) (*nfc.LockResult, error) {
	return &nfc.LockResult{UID: uid, Locked: true}, nil
}

func (m *phoneManager) TransceiveTag(_ context.Context, _, _ string, data []byte, _ bool) ([]byte, error) {
	return data, nil
}

func (m *phoneManager) TagCapabilities(_ context.Context, _, uid string) (*nfc.TagCapabilities, error) {
	caps := nfc.GetTagCapabilities(nfc.NewMockTag(uid))
	return &caps, nil
}

// Pinning a reader says nothing about a device that reports its own scans. The
// operator picked which reader to work with, not which phone, so a phone's tag
// stays reachable for operations as well as for scans.
func TestAPinnedReaderLeavesADevicesTagsReachable(t *testing.T) {
	m := &phoneManager{Manager: nfc.NewMockManager(), device: "phone-9f2a", uid: "04ABCDEF"}

	rt, err := Setup(testOptions(t), m)
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	a := rt.Agent

	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	a.SetPinnedDevice("mock:usb:001")

	if _, _, ok := a.TagOn("phone-9f2a"); !ok {
		t.Error("pinning a reader hid the phone's tag")
	}
	if _, err := a.WriteTag(context.Background(), "phone-9f2a", "04ABCDEF", nfc.NewNDEFMessage(), false, ""); err != nil {
		t.Errorf("pinning a reader refused a write to the phone: %v", err)
	}

	var listed bool
	for _, device := range a.DevicesHoldingTags() {
		if device == "phone-9f2a" {
			listed = true
		}
	}
	if !listed {
		t.Errorf("DevicesHoldingTags = %v, want the phone in it", a.DevicesHoldingTags())
	}
}

// Auto-detect excludes nothing: every reader is served, which is what an agent
// with no preference set should do.
func TestAutoDetectAdmitsEveryReader(t *testing.T) {
	a := pinnedAgent(t)
	a.SetPinnedDevice("")

	if _, _, ok := a.TagOn("mock:usb:001"); !ok {
		t.Error("a reader was excluded with nothing pinned")
	}
	if got := len(a.DevicesHoldingTags()); got != 2 {
		t.Errorf("DevicesHoldingTags reported %d readers, want both", got)
	}
}
