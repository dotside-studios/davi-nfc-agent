package multimanager_test

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/multimanager"
)

// polled stands in for a locally attached backend: no sessions, so no way to
// end one.
type polled struct{ nfc.Manager }

// sessions stands in for a backend whose devices dial in.
type sessions struct {
	nfc.Manager
	holding string
	asked   []string
}

func (s *sessions) DisconnectDevice(deviceID, _ string) bool {
	s.asked = append(s.asked, deviceID)
	return deviceID == s.holding
}

// A revoked credential names a device, not a manager: the ID a device holds
// with its driver says nothing about which driver that is. The aggregate asks
// each child, and only those holding sessions are asked.
func TestDisconnectReachesOnlyChildrenHoldingSessions(t *testing.T) {
	phones := &sessions{Manager: nfc.NewMockManager(), holding: "phone-9f2a"}
	mm := multimanager.NewMultiManager(
		multimanager.ManagerEntry{Name: "hardware", Manager: polled{nfc.NewMockManager()}},
		multimanager.ManagerEntry{Name: "smartphone", Manager: phones},
	)

	if !mm.DisconnectDevice("phone-9f2a", "device revoked") {
		t.Error("the aggregate reported ending no session for a device a child holds")
	}
	if len(phones.asked) != 1 || phones.asked[0] != "phone-9f2a" {
		t.Errorf("the child was asked about %v, want just the revoked device", phones.asked)
	}
}

// A device no child holds is nothing to end, not an error.
func TestDisconnectingAnAbsentDeviceReportsNothingEnded(t *testing.T) {
	phones := &sessions{Manager: nfc.NewMockManager(), holding: "phone-9f2a"}
	mm := multimanager.NewMultiManager(
		multimanager.ManagerEntry{Name: "smartphone", Manager: phones},
	)

	if mm.DisconnectDevice("someone-else", "device revoked") {
		t.Error("the aggregate reported ending a session no child holds")
	}
}

// An aggregate of polled readers answers without reaching for an interface none
// of them implement.
func TestDisconnectOverPolledReadersOnly(t *testing.T) {
	mm := multimanager.NewMultiManager(
		multimanager.ManagerEntry{Name: "hardware", Manager: polled{nfc.NewMockManager()}},
	)

	if mm.DisconnectDevice("anyone", "device revoked") {
		t.Error("a reader-only aggregate reported ending a session")
	}
}
