package remotenfc

import (
	"fmt"
	"sync"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// Device implements the nfc.Device interface for smartphone NFC scanning.
type Device struct {
	deviceID     string             // Unique ID for this smartphone (UUID)
	connection   string             // Connection info (e.g., "smartphone:uuid")
	deviceName   string             // Human-readable name (e.g., "iPhone 12 Pro")
	platform     string             // "ios" or "android"
	appVersion   string             // Mobile app version
	protoVersion int                // Negotiated bridge protocol version
	isActive     bool               // Whether device is connected
	tagChannel   chan []nfc.Tag     // Channel to receive tags from smartphone
	closeChannel chan struct{}      // Signal to close device
	mu           sync.RWMutex       // Protects device state
	lastSeen     time.Time          // Last activity timestamp (for health monitoring)
	capabilities DeviceCapabilities // Read/write capabilities
	metadata     map[string]string  // Additional device info
}

// NewDevice creates a new smartphone device instance.
func NewDevice(deviceID string, req DeviceRegistrationRequest) *Device {
	return &Device{
		deviceID:     deviceID,
		connection:   fmt.Sprintf("smartphone:%s", deviceID),
		deviceName:   req.DeviceName,
		platform:     req.Platform,
		appVersion:   req.AppVersion,
		protoVersion: req.ProtocolVersion,
		isActive:     true,
		tagChannel:   make(chan []nfc.Tag, TagChannelBuffer),
		closeChannel: make(chan struct{}),
		lastSeen:     time.Now(),
		capabilities: req.Capabilities,
		metadata:     req.Metadata,
	}
}

// Close closes the device connection.
func (d *Device) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !d.isActive {
		return nil
	}

	d.isActive = false
	close(d.closeChannel)
	close(d.tagChannel)

	return nil
}

// IsHealthy checks if the device connection is healthy (implements nfc.DeviceHealthChecker).
func (d *Device) IsHealthy() error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if !d.isActive {
		return fmt.Errorf("device is not active")
	}

	// Check if device has timed out
	timeSinceLastSeen := time.Since(d.lastSeen)
	if timeSinceLastSeen > DeviceTimeout {
		return fmt.Errorf("device timeout: last seen %v ago", timeSinceLastSeen)
	}

	return nil
}

// String returns a human-readable device name.
func (d *Device) String() string {
	return fmt.Sprintf("%s [%s]", d.deviceName, d.connection)
}

// Connection returns the device connection string.
func (d *Device) Connection() string {
	return d.connection
}

// DeviceType returns the device type identifier (implements nfc.DeviceInfoProvider).
func (d *Device) DeviceType() string {
	return "smartphone"
}

// SupportedTagTypes returns the NFC types this device supports (implements nfc.DeviceInfoProvider).
func (d *Device) SupportedTagTypes() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return []string{d.capabilities.NFCType}
}

// SupportsEvents returns true as smartphones emit tag events (implements nfc.DeviceEventEmitter).
func (d *Device) SupportsEvents() bool {
	return true
}

// Transceive is not directly applicable for smartphones.
func (d *Device) Transceive(txData []byte) ([]byte, error) {
	return nil, nfc.NewNotSupportedError("Transceive")
}

// GetTags returns tags from the tag channel with timeout.
// Returns empty slice (not error) on timeout to maintain compatibility with polling loop.
func (d *Device) GetTags() ([]nfc.Tag, error) {
	d.mu.RLock()
	if !d.isActive {
		d.mu.RUnlock()
		return nil, fmt.Errorf("device is closed")
	}
	d.mu.RUnlock()

	// Wait for tags with timeout
	select {
	case tags, ok := <-d.tagChannel:
		if !ok {
			// Channel closed
			return nil, fmt.Errorf("device is closed")
		}
		return tags, nil
	case <-time.After(GetTagsTimeout):
		// Timeout - return empty slice (not error) for compatibility
		return []nfc.Tag{}, nil
	case <-d.closeChannel:
		return nil, fmt.Errorf("device is closed")
	}
}

// SendTags sends tags to the device's tag channel (called by manager).
func (d *Device) SendTags(tags []nfc.Tag) error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if !d.isActive {
		return fmt.Errorf("device is not active")
	}

	// Non-blocking send
	select {
	case d.tagChannel <- tags:
		return nil
	default:
		// Channel full - skip this update
		return fmt.Errorf("tag channel full, skipping update")
	}
}

// UpdateLastSeen updates the device's last activity timestamp.
func (d *Device) UpdateLastSeen() {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.lastSeen = time.Now()
}

// IsActive returns whether the device is currently active.
func (d *Device) IsActive() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.isActive
}

// LastSeen returns the last activity timestamp.
func (d *Device) LastSeen() time.Time {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.lastSeen
}

// DeviceID returns the device's unique identifier.
func (d *Device) DeviceID() string {
	return d.deviceID
}

// Platform returns the device platform ("ios" or "android").
func (d *Device) Platform() string {
	return d.platform
}

// AppVersion returns the mobile app version.
func (d *Device) AppVersion() string {
	return d.appVersion
}

// ProtocolVersion returns the bridge protocol version negotiated at registration.
func (d *Device) ProtocolVersion() int {
	return d.protoVersion
}

// PhoneCapabilities returns the smartphone-specific device capabilities.
func (d *Device) PhoneCapabilities() DeviceCapabilities {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.capabilities
}

// Metadata returns additional device metadata.
func (d *Device) Metadata() map[string]string {
	d.mu.RLock()
	defer d.mu.RUnlock()
	// Return a copy to prevent external modification
	metadataCopy := make(map[string]string)
	for k, v := range d.metadata {
		metadataCopy[k] = v
	}
	return metadataCopy
}
