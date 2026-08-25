package multimanager

import (
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
}

func newHoldingManager(name string, holding map[string]string) *holdingManager {
	return &holdingManager{
		mockManager: mockManager{name: name},
		holding:     holding,
		wrote:       map[string]bool{},
	}
}

func (m *holdingManager) TagOn(deviceID string) (string, nfc.Tag, bool) {
	if deviceID == "" {
		for id := range m.holding {
			return id, nfc.NewMockTag(m.holding[id]), true
		}
		return "", nil, false
	}
	uid, ok := m.holding[deviceID]
	if !ok {
		return "", nil, false
	}
	return deviceID, nfc.NewMockTag(uid), true
}

func (m *holdingManager) DevicesHoldingTags() []string {
	out := make([]string, 0, len(m.holding))
	for id := range m.holding {
		out = append(out, id)
	}
	return out
}

func (m *holdingManager) WriteTag(deviceID, tagUID string, ndef []byte, lock bool, key string) error {
	if _, ok := m.holding[deviceID]; !ok {
		return errors.New("device is not holding a tag")
	}
	m.wrote[deviceID] = true
	return nil
}

func (m *holdingManager) TransceiveTag(deviceID, tagUID string, data []byte, raw bool) ([]byte, error) {
	return data, nil
}

// The aggregate is what the agent asks, so a tag held by a child has to be
// reachable through it.
func TestMultiManagerAnswersForTagsItsChildrenHold(t *testing.T) {
	phones := newHoldingManager("smartphone", map[string]string{"phone-1": "04A1B2C3"})
	mm := NewMultiManager(
		ManagerEntry{Name: "hardware", Manager: &mockManager{name: "hardware"}},
		ManagerEntry{Name: "smartphone", Manager: phones},
	)

	if nfc.TagsHeldBy(mm) == nil {
		t.Fatal("the aggregate reports no held tags with a child that has some")
	}

	holding, tag, ok := mm.TagOn("phone-1")
	if !ok || holding != "phone-1" || tag.UID() != "04A1B2C3" {
		t.Errorf("TagOn = %q, %v, %v; want the phone and its tag", holding, tag, ok)
	}

	if got := mm.DevicesHoldingTags(); len(got) != 1 || got[0] != "phone-1" {
		t.Errorf("DevicesHoldingTags = %v, want [phone-1]", got)
	}

	if err := mm.WriteTag("phone-1", "04A1B2C3", []byte{0x03}, false, "key-1"); err != nil {
		t.Fatalf("WriteTag: %v", err)
	}
	if !phones.wrote["phone-1"] {
		t.Error("the write did not reach the manager holding the tag")
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
	if err := mm.WriteTag("phone-1", "04A1B2C3", nil, false, "key-1"); err == nil {
		t.Error("a write with no manager holding tags was accepted")
	}
}
