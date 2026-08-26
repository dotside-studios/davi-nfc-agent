package nfc

import (
	"fmt"
	"testing"
)

func TestMockManager_Devices(t *testing.T) {
	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001", "mock:usb:002", "mock:usb:003"}

	devices, err := manager.Devices()
	if err != nil {
		t.Errorf("Devices() failed: %v", err)
	}

	if len(devices) != 3 {
		t.Errorf("Expected 3 devices, got %d", len(devices))
	}

	expectedDevices := []string{"mock:usb:001", "mock:usb:002", "mock:usb:003"}
	for i, device := range devices {
		if device.Path != expectedDevices[i] {
			t.Errorf("Expected device %d to be '%s', got '%s'", i, expectedDevices[i], device.Path)
		}
		if !device.Capabilities.CanPoll {
			t.Errorf("device %d is not offered as a reader", i)
		}
	}
}

func TestMockManager_DevicesError(t *testing.T) {
	manager := NewMockManager()
	expectedErr := fmt.Errorf("no devices found")
	manager.DevicesError = expectedErr

	_, err := manager.Devices()
	if err != expectedErr {
		t.Errorf("Expected error '%v', got '%v'", expectedErr, err)
	}
}

func TestMockManager_OpenDevice(t *testing.T) {
	manager := NewMockManager()

	device, err := manager.OpenDevice("mock:usb:001")
	if err != nil {
		t.Errorf("OpenDevice() failed: %v", err)
	}

	if device == nil {
		t.Error("Expected device to be non-nil")
	}

	// Verify the device has the correct connection string
	if conn := device.Connection(); conn != "mock:usb:001" {
		t.Errorf("Expected connection 'mock:usb:001', got '%s'", conn)
	}
}

func TestMockManager_OpenDeviceError(t *testing.T) {
	manager := NewMockManager()
	expectedErr := fmt.Errorf("device busy")
	manager.OpenDeviceError = expectedErr

	_, err := manager.OpenDevice("mock:usb:001")
	if err != expectedErr {
		t.Errorf("Expected error '%v', got '%v'", expectedErr, err)
	}
}

func TestMockDevice_GetTags(t *testing.T) {
	device := NewMockDevice()

	// Add some mock tags
	tag1 := NewMockTag("04A1B2C3")
	tag2 := NewMockTag("04D5E6F7")
	device.SetTags([]Tag{tag1, tag2})

	tags, err := device.GetTags()
	if err != nil {
		t.Errorf("GetTags() failed: %v", err)
	}

	if len(tags) != 2 {
		t.Errorf("Expected 2 tags, got %d", len(tags))
	}
}

func TestMockDevice_GetTagsError(t *testing.T) {
	device := NewMockDevice()
	expectedErr := fmt.Errorf("no tags detected")
	device.GetTagsError = expectedErr

	_, err := device.GetTags()
	if err != expectedErr {
		t.Errorf("Expected error '%v', got '%v'", expectedErr, err)
	}
}

func TestMockDevice_AddAndClearTags(t *testing.T) {
	device := NewMockDevice()

	// Initially should have no tags
	tags, _ := device.GetTags()
	if len(tags) != 0 {
		t.Errorf("Expected 0 tags initially, got %d", len(tags))
	}

	// Add a tag
	tag := NewMockTag("04A1B2C3")
	device.AddTag(tag)

	tags, _ = device.GetTags()
	if len(tags) != 1 {
		t.Errorf("Expected 1 tag after adding, got %d", len(tags))
	}

	// Clear tags
	device.ClearTags()
	tags, _ = device.GetTags()
	if len(tags) != 0 {
		t.Errorf("Expected 0 tags after clearing, got %d", len(tags))
	}
}

func TestMockManager_CallLog(t *testing.T) {
	manager := NewMockManager()
	manager.ClearCallLog()

	_, _ = manager.Devices()
	device, _ := manager.OpenDevice("mock:usb:001")

	callLog := manager.GetCallLog()
	expectedCalls := []string{
		"Devices",
		"OpenDevice(mock:usb:001)",
	}

	if len(callLog) != len(expectedCalls) {
		t.Errorf("Expected %d calls, got %d", len(expectedCalls), len(callLog))
	}

	for i, call := range expectedCalls {
		if i >= len(callLog) {
			break
		}
		if callLog[i] != call {
			t.Errorf("Expected call %d to be '%s', got '%s'", i, call, callLog[i])
		}
	}

	// Test device call log
	mockDevice, ok := device.(*MockDevice)
	if !ok {
		t.Fatal("Expected device to be MockDevice")
	}
	mockDevice.ClearCallLog()
	_, _ = mockDevice.GetTags()

	deviceCallLog := mockDevice.GetCallLog()
	if len(deviceCallLog) != 1 || deviceCallLog[0] != "GetTags" {
		t.Errorf("Expected device call log to contain 'GetTags', got %v", deviceCallLog)
	}
}
