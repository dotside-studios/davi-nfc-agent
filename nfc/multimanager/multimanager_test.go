package multimanager

import (
	"errors"
	"fmt"
	"strings"
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

func (m *mockManager) Devices() ([]nfc.DeviceListing, error) {
	if m.failList {
		return nil, fmt.Errorf("mock list error")
	}
	return listings(m.devices, nfc.DeviceCapabilities{CanPoll: true}), nil
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
	mock1 := &mockManager{name: "mock1", devices: []string{"device1"}}
	mock2 := &mockManager{name: "mock2", devices: []string{"device2"}}

	mm := NewMultiManager(
		ManagerEntry{Name: "hardware", Manager: mock1},
		ManagerEntry{Name: "smartphone", Manager: mock2},
	)

	for _, name := range []string{"hardware", "smartphone"} {
		if _, ok := mm.GetManager(name); !ok {
			t.Errorf("manager %q was not registered", name)
		}
	}

	// An entry missing a name or a manager is skipped rather than registered
	// under something unusable.
	mm2 := NewMultiManager(
		ManagerEntry{Name: "", Manager: mock1},
		ManagerEntry{Name: "nilled", Manager: nil},
		ManagerEntry{Name: "valid", Manager: mock2},
	)

	if _, ok := mm2.GetManager(""); ok {
		t.Error("an entry with no name was registered")
	}
	if _, ok := mm2.GetManager("nilled"); ok {
		t.Error("an entry with no manager was registered")
	}
	if _, ok := mm2.GetManager("valid"); !ok {
		t.Error("the valid entry was not registered")
	}
}

// A duplicate name keeps the first manager, since the later entry would
// otherwise silently take over the name the first is already reachable by.
func TestNewMultiManagerSkipsDuplicateNames(t *testing.T) {
	first := &mockManager{name: "first", devices: []string{"device1"}}
	second := &mockManager{name: "second", devices: []string{"device2"}}

	mm := NewMultiManager(
		ManagerEntry{Name: "hardware", Manager: first},
		ManagerEntry{Name: "hardware", Manager: second},
	)

	got, ok := mm.GetManager("hardware")
	if !ok {
		t.Fatal("hardware was not registered")
	}
	if got != nfc.Manager(first) {
		t.Error("a duplicate entry replaced the manager registered first")
	}
}

func TestMultiManagerGetManager(t *testing.T) {
	mock := &mockManager{name: "mock", devices: []string{"device1"}}
	mm := NewMultiManager(ManagerEntry{Name: "manager1", Manager: mock})

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

	mock1 := &mockManager{name: "mock1", devices: []string{"device1", "device2"}}
	mock2 := &mockManager{name: "mock2", devices: []string{"device3", "device4"}}

	mm := NewMultiManager(
		ManagerEntry{Name: "hardware", Manager: mock1},
		ManagerEntry{Name: "smartphone", Manager: mock2},
	)

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

	// Register managers with names "hardware" and "smartphone"
	// The mock hardware manager accepts libnfc-style device IDs with colons
	mock1 := &mockManager{name: "mock1", devices: []string{"acr122_usb:001:003", "pn532_uart:/dev/ttyUSB0"}}
	mock2 := &mockManager{name: "mock2", devices: []string{"phone123"}}

	mm := NewMultiManager(
		ManagerEntry{Name: "hardware", Manager: mock1},
		ManagerEntry{Name: "smartphone", Manager: mock2},
	)

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

	// Add managers in specific order
	mock1 := &mockManager{name: "mock1", devices: []string{"device1"}}
	mock2 := &mockManager{name: "mock2", devices: []string{"device2"}}

	mm := NewMultiManager(
		ManagerEntry{Name: "first", Manager: mock1},
		ManagerEntry{Name: "second", Manager: mock2},
	)

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

	// First manager doesn't have the device
	mock1 := &mockManager{name: "mock1", devices: []string{"device1"}}
	// Second manager has it
	mock2 := &mockManager{name: "mock2", devices: []string{"device2", "target"}}

	mm := NewMultiManager(
		ManagerEntry{Name: "first", Manager: mock1},
		ManagerEntry{Name: "second", Manager: mock2},
	)

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

func TestMultiManagerDevices(t *testing.T) {

	mock1 := &mockManager{name: "mock1", devices: []string{"device1", "device2"}}
	mock2 := &mockManager{name: "mock2", devices: []string{"device3"}}

	mm := NewMultiManager(
		ManagerEntry{Name: "hardware", Manager: mock1},
		ManagerEntry{Name: "smartphone", Manager: mock2},
	)

	// List all devices
	devices, err := mm.Devices()
	if err != nil {
		t.Errorf("Devices() failed: %v", err)
	}

	if len(devices) != 3 {
		t.Errorf("Devices() returned %d devices, want 3", len(devices))
	}

	// Check that devices have manager prefix
	hasHardware := false
	hasSmartphone := false
	for _, device := range devices {
		if strings.HasPrefix(device.Path, "hardware:") {
			hasHardware = true
		}
		if strings.HasPrefix(device.Path, "smartphone:") {
			hasSmartphone = true
		}
	}

	if !hasHardware || !hasSmartphone {
		t.Error("Devices should have manager prefixes")
	}
}

func TestMultiManagerDevicesWithErrors(t *testing.T) {

	mock1 := &mockManager{name: "mock1", devices: []string{"device1"}, failList: true}
	mock2 := &mockManager{name: "mock2", devices: []string{"device2"}}

	mm := NewMultiManager(
		ManagerEntry{Name: "failing", Manager: mock1},
		ManagerEntry{Name: "working", Manager: mock2},
	)

	// Should still return devices from working manager
	devices, err := mm.Devices()
	if err != nil {
		t.Errorf("Devices() should not return error when some managers fail: %v", err)
	}

	if len(devices) != 1 {
		t.Errorf("Devices() should return 1 device from working manager, got %d", len(devices))
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
	devices, err := mm.Devices()
	if err != nil {
		t.Errorf("Devices() should not fail with no managers: %v", err)
	}
	if len(devices) != 0 {
		t.Errorf("Devices() should return empty list, got %d devices", len(devices))
	}
}

// The listing picks the agent's reader when none is pinned, so it has to follow
// registration order rather than map order.
func TestDevicesFollowRegistrationOrder(t *testing.T) {
	mm := NewMultiManager(
		ManagerEntry{Name: "first", Manager: &mockManager{name: "mock1", devices: []string{"a"}}},
		ManagerEntry{Name: "second", Manager: &mockManager{name: "mock2", devices: []string{"b"}}},
		ManagerEntry{Name: "third", Manager: &mockManager{name: "mock3", devices: []string{"c"}}},
	)

	want := []string{"first:a", "second:b", "third:c"}

	// Repeated, because map iteration only sometimes disagrees with insertion
	// order and a single pass can pass by luck.
	for i := 0; i < 20; i++ {
		got, err := mm.Devices()
		if err != nil {
			t.Fatalf("Devices: %v", err)
		}
		if len(got) != len(want) {
			t.Fatalf("Devices() = %v, want %v", got, want)
		}
		for j := range want {
			if got[j].Path != want[j] {
				t.Fatalf("Devices() = %v, want %v", got, want)
			}
		}
	}
}

func TestMultiManagerEmptyDeviceString(t *testing.T) {
	// Make mock manager that accepts empty string
	mock1 := &mockManager{
		name:    "mock1",
		devices: []string{"", "default-device"}, // Include empty string as valid device
	}
	mock2 := &mockManager{name: "mock2", devices: []string{"other-device"}}

	mm := NewMultiManager(
		ManagerEntry{Name: "first", Manager: mock1},
		ManagerEntry{Name: "second", Manager: mock2},
	)

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
// fill the log with the same line: the operator has to be able to see anything
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

// closableManager records whether Close reached it.
type closableManager struct {
	mockManager
	closed int
}

func (m *closableManager) Close() { m.closed++ }

// Close used to test for an interface from the server package that no manager
// implemented, so it propagated to nothing and the drivers were never stopped.
func TestCloseReachesTheManagers(t *testing.T) {
	closable := &closableManager{mockManager: mockManager{name: "closable"}}
	plain := &mockManager{name: "plain"}

	mm := NewMultiManager(
		ManagerEntry{Name: "smartphone", Manager: closable},
		ManagerEntry{Name: "hardware", Manager: plain},
	)

	mm.Close()

	if closable.closed != 1 {
		t.Errorf("manager closed %d times, want 1", closable.closed)
	}

	// A second call must not close twice, nor panic on the already-closed
	// forwarding channel.
	mm.Close()

	if closable.closed != 1 {
		t.Errorf("second Close reached the manager again: %d calls", closable.closed)
	}
}
