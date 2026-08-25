package remotenfc

import (
	"fmt"
	"sync"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// Device implements the nfc.Device interface for smartphone NFC scanning.
type Device struct {
	deviceID     string              // Unique ID for this smartphone (UUID)
	connection   string              // Connection info (e.g., "smartphone:uuid")
	deviceName   string              // Human-readable name (e.g., "iPhone 12 Pro")
	platform     string              // "ios" or "android"
	appVersion   string              // Mobile app version
	protoVersion int                 // Negotiated bridge protocol version
	isActive     bool                // Whether device is connected
	closeChannel chan struct{}       // Signal to close device
	mu           sync.RWMutex        // Protects device state
	lastSeen     time.Time           // Last activity timestamp (for health monitoring)
	capabilities *DeviceCapabilities // What it said it can do, nil if it said nothing
	tag          nfc.Tag             // The tag it is holding, nil when its field is empty
	metadata     map[string]string   // Additional device info
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
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.capabilities != nil && d.capabilities.DeviceType != "" {
		return d.capabilities.DeviceType
	}
	return "smartphone"
}

// SupportedTagTypes returns the NFC types this device supports (implements nfc.DeviceInfoProvider).
// A v0 device declares only its radio technology, which is all we can report.
func (d *Device) SupportedTagTypes() []string {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.capabilities == nil {
		return nil
	}
	if len(d.capabilities.SupportedTagTypes) > 0 {
		return append([]string(nil), d.capabilities.SupportedTagTypes...)
	}
	return []string{d.capabilities.NFCType}
}

// SupportsEvents returns true as smartphones emit tag events (implements nfc.DeviceEventEmitter).
func (d *Device) SupportsEvents() bool {
	return true
}

// SupportsTransceive reports device-level transceive, which remains
// unsupported (implements nfc.DeviceTransceiver).
//
// A device may well declare CanTransceive, but that capability is exercised
// against a specific tag and is reported by remotenfc.Tag. Device-level
// transceive has no tag to address, so declaring it here would promise
// something Transceive below cannot do.
func (d *Device) SupportsTransceive() bool {
	return false
}

// Transceive is not directly applicable for smartphones. Raw exchange with a
// scanned tag goes through remotenfc.Tag.Transceive.
func (d *Device) Transceive(txData []byte) ([]byte, error) {
	return nil, nfc.NewNotSupportedError("Transceive")
}

// GetTags satisfies nfc.Device and never returns a tag.
//
// A phone is not polled the way a reader is: it pushes its scans, and those
// reach the agent through Manager.Data. What a device is holding right now is a
// separate question, answered by the registry of held tags on the manager.
func (d *Device) GetTags() ([]nfc.Tag, error) {
	d.mu.RLock()
	active, tag := d.isActive, d.tag
	d.mu.RUnlock()

	if !active {
		return nil, nfc.ErrDeviceClosed
	}
	if tag != nil {
		return []nfc.Tag{tag}, nil
	}

	// Nothing in its field. The wait is what a poller expects of a reader with
	// no card, and keeps one that polls anyway from spinning; a close is still
	// reported as one.
	select {
	case <-time.After(GetTagsTimeout):
		return []nfc.Tag{}, nil
	case <-d.closeChannel:
		return nil, nfc.ErrDeviceClosed
	}
}

// setTag records the tag the device is holding, or clears it with nil.
func (d *Device) setTag(tag nfc.Tag) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.tag = tag
}

// heldTag reports the tag it is holding, without waiting.
func (d *Device) heldTag() nfc.Tag {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.tag
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

// PhoneCapabilities returns what the device declared, or the zero value if it
// declared nothing. Callers that must tell those apart use DeclaredCapabilities.
func (d *Device) PhoneCapabilities() DeviceCapabilities {
	caps, _ := d.DeclaredCapabilities()
	return caps
}

// DeclaredCapabilities returns what the device said it can do, and whether it
// said anything at all.
//
// The second result is the whole point: an omitted block and a block of falses
// are different claims, and only one of them is a refusal.
func (d *Device) DeclaredCapabilities() (DeviceCapabilities, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.capabilities == nil {
		return DeviceCapabilities{}, false
	}
	return *d.capabilities, true
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
