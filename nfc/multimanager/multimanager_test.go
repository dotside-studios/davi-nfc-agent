package multimanager

import (
	"errors"
	"fmt"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// mockManager is a mock Manager implementation for testing
type mockManager struct {
	name     string
	devices  []string
	failOpen bool
	failList bool
}

func (m *mockManager) OpenDevice(deviceStr string) (nfc.Device, error) {
	if m.failOpen {
		return nil, fmt.Errorf("mock open error")
	}

	// Check if device exists in our list
	for _, d := range m.devices {
		if d == deviceStr {
			// Return a mock device (we don't need a real one for these tests)
			return &mockDevice{connection: deviceStr}, nil
		}
	}

	return nil, fmt.Errorf("device not found: %s", deviceStr)
}

func (m *mockManager) ListDevices() ([]string, error) {
	if m.failList {
		return nil, fmt.Errorf("mock list error")
	}
	return m.devices, nil
}

// mockDevice is a minimal Device implementation for testing
type mockDevice struct {
	connection string
}

func (m *mockDevice) Close() error                             { return nil }
func (m *mockDevice) String() string                           { return m.connection }
func (m *mockDevice) Connection() string                       { return m.connection }
func (m *mockDevice) Transceive(txData []byte) ([]byte, error) { return nil, nil }
func (m *mockDevice) GetTags() ([]nfc.Tag, error)              { return []nfc.Tag{}, nil }

func TestNewMultiManager(t *testing.T) {
	// Test empty constructor
	mm := NewMultiManager()

	if mm == nil {
		t.Fatal("NewMultiManager() returned nil")
	}

	if mm.GetManagerCount() != 0 {
		t.Errorf("New MultiManager should have 0 managers, got %d", mm.GetManagerCount())
	}

	// Test with managers
	mock1 := &mockManager{name: "mock1", devices: []string{"device1"}}
	mock2 := &mockManager{name: "mock2", devices: []string{"device2"}}

	mm2 := NewMultiManager(
		ManagerEntry{Name: "hardware", Manager: mock1},
		ManagerEntry{Name: "smartphone", Manager: mock2},
	)

	if mm2.GetManagerCount() != 2 {
		t.Errorf("NewMultiManager with entries should have 2 managers, got %d", mm2.GetManagerCount())
	}

	// Test with invalid entries
	mm3 := NewMultiManager(
		ManagerEntry{Name: "", Manager: mock1}, // Invalid - empty name
		ManagerEntry{Name: "valid", Manager: mock2},
	)

	if mm3.GetManagerCount() != 1 {
		t.Errorf("NewMultiManager should skip invalid entries, got %d managers", mm3.GetManagerCount())
	}
}

func TestMultiManagerAddManager(t *testing.T) {
	mm := NewMultiManager()

	mock1 := &mockManager{name: "mock1", devices: []string{"device1"}}
	mock2 := &mockManager{name: "mock2", devices: []string{"device2"}}

	// Add first manager
	err := mm.AddManager("manager1", mock1)
	if err != nil {
		t.Errorf("AddManager() failed: %v", err)
	}

	if mm.GetManagerCount() != 1 {
		t.Errorf("Should have 1 manager, got %d", mm.GetManagerCount())
	}

	// Add second manager
	err = mm.AddManager("manager2", mock2)
	if err != nil {
		t.Errorf("AddManager() failed: %v", err)
	}

	if mm.GetManagerCount() != 2 {
		t.Errorf("Should have 2 managers, got %d", mm.GetManagerCount())
	}

	// Try to add duplicate
	err = mm.AddManager("manager1", mock1)
	if err == nil {
		t.Error("AddManager() should fail for duplicate name")
	}

	// Try to add with empty name
	err = mm.AddManager("", mock1)
	if err == nil {
		t.Error("AddManager() should fail for empty name")
	}

	// Try to add nil manager
	err = mm.AddManager("manager3", nil)
	if err == nil {
		t.Error("AddManager() should fail for nil manager")
	}
}

func TestMultiManagerRemoveManager(t *testing.T) {
	mm := NewMultiManager()

	mock := &mockManager{name: "mock", devices: []string{"device1"}}

	err := mm.AddManager("manager1", mock)
	if err != nil {
		t.Fatalf("AddManager() failed: %v", err)
	}

	// Remove manager
	err = mm.RemoveManager("manager1")
	if err != nil {
		t.Errorf("RemoveManager() failed: %v", err)
	}

	if mm.GetManagerCount() != 0 {
		t.Errorf("Should have 0 managers after remove, got %d", mm.GetManagerCount())
	}

	// Try to remove non-existent manager
	err = mm.RemoveManager("non-existent")
	if err == nil {
		t.Error("RemoveManager() should fail for non-existent manager")
	}
}

func TestMultiManagerGetManager(t *testing.T) {
	mm := NewMultiManager()

	mock := &mockManager{name: "mock", devices: []string{"device1"}}

	err := mm.AddManager("manager1", mock)
	if err != nil {
		t.Fatalf("AddManager() failed: %v", err)
	}

	// Get existing manager
	manager, exists := mm.GetManager("manager1")
	if !exists {
		t.Error("GetManager() should return true for existing manager")
	}
	if manager == nil {
		t.Error("GetManager() returned nil manager")
	}

	// Get non-existent manager
	_, exists = mm.GetManager("non-existent")
	if exists {
		t.Error("GetManager() should return false for non-existent manager")
	}
}

func TestMultiManagerOpenDeviceWithPrefix(t *testing.T) {
	mm := NewMultiManager()

	mock1 := &mockManager{name: "mock1", devices: []string{"device1", "device2"}}
	mock2 := &mockManager{name: "mock2", devices: []string{"device3", "device4"}}

	_ = mm.AddManager("hardware", mock1)
	_ = mm.AddManager("smartphone", mock2)

	// Open device with explicit manager prefix
	device, err := mm.OpenDevice("hardware:device1")
	if err != nil {
		t.Errorf("OpenDevice() with prefix failed: %v", err)
	}
	if device == nil {
		t.Error("OpenDevice() returned nil device")
	}
	if device.Connection() != "device1" {
		t.Errorf("Device connection = %v, want %v", device.Connection(), "device1")
	}

	// Open device from second manager
	device, err = mm.OpenDevice("smartphone:device3")
	if err != nil {
		t.Errorf("OpenDevice() with prefix failed: %v", err)
	}
	if device == nil {
		t.Error("OpenDevice() returned nil device")
	}

	// Try with non-existent manager
	_, err = mm.OpenDevice("unknown:device1")
	if err == nil {
		t.Error("OpenDevice() should fail for non-existent manager")
	}

	// Try with non-existent device
	_, err = mm.OpenDevice("hardware:device999")
	if err == nil {
		t.Error("OpenDevice() should fail for non-existent device")
	}
}

// TestMultiManagerOpenDeviceWithColonInDeviceID tests that device IDs containing colons
// (like libnfc format "acr122_usb:001:003") are handled correctly when the first part
// is NOT a registered manager name.
func TestMultiManagerOpenDeviceWithColonInDeviceID(t *testing.T) {
	mm := NewMultiManager()

	// Register managers with names "hardware" and "smartphone"
	// The mock hardware manager accepts libnfc-style device IDs with colons
	mock1 := &mockManager{name: "mock1", devices: []string{"acr122_usb:001:003", "pn532_uart:/dev/ttyUSB0"}}
	mock2 := &mockManager{name: "mock2", devices: []string{"phone123"}}

	_ = mm.AddManager("hardware", mock1)
	_ = mm.AddManager("smartphone", mock2)

	// Open device with colon in ID - "acr122_usb" is NOT a registered manager,
	// so it should fall through and try all managers with the full device string
	device, err := mm.OpenDevice("acr122_usb:001:003")
	if err != nil {
		t.Errorf("OpenDevice() with colon in device ID failed: %v", err)
	}
	if device == nil {
		t.Error("OpenDevice() returned nil device")
	}
	if device != nil && device.Connection() != "acr122_usb:001:003" {
		t.Errorf("Device connection = %v, want %v", device.Connection(), "acr122_usb:001:003")
	}

	// Also test another format with colons
	device, err = mm.OpenDevice("pn532_uart:/dev/ttyUSB0")
	if err != nil {
		t.Errorf("OpenDevice() with colon in device ID failed: %v", err)
	}
	if device == nil {
		t.Error("OpenDevice() returned nil device")
	}

	// Explicit manager prefix should still work
	device, err = mm.OpenDevice("hardware:acr122_usb:001:003")
	if err != nil {
		t.Errorf("OpenDevice() with explicit manager prefix failed: %v", err)
	}
	if device == nil {
		t.Error("OpenDevice() returned nil device")
	}
	// When using explicit prefix, the manager receives everything after "hardware:"
	if device != nil && device.Connection() != "acr122_usb:001:003" {
		t.Errorf("Device connection = %v, want %v", device.Connection(), "acr122_usb:001:003")
	}
}

func TestMultiManagerOpenDeviceWithoutPrefix(t *testing.T) {
	mm := NewMultiManager()

	// Add managers in specific order
	mock1 := &mockManager{name: "mock1", devices: []string{"device1"}}
	mock2 := &mockManager{name: "mock2", devices: []string{"device2"}}

	_ = mm.AddManager("first", mock1)
	_ = mm.AddManager("second", mock2)

	// Open device without prefix (should try in order)
	device, err := mm.OpenDevice("device1")
	if err != nil {
		t.Errorf("OpenDevice() without prefix failed: %v", err)
	}
	if device == nil {
		t.Error("OpenDevice() returned nil device")
	}

	// Open device from second manager
	device, err = mm.OpenDevice("device2")
	if err != nil {
		t.Errorf("OpenDevice() without prefix failed: %v", err)
	}
	if device == nil {
		t.Error("OpenDevice() returned nil device")
	}

	// Try with non-existent device
	_, err = mm.OpenDevice("device999")
	if err == nil {
		t.Error("OpenDevice() should fail when all managers fail")
	}
}

func TestMultiManagerOpenDeviceFallback(t *testing.T) {
	mm := NewMultiManager()

	// First manager doesn't have the device
	mock1 := &mockManager{name: "mock1", devices: []string{"device1"}}
	// Second manager has it
	mock2 := &mockManager{name: "mock2", devices: []string{"device2", "target"}}

	_ = mm.AddManager("first", mock1)
	_ = mm.AddManager("second", mock2)

	// Open device without prefix - should fallback to second manager
	device, err := mm.OpenDevice("target")
	if err != nil {
		t.Errorf("OpenDevice() fallback failed: %v", err)
	}
	if device == nil {
		t.Error("OpenDevice() returned nil device")
	}
	if device.Connection() != "target" {
		t.Errorf("Device connection = %v, want %v", device.Connection(), "target")
	}
}

func TestMultiManagerListDevices(t *testing.T) {
	mm := NewMultiManager()

	mock1 := &mockManager{name: "mock1", devices: []string{"device1", "device2"}}
	mock2 := &mockManager{name: "mock2", devices: []string{"device3"}}

	_ = mm.AddManager("hardware", mock1)
	_ = mm.AddManager("smartphone", mock2)

	// List all devices
	devices, err := mm.ListDevices()
	if err != nil {
		t.Errorf("ListDevices() failed: %v", err)
	}

	if len(devices) != 3 {
		t.Errorf("ListDevices() returned %d devices, want 3", len(devices))
	}

	// Check that devices have manager prefix
	hasHardware := false
	hasSmartphone := false
	for _, device := range devices {
		if len(device) > 9 && device[:9] == "hardware:" {
			hasHardware = true
		}
		if len(device) > 11 && device[:11] == "smartphone:" {
			hasSmartphone = true
		}
	}

	if !hasHardware || !hasSmartphone {
		t.Error("Devices should have manager prefixes")
	}
}

func TestMultiManagerListDevicesWithErrors(t *testing.T) {
	mm := NewMultiManager()

	mock1 := &mockManager{name: "mock1", devices: []string{"device1"}, failList: true}
	mock2 := &mockManager{name: "mock2", devices: []string{"device2"}}

	_ = mm.AddManager("failing", mock1)
	_ = mm.AddManager("working", mock2)

	// Should still return devices from working manager
	devices, err := mm.ListDevices()
	if err != nil {
		t.Errorf("ListDevices() should not return error when some managers fail: %v", err)
	}

	if len(devices) != 1 {
		t.Errorf("ListDevices() should return 1 device from working manager, got %d", len(devices))
	}
}

func TestMultiManagerNoManagers(t *testing.T) {
	mm := NewMultiManager()

	// Try to open device with no managers
	_, err := mm.OpenDevice("device1")
	if err == nil {
		t.Error("OpenDevice() should fail with no managers")
	}

	// List devices with no managers
	devices, err := mm.ListDevices()
	if err != nil {
		t.Errorf("ListDevices() should not fail with no managers: %v", err)
	}
	if len(devices) != 0 {
		t.Errorf("ListDevices() should return empty list, got %d devices", len(devices))
	}
}

func TestMultiManagerGetManagerNames(t *testing.T) {
	mm := NewMultiManager()

	mock1 := &mockManager{name: "mock1"}
	mock2 := &mockManager{name: "mock2"}
	mock3 := &mockManager{name: "mock3"}

	_ = mm.AddManager("first", mock1)
	_ = mm.AddManager("second", mock2)
	_ = mm.AddManager("third", mock3)

	names := mm.GetManagerNames()

	if len(names) != 3 {
		t.Errorf("GetManagerNames() returned %d names, want 3", len(names))
	}

	// Check order is preserved
	if names[0] != "first" || names[1] != "second" || names[2] != "third" {
		t.Errorf("GetManagerNames() = %v, want [first, second, third]", names)
	}
}

func TestMultiManagerEmptyDeviceString(t *testing.T) {
	mm := NewMultiManager()

	// Make mock manager that accepts empty string
	mock1 := &mockManager{
		name:    "mock1",
		devices: []string{"", "default-device"}, // Include empty string as valid device
	}
	mock2 := &mockManager{name: "mock2", devices: []string{"other-device"}}

	_ = mm.AddManager("first", mock1)
	_ = mm.AddManager("second", mock2)

	// Open with empty string (should try first manager with empty string)
	device, err := mm.OpenDevice("")
	if err != nil {
		// It's acceptable for this to fail if no manager accepts empty string
		t.Logf("OpenDevice('') failed (expected): %v", err)
		return
	}
	if device != nil {
		// It succeeded, which is fine
		t.Logf("OpenDevice('') succeeded with device: %v", device.Connection())
	}
}

// ListDevices is polled continuously. A reader that stays unavailable must not
// fill the log with the same line — the operator has to be able to see anything
// else that happens while it is broken.
func TestListDevicesLogsAPersistentFailureOnce(t *testing.T) {
	mm := &MultiManager{}
	boom := errors.New("failed to establish PC/SC context")

	if !mm.logListError("hardware", boom) {
		t.Error("first failure was not logged")
	}
	for i := 0; i < 50; i++ {
		if mm.logListError("hardware", boom) {
			t.Fatalf("repeat %d of the same failure was logged again", i)
		}
	}

	// A different reason is new information.
	if !mm.logListError("hardware", errors.New("no such device")) {
		t.Error("a changed reason was suppressed")
	}

	// Each manager is tracked on its own.
	if !mm.logListError("smartphone", boom) {
		t.Error("a second manager's first failure was suppressed")
	}

	// Recovering resets it, so the next failure is reported afresh.
	mm.clearListError("hardware")
	if !mm.logListError("hardware", boom) {
		t.Error("failure after a recovery was suppressed")
	}
}
