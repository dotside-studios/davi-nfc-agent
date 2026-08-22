package agent

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/multimanager"
	"github.com/dotside-studios/davi-nfc-agent/nfc/pcsc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
)

func TestFindDeviceDriverBehindMultiManager(t *testing.T) {
	remote := remotenfc.NewManager(remotenfc.DeviceTimeout)
	defer remote.Close()

	mm := multimanager.NewMultiManager(
		multimanager.ManagerEntry{Name: nfc.ManagerTypeHardware, Manager: pcsc.NewManager()},
		multimanager.ManagerEntry{Name: nfc.ManagerTypeSmartphone, Manager: remote},
	)

	if got := findDeviceDriver(mm); got != remote {
		t.Errorf("findDeviceDriver = %#v, want the remote driver", got)
	}
}

func TestFindDeviceDriverHeldDirectly(t *testing.T) {
	remote := remotenfc.NewManager(remotenfc.DeviceTimeout)
	defer remote.Close()

	if got := findDeviceDriver(remote); got != remote {
		t.Errorf("findDeviceDriver = %#v, want the remote driver", got)
	}
}

// The device server's nil check decides whether the device endpoint is mounted
// at all, so a manager with no remote driver has to answer nil.
func TestFindDeviceDriverWithoutOneIsNil(t *testing.T) {
	mm := multimanager.NewMultiManager(
		multimanager.ManagerEntry{Name: nfc.ManagerTypeHardware, Manager: pcsc.NewManager()},
	)

	if got := findDeviceDriver(mm); got != nil {
		t.Errorf("findDeviceDriver = %#v, want nil", got)
	}
	if got := findDeviceDriver(nil); got != nil {
		t.Errorf("findDeviceDriver(nil) = %#v, want nil", got)
	}
}
