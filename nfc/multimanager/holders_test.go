package multimanager

import (
	"context"
	"errors"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// holdingManager is a manager whose devices hold tags of their own, which is
// what the phone driver is.
type holdingManager struct {
	mockManager
	holding map[string]string // deviceID -> tag UID
	wrote   map[string]bool   // deviceID -> was asked to write
	locked  map[string]bool   // deviceID -> was asked to lock
}

func newHoldingManager(name string, holding map[string]string) *holdingManager {
	return &holdingManager{
		mockManager: mockManager{name: name},
		holding:     holding,
		wrote:       map[string]bool{},
		locked:      map[string]bool{},
	}
}

func (m *holdingManager) TagOn(deviceID string) (string, string, bool) {
	if deviceID == "" {
		for id := range m.holding {
			return id, m.holding[id], true
		}
		return "", "", false
	}
	uid, ok := m.holding[deviceID]
	if !ok {
		return "", "", false
	}
	return deviceID, uid, true
}

func (m *holdingManager) DevicesHoldingTags() []string {
	out := make([]string, 0, len(m.holding))
	for id := range m.holding {
		out = append(out, id)
	}
	return out
}

func (m *holdingManager) WriteTag(_ context.Context, deviceID, tagUID string, msg *nfc.NDEFMessage, lock bool, key string) (*nfc.WriteResult, error) {
	if _, ok := m.holding[deviceID]; !ok {
		return nil, errors.New("device is not holding a tag")
	}
	m.wrote[deviceID] = true
	return &nfc.WriteResult{UID: tagUID, Locked: lock}, nil
}

func (m *holdingManager) LockTag(_ context.Context, deviceID, tagUID, key string) (*nfc.LockResult, error) {
	if _, ok := m.holding[deviceID]; !ok {
		return nil, errors.New("device is not holding a tag")
	}
	m.locked[deviceID] = true
	return &nfc.LockResult{UID: tagUID, Locked: true}, nil
}

// phoneTagFamily is what this manager answers with, so an answer that did not
// come from it is recognisable.
const phoneTagFamily = "held-by-the-phone"

func (m *holdingManager) TagCapabilities(_ context.Context, deviceID, tagUID string) (*nfc.TagCapabilities, error) {
	uid, ok := m.holding[deviceID]
	if !ok {
		return nil, errors.New("device is not holding a tag")
	}
	caps := nfc.GetTagCapabilities(nfc.NewMockTag(uid))
	caps.TagFamily = phoneTagFamily
	return &caps, nil
}

func (m *holdingManager) TransceiveTag(_ context.Context, deviceID, tagUID string, data []byte, raw bool) ([]byte, error) {
	return data, nil
}

// The aggregate is what the agent asks, so a tag held by a child has to be
// reachable through it.
func TestImplementsTagHolder(t *testing.T) {
	// nfc.TagsHeldBy asserts these at runtime, so a signature that drifts out
	// of nfc.TagHolder degrades to "no device holds tags" instead of failing to
	// build.
	var _ nfc.TagHolder = (*MultiManager)(nil)
	var _ nfc.TagHolder = (*holdingManager)(nil)
}

func TestMultiManagerAnswersForTagsItsChildrenHold(t *testing.T) {
	phones := newHoldingManager("smartphone", map[string]string{"phone-1": "04A1B2C3"})
	mm := NewMultiManager(
		ManagerEntry{Name: "hardware", Manager: &mockManager{name: "hardware"}},
		ManagerEntry{Name: "smartphone", Manager: phones},
	)

	if nfc.TagsHeldBy(mm) == nil {
		t.Fatal("the aggregate reports no held tags with a child that has some")
	}

	holding, uid, ok := mm.TagOn("phone-1")
	if !ok || holding != "phone-1" || uid != "04A1B2C3" {
		t.Errorf("TagOn = %q, %q, %v; want the phone and its tag", holding, uid, ok)
	}

	if got := mm.DevicesHoldingTags(); len(got) != 1 || got[0] != "phone-1" {
		t.Errorf("DevicesHoldingTags = %v, want [phone-1]", got)
	}

	if _, err := mm.WriteTag(context.Background(), "phone-1", "04A1B2C3", nfc.NewNDEFMessage(), false, "key-1"); err != nil {
		t.Fatalf("WriteTag: %v", err)
	}
	if !phones.wrote["phone-1"] {
		t.Error("the write did not reach the manager holding the tag")
	}

	if _, err := mm.LockTag(context.Background(), "phone-1", "04A1B2C3", "key-2"); err != nil {
		t.Fatalf("LockTag: %v", err)
	}
	if !phones.locked["phone-1"] {
		t.Error("the lock did not reach the manager holding the tag")
	}

	caps, err := mm.TagCapabilities(context.Background(), "phone-1", "04A1B2C3")
	if err != nil {
		t.Fatalf("TagCapabilities: %v", err)
	}
	if caps == nil || caps.TagFamily != phoneTagFamily {
		t.Errorf("TagCapabilities = %+v, want what the manager holding the tag answered", caps)
	}
}

// A manager whose devices are polled through a reader holds nothing, and the
// aggregate must not claim otherwise.
func TestMultiManagerHoldsNothingWithoutAHoldingChild(t *testing.T) {
	mm := NewMultiManager(ManagerEntry{Name: "hardware", Manager: &mockManager{name: "hardware"}})

	if _, _, ok := mm.TagOn("phone-1"); ok {
		t.Error("the aggregate answered for a tag no child holds")
	}
	if got := mm.DevicesHoldingTags(); len(got) != 0 {
		t.Errorf("DevicesHoldingTags = %v, want none", got)
	}
	if _, err := mm.WriteTag(context.Background(), "phone-1", "04A1B2C3", nil, false, "key-1"); err == nil {
		t.Error("a write with no manager holding tags was accepted")
	}
}
