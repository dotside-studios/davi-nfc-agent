package agent

import (
	"errors"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// rosterManager carries devices of its own, as the phone driver does.
type rosterManager struct {
	devices []string
	active  int
}

func (m *rosterManager) OpenDevice(string) (nfc.Device, error) {
	return nil, errors.New("rosterManager opens nothing")
}
func (m *rosterManager) Devices() ([]nfc.DeviceListing, error) {
	out := make([]nfc.DeviceListing, 0, len(m.devices))
	for _, path := range m.devices {
		out = append(out, nfc.DeviceListing{Path: path, Capabilities: nfc.DeviceCapabilities{SupportsEvents: true}})
	}
	return out, nil
}
func (m *rosterManager) GetDeviceCount() int       { return len(m.devices) }
func (m *rosterManager) GetActiveDeviceCount() int { return m.active }

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

// The counts come from whichever manager carries the devices, without the
// caller naming the driver that does.
func TestRemoteDevicesComeFromTheManager(t *testing.T) {
	m := &rosterManager{devices: []string{"phone-1", "phone-2"}, active: 1}
	a := New(Config{Manager: m})

	total, active := a.RemoteDevices()
	if total != 2 || active != 1 {
		t.Errorf("RemoteDevices() = %d, %d; want 2, 1", total, active)
	}

	online := a.OnlineDevices()
	if len(online) != 2 || online[0] != "phone-1" {
		t.Errorf("OnlineDevices() = %v, want both devices", online)
	}
}

// A manager with no devices of its own reports none rather than failing.
func TestRemoteDevicesAreNoneWithoutADriver(t *testing.T) {
	a := New(Config{Manager: nfc.NewMockManager()})

	if total, active := a.RemoteDevices(); total != 0 || active != 0 {
		t.Errorf("RemoteDevices() = %d, %d; want none", total, active)
	}
	if got := a.OnlineDevices(); got != nil {
		t.Errorf("OnlineDevices() = %v, want nil", got)
	}
}
