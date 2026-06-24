package nfc

import "testing"

func TestInferTagCapabilities_MifareClassic1K(t *testing.T) {
	caps := InferTagCapabilities("MIFARE Classic 1K")

	if !caps.CanRead {
		t.Error("Expected CanRead to be true")
	}
	if !caps.CanWrite {
		t.Error("Expected CanWrite to be true")
	}
	if caps.CanTransceive {
		t.Error("Expected CanTransceive to be false for Classic")
	}
	if caps.CanLock {
		t.Error("Expected CanLock to be false for Classic (locking not implemented)")
	}
	if caps.TagFamily != "MIFARE Classic" {
		t.Errorf("TagFamily = %q, want %q", caps.TagFamily, "MIFARE Classic")
	}
	if caps.MemorySize != 1024 {
		t.Errorf("MemorySize = %d, want 1024", caps.MemorySize)
	}
	if !caps.SupportsCrypto {
		t.Error("Expected SupportsCrypto to be true")
	}
}

func TestInferTagCapabilities_MifareClassic4K(t *testing.T) {
	caps := InferTagCapabilities("MIFARE Classic 4K")

	if caps.MemorySize != 4096 {
		t.Errorf("MemorySize = %d, want 4096", caps.MemorySize)
	}
}

func TestInferTagCapabilities_DESFire(t *testing.T) {
	caps := InferTagCapabilities("DESFire EV1")

	if !caps.CanRead {
		t.Error("Expected CanRead to be true")
	}
	if !caps.CanWrite {
		t.Error("Expected CanWrite to be true")
	}
	if caps.CanTransceive {
		t.Error("Expected CanTransceive to be false for DESFire")
	}
	if caps.CanLock {
		t.Error("Expected CanLock to be false for DESFire (not implemented)")
	}
	if caps.TagFamily != "DESFire" {
		t.Errorf("TagFamily = %q, want %q", caps.TagFamily, "DESFire")
	}
	if !caps.SupportsCrypto {
		t.Error("Expected SupportsCrypto to be true")
	}
}

func TestInferTagCapabilities_Ultralight(t *testing.T) {
	caps := InferTagCapabilities("MIFARE Ultralight")

	if !caps.CanRead {
		t.Error("Expected CanRead to be true")
	}
	if !caps.CanWrite {
		t.Error("Expected CanWrite to be true")
	}
	if caps.MemorySize != 64 {
		t.Errorf("MemorySize = %d, want 64", caps.MemorySize)
	}
	if caps.TagFamily != "MIFARE Ultralight" {
		t.Errorf("TagFamily = %q, want %q", caps.TagFamily, "MIFARE Ultralight")
	}
}

func TestInferTagCapabilities_UltralightC(t *testing.T) {
	caps := InferTagCapabilities("MIFARE Ultralight C")

	if caps.MemorySize != 192 {
		t.Errorf("MemorySize = %d, want 192", caps.MemorySize)
	}
	if !caps.SupportsCrypto {
		t.Error("Expected SupportsCrypto to be true for Ultralight C")
	}
}

func TestInferTagCapabilities_NTAG215(t *testing.T) {
	caps := InferTagCapabilities("NTAG215")

	if !caps.CanRead {
		t.Error("Expected CanRead to be true")
	}
	if !caps.CanWrite {
		t.Error("Expected CanWrite to be true")
	}
	if !caps.CanLock {
		t.Error("Expected CanLock to be true")
	}
	if caps.TagFamily != "NTAG" {
		t.Errorf("TagFamily = %q, want %q", caps.TagFamily, "NTAG")
	}
	if caps.MemorySize != 540 {
		t.Errorf("MemorySize = %d, want 540", caps.MemorySize)
	}
	if caps.MaxNDEFSize != 504 {
		t.Errorf("MaxNDEFSize = %d, want 504", caps.MaxNDEFSize)
	}
}

func TestInferTagCapabilities_Type4(t *testing.T) {
	caps := InferTagCapabilities("Type4A")

	if !caps.CanRead {
		t.Error("Expected CanRead to be true")
	}
	if !caps.CanWrite {
		t.Error("Expected CanWrite to be true")
	}
	if !caps.CanTransceive {
		t.Error("Expected CanTransceive to be true for Type4")
	}
	if caps.TagFamily != "Type 4" {
		t.Errorf("TagFamily = %q, want %q", caps.TagFamily, "Type 4")
	}
}

func TestInferTagCapabilities_Unknown(t *testing.T) {
	caps := InferTagCapabilities("SomeUnknownTag")

	if !caps.CanRead {
		t.Error("Expected CanRead to be true (all tags can read)")
	}
	if caps.CanWrite {
		t.Error("Expected CanWrite to be false for unknown")
	}
	if caps.CanTransceive {
		t.Error("Expected CanTransceive to be false for unknown")
	}
	if caps.TagFamily != "Unknown" {
		t.Errorf("TagFamily = %q, want %q", caps.TagFamily, "Unknown")
	}
}

func TestInferTagCapabilities_CaseInsensitive(t *testing.T) {
	// Test that inference works regardless of case
	caps1 := InferTagCapabilities("mifare classic 1k")
	caps2 := InferTagCapabilities("MIFARE CLASSIC 1K")

	if caps1.TagFamily != caps2.TagFamily {
		t.Errorf("Case sensitivity issue: %q vs %q", caps1.TagFamily, caps2.TagFamily)
	}
	if caps1.MemorySize != caps2.MemorySize {
		t.Errorf("Case sensitivity issue: %d vs %d", caps1.MemorySize, caps2.MemorySize)
	}
}

func TestGetTagCapabilities_WithProvider(t *testing.T) {
	// Create a mock tag that implements TagCapabilityProvider
	mock := NewMockTag("04A1B2C3")
	mock.TagType = "MIFARE Classic 1K"

	// MockTag should implement TagCapabilityProvider after our updates
	// For now, test the fallback behavior
	caps := GetTagCapabilities(mock)

	if caps.TagFamily != "MIFARE Classic" {
		t.Errorf("TagFamily = %q, want %q", caps.TagFamily, "MIFARE Classic")
	}
}

func TestCanTagHelpers(t *testing.T) {
	mock := NewMockTag("04A1B2C3")

	// Test Type4 tag (supports transceive)
	mock.TagType = "Type4A"
	if !CanTagTransceive(mock) {
		t.Error("Expected CanTagTransceive to be true for Type4")
	}

	// Test Classic tag (no transceive)
	mock.TagType = "MIFARE Classic 1K"
	if CanTagTransceive(mock) {
		t.Error("Expected CanTagTransceive to be false for Classic")
	}

	if !CanTagWrite(mock) {
		t.Error("Expected CanTagWrite to be true for Classic")
	}

	if CanTagLock(mock) {
		t.Error("Expected CanTagLock to be false for Classic (locking not implemented)")
	}
}

type mockDeviceWithInfo struct {
	deviceType        string
	supportedTagTypes []string
	supportsEvents    bool
}

func (m *mockDeviceWithInfo) Close() error                         { return nil }
func (m *mockDeviceWithInfo) String() string                       { return "mock" }
func (m *mockDeviceWithInfo) Connection() string                   { return "mock:0" }
func (m *mockDeviceWithInfo) Transceive(tx []byte) ([]byte, error) { return nil, nil }
func (m *mockDeviceWithInfo) GetTags() ([]Tag, error)              { return nil, nil }
func (m *mockDeviceWithInfo) DeviceType() string                   { return m.deviceType }
func (m *mockDeviceWithInfo) SupportedTagTypes() []string          { return m.supportedTagTypes }
func (m *mockDeviceWithInfo) SupportsEvents() bool                 { return m.supportsEvents }

func TestGetDeviceCapabilities_WithInfoProvider(t *testing.T) {
	device := &mockDeviceWithInfo{
		deviceType:        "test",
		supportedTagTypes: []string{"MIFARE Classic", "NTAG"},
		supportsEvents:    false,
	}

	caps := GetDeviceCapabilities(device)

	if caps.DeviceType != "test" {
		t.Errorf("DeviceType = %q, want %q", caps.DeviceType, "test")
	}
	if len(caps.SupportedTagTypes) != 2 {
		t.Errorf("SupportedTagTypes length = %d, want 2", len(caps.SupportedTagTypes))
	}
	// Not event-based, so should support transceive and poll
	if !caps.CanTransceive {
		t.Error("Expected CanTransceive to be true for non-event device")
	}
	if !caps.CanPoll {
		t.Error("Expected CanPoll to be true for non-event device")
	}
}

func TestGetDeviceCapabilities_EventBasedDevice(t *testing.T) {
	device := &mockDeviceWithInfo{
		deviceType:        "smartphone",
		supportedTagTypes: []string{"ISO-DEP"},
		supportsEvents:    true,
	}

	caps := GetDeviceCapabilities(device)

	if caps.DeviceType != "smartphone" {
		t.Errorf("DeviceType = %q, want %q", caps.DeviceType, "smartphone")
	}
	if !caps.SupportsEvents {
		t.Error("Expected SupportsEvents to be true")
	}
	// Event-based devices don't poll or transceive directly
	if caps.CanTransceive {
		t.Error("Expected CanTransceive to be false for event-based device")
	}
	if caps.CanPoll {
		t.Error("Expected CanPoll to be false for event-based device")
	}
}

func TestGetDeviceCapabilities_Fallback(t *testing.T) {
	// MockDevice implements DeviceInfoProvider
	device := NewMockDevice()
	caps := GetDeviceCapabilities(device)

	// Should get mock device capabilities via BuildDeviceCapabilities
	if !caps.CanTransceive {
		t.Error("Expected CanTransceive to be true")
	}
	if !caps.CanPoll {
		t.Error("Expected CanPoll to be true")
	}
	if caps.DeviceType != "mock" {
		t.Errorf("DeviceType = %q, want %q", caps.DeviceType, "mock")
	}
}
