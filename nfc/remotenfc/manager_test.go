package remotenfc

import (
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

func TestNewManager(t *testing.T) {
	m := NewManager(30 * time.Second)
	defer m.Close()

	if m == nil {
		t.Fatal("NewManager() returned nil")
	}

	if m.GetDeviceCount() != 0 {
		t.Errorf("New manager should have 0 devices, got %d", m.GetDeviceCount())
	}
}

func TestManagerImplementsNFCManager(t *testing.T) {
	// Verify Manager implements nfc.Manager interface
	var _ nfc.Manager = (*Manager)(nil)
}

func TestManagerRegisterDevice(t *testing.T) {
	m := NewManager(30 * time.Second)
	defer m.Close()

	req := DeviceRegistrationRequest{
		DeviceName: "Test iPhone",
		Platform:   "ios",
		AppVersion: "1.0.0",
		Capabilities: &DeviceCapabilities{
			CanRead:  true,
			CanWrite: true,
			NFCType:  "isodep",
		},
	}

	device, err := m.RegisterDevice(req)
	if err != nil {
		t.Fatalf("RegisterDevice() failed: %v", err)
	}

	if device == nil {
		t.Fatal("RegisterDevice() returned nil device")
	}

	if device.DeviceID() == "" {
		t.Error("Device should have non-empty ID")
	}

	if m.GetDeviceCount() != 1 {
		t.Errorf("Manager should have 1 device, got %d", m.GetDeviceCount())
	}
}

func TestManagerRegisterDeviceValidation(t *testing.T) {
	m := NewManager(30 * time.Second)
	defer m.Close()

	tests := []struct {
		name    string
		req     DeviceRegistrationRequest
		wantErr bool
	}{
		{
			name: "valid iOS device",
			req: DeviceRegistrationRequest{
				DeviceName: "iPhone",
				Platform:   "ios",
				AppVersion: "1.0.0",
			},
			wantErr: false,
		},
		{
			name: "valid Android device",
			req: DeviceRegistrationRequest{
				DeviceName: "Pixel",
				Platform:   "android",
				AppVersion: "1.0.0",
			},
			wantErr: false,
		},
		{
			name: "missing device name",
			req: DeviceRegistrationRequest{
				Platform:   "ios",
				AppVersion: "1.0.0",
			},
			wantErr: true,
		},
		{
			name: "a platform outside the three the agent knows",
			req: DeviceRegistrationRequest{
				DeviceName: "PN532 bridge",
				Platform:   "pn532-serial",
				AppVersion: "1.0.0",
			},
			wantErr: false,
		},
		{
			name: "the platform the bundled client defaults to",
			req: DeviceRegistrationRequest{
				DeviceName: "Device",
				Platform:   "unknown",
				AppVersion: "1.0.0",
			},
			wantErr: false,
		},
		{
			name: "the platform the bundled client's Node example sends",
			req: DeviceRegistrationRequest{
				DeviceName: "My NFC Device",
				Platform:   "node",
				AppVersion: "1.0.0",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := m.RegisterDevice(tt.req)
			if (err != nil) != tt.wantErr {
				t.Errorf("RegisterDevice() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestManagerUnregisterDevice(t *testing.T) {
	m := NewManager(30 * time.Second)
	defer m.Close()

	req := DeviceRegistrationRequest{
		DeviceName: "Test Device",
		Platform:   "ios",
		AppVersion: "1.0.0",
	}

	device, err := m.RegisterDevice(req)
	if err != nil {
		t.Fatalf("RegisterDevice() failed: %v", err)
	}

	deviceID := device.DeviceID()

	// Unregister device
	err = m.UnregisterDevice(deviceID)
	if err != nil {
		t.Errorf("UnregisterDevice() failed: %v", err)
	}

	if m.GetDeviceCount() != 0 {
		t.Errorf("Manager should have 0 devices after unregister, got %d", m.GetDeviceCount())
	}

	// Try to unregister again (should fail)
	err = m.UnregisterDevice(deviceID)
	if err == nil {
		t.Error("UnregisterDevice() should fail for non-existent device")
	}
}

func TestManagerOpenDevice(t *testing.T) {
	m := NewManager(30 * time.Second)
	defer m.Close()

	req := DeviceRegistrationRequest{
		DeviceName: "Test Device",
		Platform:   "android",
		AppVersion: "1.0.0",
	}

	registeredDevice, err := m.RegisterDevice(req)
	if err != nil {
		t.Fatalf("RegisterDevice() failed: %v", err)
	}

	deviceID := registeredDevice.DeviceID()

	// Test opening with full connection string
	device, err := m.OpenDevice("smartphone:" + deviceID)
	if err != nil {
		t.Errorf("OpenDevice() with prefix failed: %v", err)
	}
	if device == nil {
		t.Error("OpenDevice() returned nil device")
	}

	// Test opening with just device ID
	device, err = m.OpenDevice(deviceID)
	if err != nil {
		t.Errorf("OpenDevice() without prefix failed: %v", err)
	}
	if device == nil {
		t.Error("OpenDevice() returned nil device")
	}

	// Test opening non-existent device
	_, err = m.OpenDevice("non-existent-id")
	if err == nil {
		t.Error("OpenDevice() should fail for non-existent device")
	}
}

func TestManagerListDevices(t *testing.T) {
	m := NewManager(30 * time.Second)
	defer m.Close()

	// Initially empty
	devices, err := m.ListDevices()
	if err != nil {
		t.Errorf("ListDevices() failed: %v", err)
	}
	if len(devices) != 0 {
		t.Errorf("ListDevices() should return empty list, got %d devices", len(devices))
	}

	// Register multiple devices
	for i := 0; i < 3; i++ {
		req := DeviceRegistrationRequest{
			DeviceName: "Device",
			Platform:   "ios",
			AppVersion: "1.0.0",
		}
		_, err := m.RegisterDevice(req)
		if err != nil {
			t.Fatalf("RegisterDevice() failed: %v", err)
		}
	}

	devices, err = m.ListDevices()
	if err != nil {
		t.Errorf("ListDevices() failed: %v", err)
	}
	if len(devices) != 3 {
		t.Errorf("ListDevices() should return 3 devices, got %d", len(devices))
	}

	// Check format
	for _, device := range devices {
		if len(device) < len("smartphone:") {
			t.Errorf("Device string should have 'smartphone:' prefix, got: %s", device)
		}
	}
}

func TestManagerSendTagData(t *testing.T) {
	m := NewManager(30 * time.Second)
	defer m.Close()

	req := DeviceRegistrationRequest{
		DeviceName: "Test Device",
		Platform:   "ios",
		AppVersion: "1.0.0",
	}

	device, err := m.RegisterDevice(req)
	if err != nil {
		t.Fatalf("RegisterDevice() failed: %v", err)
	}

	tagData := TagData{
		DeviceID:   device.DeviceID(),
		UID:        "04:AB:CD:EF",
		Technology: "ISO14443A",
		Type:       "Type4",
		ScannedAt:  time.Now(),
	}

	// Send tag data
	err = m.SendTagData(device.DeviceID(), tagData)
	if err != nil {
		t.Errorf("SendTagData() failed: %v", err)
	}

	// Try sending to non-existent device
	tagData.DeviceID = "non-existent"
	err = m.SendTagData("non-existent", tagData)
	if err == nil {
		t.Error("SendTagData() should fail for non-existent device")
	}
}

func TestManagerUpdateHeartbeat(t *testing.T) {
	m := NewManager(30 * time.Second)
	defer m.Close()

	req := DeviceRegistrationRequest{
		DeviceName: "Test Device",
		Platform:   "android",
		AppVersion: "1.0.0",
	}

	device, err := m.RegisterDevice(req)
	if err != nil {
		t.Fatalf("RegisterDevice() failed: %v", err)
	}

	deviceID := device.DeviceID()
	firstSeen := device.LastSeen()

	time.Sleep(10 * time.Millisecond)

	err = m.UpdateHeartbeat(deviceID)
	if err != nil {
		t.Errorf("UpdateHeartbeat() failed: %v", err)
	}

	// Get device and check last seen
	updatedDevice, exists := m.GetDevice(deviceID)
	if !exists {
		t.Fatal("Device should exist after heartbeat")
	}

	if !updatedDevice.LastSeen().After(firstSeen) {
		t.Error("LastSeen should be updated after heartbeat")
	}

	// Try updating non-existent device
	err = m.UpdateHeartbeat("non-existent")
	if err == nil {
		t.Error("UpdateHeartbeat() should fail for non-existent device")
	}
}

func TestManagerCleanupInactiveDevices(t *testing.T) {
	// Use short timeout for testing
	m := NewManager(100 * time.Millisecond)
	defer m.Close()

	req := DeviceRegistrationRequest{
		DeviceName: "Test Device",
		Platform:   "ios",
		AppVersion: "1.0.0",
	}

	device, err := m.RegisterDevice(req)
	if err != nil {
		t.Fatalf("RegisterDevice() failed: %v", err)
	}

	deviceID := device.DeviceID()

	// Device should exist initially
	if m.GetDeviceCount() != 1 {
		t.Error("Device should exist initially")
	}

	// Wait for cleanup to run (timeout + cleanup interval)
	time.Sleep(200 * time.Millisecond)

	// Manually trigger cleanup
	m.cleanupInactiveDevices()

	// Device should be removed
	_, exists := m.GetDevice(deviceID)
	if exists {
		t.Error("Device should be removed after timeout")
	}
}

func TestManagerGetDevice(t *testing.T) {
	m := NewManager(30 * time.Second)
	defer m.Close()

	req := DeviceRegistrationRequest{
		DeviceName: "Test Device",
		Platform:   "android",
		AppVersion: "1.0.0",
	}

	registeredDevice, err := m.RegisterDevice(req)
	if err != nil {
		t.Fatalf("RegisterDevice() failed: %v", err)
	}

	deviceID := registeredDevice.DeviceID()

	// Get existing device
	device, exists := m.GetDevice(deviceID)
	if !exists {
		t.Error("GetDevice() should return true for existing device")
	}
	if device == nil {
		t.Error("GetDevice() returned nil device")
	}

	// Get non-existent device
	_, exists = m.GetDevice("non-existent")
	if exists {
		t.Error("GetDevice() should return false for non-existent device")
	}
}

func TestManagerClose(t *testing.T) {
	m := NewManager(30 * time.Second)

	// Register some devices
	for i := 0; i < 3; i++ {
		req := DeviceRegistrationRequest{
			DeviceName: "Device",
			Platform:   "ios",
			AppVersion: "1.0.0",
		}
		_, err := m.RegisterDevice(req)
		if err != nil {
			t.Fatalf("RegisterDevice() failed: %v", err)
		}
	}

	if m.GetDeviceCount() != 3 {
		t.Errorf("Should have 3 devices before close, got %d", m.GetDeviceCount())
	}

	// Close manager
	m.Close()

	// All devices should be removed
	if m.GetDeviceCount() != 0 {
		t.Errorf("Should have 0 devices after close, got %d", m.GetDeviceCount())
	}
}

// TestPlatformDescribesTheDeviceRatherThanAdmittingIt covers what the
// ios/android/web allowlist refused, including the bundled client's own default
// of "unknown" and the "node" its Node example sends.
func TestPlatformDescribesTheDeviceRatherThanAdmittingIt(t *testing.T) {
	m := NewManager(30 * time.Second)
	defer m.Close()

	for _, platform := range []string{"ios", "android", "web", "node", "unknown", "pn532-serial"} {
		device, err := m.RegisterDevice(DeviceRegistrationRequest{
			DeviceName: "Device",
			Platform:   platform,
		})
		if err != nil {
			t.Errorf("RegisterDevice(platform=%q) = %v, want it admitted", platform, err)
			continue
		}
		if device.Platform() != platform {
			t.Errorf("Platform() = %q, want %q reported back as sent", device.Platform(), platform)
		}
	}

	// Saying nothing is not a reason to refuse either.
	device, err := m.RegisterDevice(DeviceRegistrationRequest{DeviceName: "Quiet device"})
	if err != nil {
		t.Fatalf("RegisterDevice with no platform = %v, want it admitted", err)
	}
	if device.Platform() != "unknown" {
		t.Errorf("Platform() = %q for a device that sent none, want %q", device.Platform(), "unknown")
	}

	// A device name is still required: it names the row an operator sees.
	if _, err := m.RegisterDevice(DeviceRegistrationRequest{Platform: "ios"}); err == nil {
		t.Error("RegisterDevice with no device name was admitted, want an error")
	}
}
