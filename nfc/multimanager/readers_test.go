package multimanager

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// remoteManager stands in for the smartphone manager: it holds devices, and
// none of them is a reader attached to this machine.
type remoteManager struct {
	devices []string
}

func (m *remoteManager) Devices() ([]nfc.DeviceListing, error) {
	return listings(m.devices, nfc.DeviceCapabilities{SupportsEvents: true}), nil
}
func (m *remoteManager) OpenDevice(string) (nfc.Device, error) {
	return nil, nil
}
func (m *remoteManager) Close() error { return nil }

type localManager struct {
	devices []string
}

func (m *localManager) Devices() ([]nfc.DeviceListing, error) {
	return listings(m.devices, nfc.DeviceCapabilities{CanPoll: true}), nil
}
func (m *localManager) OpenDevice(string) (nfc.Device, error) {
	return nil, nil
}
func (m *localManager) Close() error { return nil }

// listings describes every path the same way, as a driver with a fixed
// transport does.
func listings(paths []string, caps nfc.DeviceCapabilities) []nfc.DeviceListing {
	out := make([]nfc.DeviceListing, 0, len(paths))
	for _, path := range paths {
		out = append(out, nfc.DeviceListing{Path: path, Capabilities: caps})
	}
	return out
}

func newMixed(t *testing.T) *MultiManager {
	t.Helper()

	return NewMultiManager(
		ManagerEntry{Name: nfc.ManagerTypeHardware, Manager: &localManager{devices: []string{"ACS ACR122U"}}},
		ManagerEntry{Name: nfc.ManagerTypeSmartphone, Manager: &remoteManager{devices: []string{"smartphone:85bacf02"}}},
	)
}

// A phone reports its scans over the device bridge and is never opened as a
// reader, so offering one as a candidate pins the reader to a device that can
// never connect, which is what filled the log with failed connection attempts.
func TestListReaders_LeavesOutPhones(t *testing.T) {
	readers, err := nfc.ListReaders(newMixed(t))
	if err != nil {
		t.Fatalf("ListReaders: %v", err)
	}

	for _, device := range readers {
		if device == "smartphone:85bacf02" {
			t.Fatalf("ListReaders offered a phone as a reader: %v", readers)
		}
	}
	if len(readers) != 1 || readers[0] != "hardware:ACS ACR122U" {
		t.Fatalf("ListReaders = %v, want the hardware reader alone", readers)
	}
}

// The full list is what the device panel and the pairing views are built from,
// so narrowing it to readers would hide the phones an operator has paired.
func TestDevices_StillReportsPhones(t *testing.T) {
	devices, err := newMixed(t).Devices()
	if err != nil {
		t.Fatalf("Devices: %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("Devices() = %v, want both the reader and the phone", devices)
	}
}
