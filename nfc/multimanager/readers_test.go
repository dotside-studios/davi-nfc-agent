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

func (m *remoteManager) ListDevices() ([]string, error) { return m.devices, nil }
func (m *remoteManager) OpenDevice(string) (nfc.Device, error) {
	return nil, nil
}
func (m *remoteManager) Close() error        { return nil }
func (m *remoteManager) RemoteDevices() bool { return true }

type localManager struct {
	devices []string
}

func (m *localManager) ListDevices() ([]string, error) { return m.devices, nil }
func (m *localManager) OpenDevice(string) (nfc.Device, error) {
	return nil, nil
}
func (m *localManager) Close() error { return nil }

func newMixed(t *testing.T) *MultiManager {
	t.Helper()

	return NewMultiManager(
		ManagerEntry{Name: nfc.ManagerTypeHardware, Manager: &localManager{devices: []string{"ACS ACR122U"}}},
		ManagerEntry{Name: nfc.ManagerTypeSmartphone, Manager: &remoteManager{devices: []string{"smartphone:85bacf02"}}},
	)
}

// A phone reports its scans over the device bridge and is never opened as a
// reader, so offering one as a candidate pins the reader to a device that can
// never connect — which is what filled the log with failed connection attempts.
func TestListReaders_LeavesOutPhones(t *testing.T) {
	readers, err := newMixed(t).ListReaders()
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
func TestListDevices_StillReportsPhones(t *testing.T) {
	devices, err := newMixed(t).ListDevices()
	if err != nil {
		t.Fatalf("ListDevices: %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("ListDevices = %v, want both the reader and the phone", devices)
	}
}

// Recognized from the path alone, because the case that matters is a phone
// pinned in settings and no longer connected.
func TestRemoteDevice_RecognizesAPinnedPhone(t *testing.T) {
	mm := newMixed(t)

	cases := []struct {
		path string
		want bool
	}{
		{"smartphone:85bacf02-3188-4167-9936-c870a5b87679", true},
		{"hardware:ACS ACR122U", false},
		{"acr122_usb:001:003", false},
		{"", false},
	}

	for _, c := range cases {
		if got := nfc.IsRemoteDevice(mm, c.path); got != c.want {
			t.Errorf("IsRemoteDevice(%q) = %v, want %v", c.path, got, c.want)
		}
	}
}
