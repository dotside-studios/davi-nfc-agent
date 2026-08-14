package remotenfc

import (
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

func TestDeviceCreation(t *testing.T) {
	req := DeviceRegistrationRequest{
		DeviceName: "Test iPhone",
		Platform:   "ios",
		AppVersion: "1.0.0",
		Capabilities: DeviceCapabilities{
			CanRead:  true,
			CanWrite: false,
			NFCType:  "isodep",
		},
		Metadata: map[string]string{
			"model": "iPhone14,2",
		},
	}

	device := NewDevice("test-device-id", req)

	if device.DeviceID() != "test-device-id" {
		t.Errorf("DeviceID() = %v, want %v", device.DeviceID(), "test-device-id")
	}

	if device.Connection() != "smartphone:test-device-id" {
		t.Errorf("Connection() = %v, want %v", device.Connection(), "smartphone:test-device-id")
	}

	if device.Platform() != "ios" {
		t.Errorf("Platform() = %v, want %v", device.Platform(), "ios")
	}

	if device.AppVersion() != "1.0.0" {
		t.Errorf("AppVersion() = %v, want %v", device.AppVersion(), "1.0.0")
	}

	if !device.IsActive() {
		t.Error("Device should be active after creation")
	}

	caps := device.PhoneCapabilities()
	if !caps.CanRead || caps.CanWrite {
		t.Errorf("PhoneCapabilities not set correctly: %+v", caps)
	}
}

func TestDeviceImplementsNFCDevice(t *testing.T) {
	// Verify Device implements nfc.Device interface
	var _ nfc.Device = (*Device)(nil)
}

func TestDeviceGetTags(t *testing.T) {
	req := DeviceRegistrationRequest{
		DeviceName: "Test Device",
		Platform:   "android",
		AppVersion: "1.0.0",
	}

	device := NewDevice("test-id", req)
	defer func() { _ = device.Close() }()

	// Test timeout (no tags sent)
	tags, err := device.GetTags()
	if err != nil {
		t.Errorf("GetTags() with timeout should not return error, got: %v", err)
	}
	if len(tags) != 0 {
		t.Errorf("GetTags() with timeout should return empty slice, got %d tags", len(tags))
	}

	// Test sending tags
	mockTag := &Tag{
		uid:        "04:AB:CD:EF",
		tagType:    "Type4",
		technology: "ISO14443A",
	}

	err = device.SendTags([]nfc.Tag{mockTag})
	if err != nil {
		t.Errorf("SendTags() failed: %v", err)
	}

	// Retrieve tags
	tags, err = device.GetTags()
	if err != nil {
		t.Errorf("GetTags() failed: %v", err)
	}
	if len(tags) != 1 {
		t.Errorf("GetTags() returned %d tags, want 1", len(tags))
	}
}

func TestDeviceClose(t *testing.T) {
	req := DeviceRegistrationRequest{
		DeviceName: "Test Device",
		Platform:   "ios",
		AppVersion: "1.0.0",
	}

	device := NewDevice("test-id", req)

	if !device.IsActive() {
		t.Error("Device should be active before Close()")
	}

	err := device.Close()
	if err != nil {
		t.Errorf("Close() failed: %v", err)
	}

	if device.IsActive() {
		t.Error("Device should not be active after Close()")
	}

	// Test GetTags after close
	_, err = device.GetTags()
	if err == nil {
		t.Error("GetTags() should return error after Close()")
	}
}

func TestDeviceIsHealthy(t *testing.T) {
	req := DeviceRegistrationRequest{
		DeviceName: "Test Device",
		Platform:   "android",
		AppVersion: "1.0.0",
	}

	device := NewDevice("test-id", req)
	defer func() { _ = device.Close() }()

	// Should succeed when active and recent
	err := device.IsHealthy()
	if err != nil {
		t.Errorf("IsHealthy() failed: %v", err)
	}

	// Test timeout by setting old lastSeen
	device.mu.Lock()
	device.lastSeen = time.Now().Add(-DeviceTimeout - time.Second)
	device.mu.Unlock()

	err = device.IsHealthy()
	if err == nil {
		t.Error("IsHealthy() should fail after timeout")
	}
}

func TestDeviceUpdateLastSeen(t *testing.T) {
	req := DeviceRegistrationRequest{
		DeviceName: "Test Device",
		Platform:   "ios",
		AppVersion: "1.0.0",
	}

	device := NewDevice("test-id", req)
	defer func() { _ = device.Close() }()

	firstSeen := device.LastSeen()
	time.Sleep(10 * time.Millisecond)

	device.UpdateLastSeen()
	secondSeen := device.LastSeen()

	if !secondSeen.After(firstSeen) {
		t.Error("LastSeen should be updated after UpdateLastSeen()")
	}
}

func TestDeviceTransceive(t *testing.T) {
	req := DeviceRegistrationRequest{
		DeviceName: "Test Device",
		Platform:   "android",
		AppVersion: "1.0.0",
	}

	device := NewDevice("test-id", req)
	defer func() { _ = device.Close() }()

	// Transceive should not be supported
	_, err := device.Transceive([]byte{0x01, 0x02})
	if err == nil {
		t.Error("Transceive() should return error (not supported)")
	}
}

func TestDeviceString(t *testing.T) {
	req := DeviceRegistrationRequest{
		DeviceName: "Test iPhone 12",
		Platform:   "ios",
		AppVersion: "1.0.0",
	}

	device := NewDevice("test-id-123", req)
	defer func() { _ = device.Close() }()

	str := device.String()
	expectedSubstr := "Test iPhone 12"
	if len(str) == 0 {
		t.Error("String() should not return empty string")
	}
	// Check if device name is in the string
	found := false
	for i := 0; i <= len(str)-len(expectedSubstr); i++ {
		if str[i:i+len(expectedSubstr)] == expectedSubstr {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("String() = %v, should contain %v", str, expectedSubstr)
	}
}
