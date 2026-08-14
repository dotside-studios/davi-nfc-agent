package remotenfc

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/google/uuid"
)

// Manager implements the nfc.Manager interface for managing smartphone connections.
type Manager struct {
	devices           map[string]*Device // deviceID -> device
	mu                sync.RWMutex       // Protects devices map
	cleanupTicker     *time.Ticker       // Periodic cleanup of inactive devices
	stopCleanup       chan struct{}      // Stop cleanup goroutine
	inactivityTimeout time.Duration      // Device timeout duration
	closed            bool               // Whether Close() has been called
	dataChan          chan nfc.NFCData   // Channel for broadcasting tag data to server
	deviceChangeChan  chan struct{}      // Channel for device registration/unregistration events
	tagWriter         TagWriter          // Route for writes back to devices; nil until wired
}

// NewManager creates a new smartphone manager.
func NewManager(inactivityTimeout time.Duration) *Manager {
	if inactivityTimeout == 0 {
		inactivityTimeout = DeviceTimeout
	}

	m := &Manager{
		devices:           make(map[string]*Device),
		inactivityTimeout: inactivityTimeout,
		stopCleanup:       make(chan struct{}),
		dataChan:          make(chan nfc.NFCData, 10), // Buffered to prevent blocking
		deviceChangeChan:  make(chan struct{}, 1),     // Buffered to prevent blocking
	}

	// Start cleanup routine
	m.startCleanupRoutine()

	return m
}

// OpenDevice opens connection to a registered smartphone device by ID.
// Format: "smartphone:{deviceID}" or just "{deviceID}"
func (m *Manager) OpenDevice(deviceStr string) (nfc.Device, error) {
	// Parse device string
	deviceID := deviceStr
	if strings.HasPrefix(deviceStr, "smartphone:") {
		deviceID = strings.TrimPrefix(deviceStr, "smartphone:")
	}

	m.mu.RLock()
	device, exists := m.devices[deviceID]
	m.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("smartphone device not found: %s", deviceID)
	}

	if !device.IsActive() {
		return nil, fmt.Errorf("smartphone device is inactive: %s", deviceID)
	}

	return device, nil
}

// ListDevices returns list of connected smartphone device connection strings.
func (m *Manager) ListDevices() ([]string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	devices := make([]string, 0, len(m.devices))
	for deviceID, device := range m.devices {
		if device.IsActive() {
			devices = append(devices, fmt.Sprintf("smartphone:%s", deviceID))
		}
	}

	return devices, nil
}

// RegisterDevice creates and registers a new smartphone device.
func (m *Manager) RegisterDevice(req DeviceRegistrationRequest) (*Device, error) {
	// Validate request
	if req.DeviceName == "" {
		return nil, fmt.Errorf("device name is required")
	}
	if req.Platform != "ios" && req.Platform != "android" && req.Platform != "web" {
		return nil, fmt.Errorf("invalid platform: %s (must be 'ios', 'android', or 'web')", req.Platform)
	}

	// Generate unique device ID
	deviceID := uuid.New().String()

	// Create device
	device := NewDevice(deviceID, req)

	// Register device
	m.mu.Lock()
	m.devices[deviceID] = device
	m.mu.Unlock()

	log.Printf("[smartphone] Device registered: %s (%s, %s)", device.String(), req.Platform, req.AppVersion)

	// Notify listeners
	m.notifyDeviceChange()

	return device, nil
}

// UnregisterDevice removes a smartphone device.
func (m *Manager) UnregisterDevice(deviceID string) error {
	m.mu.Lock()
	device, exists := m.devices[deviceID]
	if exists {
		delete(m.devices, deviceID)
	}
	m.mu.Unlock()

	if !exists {
		return fmt.Errorf("device not found: %s", deviceID)
	}

	// Close the device
	if err := device.Close(); err != nil {
		log.Printf("[smartphone] Error closing device %s: %v", deviceID, err)
	}

	log.Printf("[smartphone] Device unregistered: %s", device.String())

	// Notify listeners
	m.notifyDeviceChange()

	return nil
}

// GetDevice retrieves a device by ID.
func (m *Manager) GetDevice(deviceID string) (*Device, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	device, exists := m.devices[deviceID]
	return device, exists
}

// SendTagData converts tag data and broadcasts it via the data channel.
func (m *Manager) SendTagData(deviceID string, tagData TagData) error {
	m.mu.RLock()
	device, exists := m.devices[deviceID]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("device not found: %s", deviceID)
	}

	// Convert tag data to internal format
	m.mu.RLock()
	writer := m.tagWriter
	m.mu.RUnlock()

	tag, err := ConvertTagDataWithWriter(tagData, writer)
	if err != nil {
		return fmt.Errorf("failed to convert tag data: %w", err)
	}

	// Create Card and broadcast via data channel
	card := nfc.NewCard(tag)
	select {
	case m.dataChan <- nfc.NFCData{Card: card, Err: nil}:
	default:
		log.Printf("[smartphone] Data channel full, dropping tag data for device %s", deviceID)
	}

	// Update heartbeat
	device.UpdateLastSeen()

	return nil
}

// SetTagWriter wires the route used to write and lock tags held by devices.
// Tags produced after this call can report and perform those operations.
func (m *Manager) SetTagWriter(writer TagWriter) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tagWriter = writer
}

// Data returns a channel that provides NFCData as tags are scanned.
func (m *Manager) Data() <-chan nfc.NFCData {
	return m.dataChan
}

// SendTagRemoved broadcasts a tag removal event via the data channel.
func (m *Manager) SendTagRemoved(deviceID string, data TagRemovedData) error {
	m.mu.RLock()
	device, exists := m.devices[deviceID]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("device not found: %s", deviceID)
	}

	// Broadcast removal via data channel (Card: nil signals removal)
	select {
	case m.dataChan <- nfc.NFCData{Card: nil, Err: nil}:
		log.Printf("[smartphone] Tag removed: device=%s, UID=%s", deviceID, data.UID)
	default:
		log.Printf("[smartphone] Data channel full, dropping tag removal for device %s", deviceID)
	}

	// Update heartbeat
	device.UpdateLastSeen()

	return nil
}

// UpdateHeartbeat updates device last-seen timestamp.
func (m *Manager) UpdateHeartbeat(deviceID string) error {
	m.mu.RLock()
	device, exists := m.devices[deviceID]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("device not found: %s", deviceID)
	}

	device.UpdateLastSeen()
	return nil
}

// Close cleanup and stop background tasks.
func (m *Manager) Close() {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	m.mu.Unlock()

	// Stop cleanup routine
	if m.cleanupTicker != nil {
		m.cleanupTicker.Stop()
	}
	close(m.stopCleanup)

	// Close all devices
	m.mu.Lock()
	for deviceID, device := range m.devices {
		if err := device.Close(); err != nil {
			log.Printf("[smartphone] Error closing device %s: %v", deviceID, err)
		}
	}
	m.devices = make(map[string]*Device)
	m.mu.Unlock()

	log.Printf("[smartphone] Manager closed")
}

// startCleanupRoutine starts a background goroutine to cleanup inactive devices.
func (m *Manager) startCleanupRoutine() {
	m.cleanupTicker = time.NewTicker(CleanupInterval)

	go func() {
		for {
			select {
			case <-m.cleanupTicker.C:
				m.cleanupInactiveDevices()
			case <-m.stopCleanup:
				return
			}
		}
	}()
}

// cleanupInactiveDevices removes devices that exceeded inactivity timeout.
func (m *Manager) cleanupInactiveDevices() {
	m.mu.Lock()

	now := time.Now()
	removedCount := 0
	for deviceID, device := range m.devices {
		timeSinceLastSeen := now.Sub(device.LastSeen())
		if timeSinceLastSeen > m.inactivityTimeout {
			log.Printf("[smartphone] Cleaning up inactive device: %s (last seen %v ago)", device.String(), timeSinceLastSeen)

			// Close and remove device
			if err := device.Close(); err != nil {
				log.Printf("[smartphone] Error closing device %s: %v", deviceID, err)
			}
			delete(m.devices, deviceID)
			removedCount++
		}
	}
	m.mu.Unlock()

	// Notify listeners if any devices were removed
	if removedCount > 0 {
		m.notifyDeviceChange()
	}
}

// GetDeviceCount returns the number of registered devices.
func (m *Manager) GetDeviceCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.devices)
}

// GetActiveDeviceCount returns the number of active devices.
func (m *Manager) GetActiveDeviceCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	count := 0
	for _, device := range m.devices {
		if device.IsActive() {
			count++
		}
	}
	return count
}

// DeviceChanges returns a channel that signals when devices are registered or unregistered.
func (m *Manager) DeviceChanges() <-chan struct{} {
	return m.deviceChangeChan
}

// notifyDeviceChange signals a device change event.
func (m *Manager) notifyDeviceChange() {
	select {
	case m.deviceChangeChan <- struct{}{}:
	default:
		// Channel full, skip (previous notification not yet consumed)
	}
}
