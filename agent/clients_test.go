package agent

import (
	"errors"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// rosterManager carries devices that report their own scans, as the phone
// driver does, and names each by the identity it holds rather than by its path.
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

// A surface asks the agent, and the agent answers whether or not it is running.
// These used to be five nil checks on Agent.ClientServer in the console, which
// is a field that does not exist until Start.
func TestAStoppedAgentAnswersAboutItsClients(t *testing.T) {
	a := New(Config{Manager: nfc.NewMockManager()})

	if got := a.ClientCount(); got != 0 {
		t.Errorf("ClientCount() = %d on a stopped agent, want 0", got)
	}
	if got := a.Clients(); got != nil {
		t.Errorf("Clients() = %v on a stopped agent, want nil", got)
	}
	if got := a.LastCard(); got != nil {
		t.Errorf("LastCard() = %v on a stopped agent, want nil", got)
	}
	if err := a.DisconnectClient("whoever"); err == nil {
		t.Error("DisconnectClient succeeded on a stopped agent")
	}
}

// The devices connected come from whatever manager carries them, by the
// identity each holds: what a paired device is matched against is that, not the
// path an aggregate qualified.
func TestOnlineDevicesComeFromTheManager(t *testing.T) {
	m := &rosterManager{devices: []string{"phone-1", "phone-2"}}
	a := New(Config{Manager: m})

	online := a.OnlineDevices()
	if len(online) != 2 || online[0] != "phone-1" {
		t.Errorf("OnlineDevices() = %v, want both devices by identity", online)
	}
}

// A manager whose devices are all readers has none connected of its own, and
// says so rather than counting the readers.
func TestOnlineDevicesAreNoneWithoutADriver(t *testing.T) {
	a := New(Config{Manager: nfc.NewMockManager()})

	if got := a.OnlineDevices(); got != nil {
		t.Errorf("OnlineDevices() = %v, want nil", got)
	}
}
