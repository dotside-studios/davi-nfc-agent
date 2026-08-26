package agent

import (
	"errors"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// rosterManager carries devices that report their own scans, as the phone
// driver does, naming each by identity rather than by path.
type rosterManager struct {
	devices []string
}

func (m *rosterManager) OpenDevice(string) (nfc.Device, error) {
	return nil, errors.New("rosterManager opens nothing")
}
func (m *rosterManager) Devices() ([]nfc.DeviceListing, error) {
	out := make([]nfc.DeviceListing, 0, len(m.devices))
	for _, id := range m.devices {
		out = append(out, nfc.DeviceListing{
			Path:         "smartphone:" + id,
			ID:           id,
			Capabilities: nfc.DeviceCapabilities{SupportsEvents: true},
		})
	}
	return out, nil
}

// The last card outlives a run: the readers that scanned it are rebuilt by
// every restart, the card presented to them is not.
func TestAStoppedAgentAnswersForTheLastCard(t *testing.T) {
	a := New(Config{Manager: nfc.NewMockManager()})

	if got := a.LastCard(); got != nil {
		t.Errorf("LastCard() = %v before anything was scanned, want nil", got)
	}

	a.reportTag(nfc.NFCData{Card: nfc.NewCard(nfc.NewMockTag("04A1B2C3"))})
	if got := a.LastCard(); got == nil || got.UID != "04A1B2C3" {
		t.Errorf("LastCard() = %v, want the card that was reported", got)
	}
}

// Connected devices come from the manager, by identity: that is what a paired
// device is matched against, not the path an aggregate qualified.
func TestOnlineDevicesComeFromTheManager(t *testing.T) {
	m := &rosterManager{devices: []string{"phone-1", "phone-2"}}
	a := New(Config{Manager: m})

	online := a.OnlineDevices()
	if len(online) != 2 || online[0] != "phone-1" {
		t.Errorf("OnlineDevices() = %v, want both devices by identity", online)
	}
}

// A manager whose devices are all readers reports none connected of its own.
func TestOnlineDevicesAreNoneWithoutADriver(t *testing.T) {
	a := New(Config{Manager: nfc.NewMockManager()})

	if got := a.OnlineDevices(); got != nil {
		t.Errorf("OnlineDevices() = %v, want nil", got)
	}
}
