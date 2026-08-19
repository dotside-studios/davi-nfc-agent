package remotenfc

import (
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// scanning registers a device and gives it a tag, the way a session does.
func scanning(t *testing.T, m *Manager, uid string) *Device {
	t.Helper()

	dev, err := m.RegisterDevice(DeviceRegistrationRequest{
		DeviceName: "Test Phone", Platform: "android", ProtocolVersion: DeviceProtocolV1,
		Capabilities: DeviceCapabilities{CanRead: true},
	})
	if err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}

	if err := m.SendTagData(dev.DeviceID(), TagData{
		DeviceID: dev.DeviceID(), UID: uid, Technology: "ISO14443A", Type: "NTAG215",
	}); err != nil {
		t.Fatalf("SendTagData: %v", err)
	}
	return dev
}

// A device is the thing holding a tag, so asking it what it holds is the
// question nfc.Device.GetTags already exists to answer. It used to answer
// nothing for a phone, which is why a parallel registry grew beside it.
func TestDeviceReportsTheTagItHolds(t *testing.T) {
	m := NewManager(DeviceTimeout)
	defer m.Close()

	dev := scanning(t, m, "04:A1:B2:C3")

	tags, err := dev.GetTags()
	if err != nil {
		t.Fatalf("GetTags: %v", err)
	}
	if len(tags) != 1 {
		t.Fatalf("GetTags returned %d tags, want the one the device is holding", len(tags))
	}
	if tags[0].UID() != "04:A1:B2:C3" {
		t.Errorf("UID = %q, want the scanned tag", tags[0].UID())
	}
}

// With no tag it answers empty, after a wait: that is what paces a caller
// polling a device with nothing in its field.
func TestDeviceWithoutATagWaitsThenReportsNone(t *testing.T) {
	m := NewManager(DeviceTimeout)
	defer m.Close()

	dev, err := m.RegisterDevice(DeviceRegistrationRequest{
		DeviceName: "Empty", Platform: "ios", ProtocolVersion: DeviceProtocolV1,
	})
	if err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}

	start := time.Now()
	tags, err := dev.GetTags()
	if err != nil {
		t.Fatalf("GetTags: %v", err)
	}
	if len(tags) != 0 {
		t.Errorf("GetTags returned %d tags, want none", len(tags))
	}
	if time.Since(start) < GetTagsTimeout/2 {
		t.Error("GetTags returned immediately with no tag; a poller would spin")
	}
}

// A tag reported present is reported gone when it leaves the field.
func TestDeviceForgetsTheTagOnRemoval(t *testing.T) {
	m := NewManager(DeviceTimeout)
	defer m.Close()

	dev := scanning(t, m, "04:A1:B2:C3")
	m.clearActiveTag(dev.DeviceID(), "04:A1:B2:C3")

	tags, err := dev.GetTags()
	if err != nil {
		t.Fatalf("GetTags: %v", err)
	}
	if len(tags) != 0 {
		t.Errorf("GetTags still reports %d tags after removal", len(tags))
	}
}

// The manager's view and the device's view are the same fact.
func TestActiveTagAgreesWithTheDevice(t *testing.T) {
	m := NewManager(DeviceTimeout)
	defer m.Close()

	dev := scanning(t, m, "04:A1:B2:C3")

	info, ok := m.ActiveTag(dev.DeviceID())
	if !ok {
		t.Fatal("ActiveTag reports no tag")
	}

	tags, _ := dev.GetTags()
	if len(tags) != 1 || tags[0].UID() != info.UID {
		t.Errorf("device says %v, manager says %q", tags, info.UID)
	}
}

var _ nfc.Device = (*Device)(nil)
