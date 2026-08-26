package nfc

import (
	"fmt"
	"sync"
)

// MockDevice is a test implementation of Device that simulates NFC hardware.
//
// MockDevice allows testing NFC functionality without physical hardware by
// simulating device behavior, connection states, and data transmission.
// It also implements DeviceHealthChecker for health check simulation.
//
// Example:
//
//	mock := &MockDevice{
//	    DeviceName: "Mock NFC Reader",
//	    DeviceConnection: "mock:usb:001",
//	}
//	tags, err := mock.GetTags()
type MockDevice struct {
	// DeviceName is the simulated device name returned by String()
	DeviceName string

	// DeviceConnection is the simulated connection string returned by Connection()
	DeviceConnection string

	// IsOpen tracks whether the device is currently open
	IsOpen bool

	// InitError, if set, will be returned by IsHealthy()
	InitError error

	// CloseError, if set, will be returned by Close()
	CloseError error

	// TransceiveFunc allows custom transceive behavior for testing
	// If nil, returns TransceiveResponse or TransceiveError
	TransceiveFunc func([]byte) ([]byte, error)

	// TransceiveResponse is the default response for Transceive calls
	TransceiveResponse []byte

	// TransceiveError, if set, will be returned by Transceive()
	TransceiveError error

	// GetTagsFunc allows custom GetTags behavior for testing
	// If nil, returns Tags or GetTagsError
	GetTagsFunc func() ([]Tag, error)

	// Tags is the list of tags returned by GetTags()
	Tags []Tag

	// GetTagsError, if set, will be returned by GetTags()
	GetTagsError error

	// CallLog tracks all method calls for verification in tests
	CallLog []string

	// MockDeviceType allows overriding the device type (default: "mock")
	MockDeviceType string

	// MockSupportedTagTypes allows overriding supported tag types
	MockSupportedTagTypes []string

	// MockSupportsEvents makes the device report as event-based (like smartphone)
	MockSupportsEvents bool

	// SignalError, if set, will be returned by Signal()
	SignalError error

	// Signals records the signals passed to Signal(), in order
	Signals []Signal

	mu sync.Mutex
}

// NewMockDevice creates a new MockDevice with default values.
func NewMockDevice() *MockDevice {
	return &MockDevice{
		DeviceName:       "Mock NFC Reader",
		DeviceConnection: "mock:usb:001",
		IsOpen:           true,
		CallLog:          make([]string, 0),
	}
}

// Close simulates closing the device.
func (m *MockDevice) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = append(m.CallLog, "Close")

	if !m.IsOpen {
		return fmt.Errorf("device already closed")
	}

	m.IsOpen = false
	return m.CloseError
}

// IsHealthy checks if the mock device is healthy (implements DeviceHealthChecker).
func (m *MockDevice) IsHealthy() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = append(m.CallLog, "IsHealthy")

	if !m.IsOpen {
		return fmt.Errorf("device not open")
	}

	return m.InitError
}

// String returns the simulated device name.
func (m *MockDevice) String() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = append(m.CallLog, "String")
	return m.DeviceName
}

// Connection returns the simulated connection string.
func (m *MockDevice) Connection() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = append(m.CallLog, "Connection")
	return m.DeviceConnection
}

// SetConnection changes the simulated connection string. Taken under the
// device's own lock: a manager that hands the same device to a second reader
// writes this while the first one is polling it.
func (m *MockDevice) SetConnection(connection string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.DeviceConnection = connection
}

// DeviceType returns the device type (implements DeviceInfoProvider).
func (m *MockDevice) DeviceType() string {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = append(m.CallLog, "DeviceType")
	if m.MockDeviceType != "" {
		return m.MockDeviceType
	}
	return "mock"
}

// SupportedTagTypes returns the supported tag types (implements DeviceInfoProvider).
func (m *MockDevice) SupportedTagTypes() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = append(m.CallLog, "SupportedTagTypes")
	if m.MockSupportedTagTypes != nil {
		return m.MockSupportedTagTypes
	}
	return []string{"MIFARE Classic", "NTAG", "Type 4"}
}

// SupportsEvents returns whether this device emits events (implements DeviceEventEmitter).
func (m *MockDevice) SupportsEvents() bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = append(m.CallLog, "SupportsEvents")
	return m.MockSupportsEvents
}

// Signal records a reader feedback signal (implements FeedbackDevice).
func (m *MockDevice) Signal(s Signal) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = append(m.CallLog, fmt.Sprintf("Signal(%s)", s))

	if m.SignalError != nil {
		return m.SignalError
	}

	m.Signals = append(m.Signals, s)
	return nil
}

// GetSignals returns a copy of the recorded signals.
func (m *MockDevice) GetSignals() []Signal {
	m.mu.Lock()
	defer m.mu.Unlock()

	out := make([]Signal, len(m.Signals))
	copy(out, m.Signals)
	return out
}

// Transceive simulates data transmission with the device.
func (m *MockDevice) Transceive(txData []byte) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = append(m.CallLog, fmt.Sprintf("Transceive(%d bytes)", len(txData)))

	if !m.IsOpen {
		return nil, fmt.Errorf("device not open")
	}

	if m.TransceiveFunc != nil {
		return m.TransceiveFunc(txData)
	}

	if m.TransceiveError != nil {
		return nil, m.TransceiveError
	}

	return m.TransceiveResponse, nil
}

// GetCallLog returns a copy of the call log for verification.
func (m *MockDevice) GetCallLog() []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	logCopy := make([]string, len(m.CallLog))
	copy(logCopy, m.CallLog)
	return logCopy
}

// ClearCallLog clears the call log.
func (m *MockDevice) ClearCallLog() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = make([]string, 0)
}

// GetTags simulates detecting tags on the device.
func (m *MockDevice) GetTags() ([]Tag, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.CallLog = append(m.CallLog, "GetTags")

	if !m.IsOpen {
		return nil, fmt.Errorf("device not open")
	}

	if m.GetTagsFunc != nil {
		return m.GetTagsFunc()
	}

	if m.GetTagsError != nil {
		return nil, m.GetTagsError
	}

	// Return a copy to prevent external modification
	tagsCopy := make([]Tag, len(m.Tags))
	copy(tagsCopy, m.Tags)
	return tagsCopy, nil
}

// SetTags sets the tags that will be returned by GetTags().
func (m *MockDevice) SetTags(tags []Tag) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.Tags = tags
}

// AddTag adds a tag to the list returned by GetTags().
func (m *MockDevice) AddTag(tag Tag) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.Tags = append(m.Tags, tag)
}

// ClearTags removes all tags from the list returned by GetTags().
func (m *MockDevice) ClearTags() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.Tags = make([]Tag, 0)
}
