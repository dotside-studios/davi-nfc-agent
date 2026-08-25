package remotenfc

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/event"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/server/wsconn"
	"github.com/google/uuid"
)

// Manager implements nfc.Manager for phones and other networked devices, and
// serves the WebSocket endpoint they connect to. See Handler.
//
// It owns both the device registry and the sessions behind it, so a
// registration and its connection cannot outlive one another.
type Manager struct {
	devices           map[string]*Device // deviceID -> device
	mu                sync.RWMutex       // Protects devices and the policy fields
	cleanupTicker     *time.Ticker       // Periodic sweep for silent devices
	stopped           chan struct{}      // Closed by Close, ending the manager's goroutines
	inactivityTimeout time.Duration      // Device timeout duration
	closed            bool               // Whether Close() has been called
	dataChan          chan nfc.NFCData   // Scans, drained onto scans
	deviceChangeChan  chan struct{}      // Signals registration and unregistration

	// Policy supplied by the agent through Handler.
	publicKeyPin         func() string
	allowTagModification func() bool

	sessions    map[string]*wsconn.SafeConn // deviceID -> connection
	sessionConn map[*wsconn.SafeConn]string // reverse lookup
	sessionsMu  sync.RWMutex

	pending   map[string]pendingRequest // requestID -> waiter
	pendingMu sync.Mutex

	// activeLatest names the device that scanned most recently, for a request
	// that names none. The tags themselves live on the devices holding them.
	activeLatest string
	activeMu     sync.RWMutex

	// reqSeq labels each request to a device.
	reqSeq atomic.Uint64

	// scans is what subscribers connect to. The channel in front of it keeps a
	// device's read loop off the subscribers: a scan is buffered and dropped
	// here rather than at the socket.
	scans event.Signal[nfc.NFCData]
}

// NewManager creates a new smartphone manager.
func NewManager(inactivityTimeout time.Duration) *Manager {
	if inactivityTimeout == 0 {
		inactivityTimeout = DeviceTimeout
	}

	m := &Manager{
		devices:           make(map[string]*Device),
		inactivityTimeout: inactivityTimeout,
		stopped:           make(chan struct{}),
		dataChan:          make(chan nfc.NFCData, 10), // Buffered to prevent blocking
		deviceChangeChan:  make(chan struct{}, 1),     // Buffered to prevent blocking
		sessions:          make(map[string]*wsconn.SafeConn),
		sessionConn:       make(map[*wsconn.SafeConn]string),
		pending:           make(map[string]pendingRequest),
	}

	// Start cleanup routine
	m.startCleanupRoutine()
	go m.publishScans()

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
	// Platform describes the device rather than admitting it: nothing branches
	// on the value, and the bridge carries whatever speaks the protocol.
	if req.Platform == "" {
		req.Platform = "unknown"
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

	tag, err := convertTagData(tagData, m)
	if err != nil {
		return fmt.Errorf("failed to convert tag data: %w", err)
	}

	// Record before publishing: a client reacting to the broadcast with an
	// immediate write must find the device already holding the tag.
	m.setActiveTag(deviceID, tag.UID(), tag)

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

// Scans carries every tag the registered devices report.
func (m *Manager) Scans() *event.Signal[nfc.NFCData] { return &m.scans }

// publishScans hands what the devices reported to the subscribers. It runs on a
// goroutine of its own so a slow subscriber holds up neither the socket a scan
// arrived on nor the other devices.
func (m *Manager) publishScans() {
	for {
		select {
		case <-m.stopped:
			return
		case data := <-m.dataChan:
			m.scans.Emit(data)
		}
	}
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
	close(m.stopped)

	// Drop the sessions first so their serve loops exit; each unregisters the
	// device it owned on the way out.
	m.sessionsMu.Lock()
	conns := make([]*wsconn.SafeConn, 0, len(m.sessions))
	for _, conn := range m.sessions {
		conns = append(conns, conn)
	}
	m.sessionsMu.Unlock()

	for _, conn := range conns {
		_ = conn.Close()
	}

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
			case <-m.stopped:
				return
			}
		}
	}()
}

// cleanupInactiveDevices drops devices that have gone silent, which covers the
// half-open connection where the socket survives but heartbeats stop.
//
// A device that still has a session is reaped by closing it, so the ordinary
// disconnect path does the unregistering. Deleting the registration on its own
// would leave a live socket bound to a device the manager no longer knows.
func (m *Manager) cleanupInactiveDevices() {
	now := time.Now()

	m.mu.RLock()
	var stale []string
	for deviceID, device := range m.devices {
		if since := now.Sub(device.LastSeen()); since > m.inactivityTimeout {
			log.Printf("[smartphone] Device silent for %v, dropping: %s", since, device.String())
			stale = append(stale, deviceID)
		}
	}
	m.mu.RUnlock()

	// Outside the lock: closing a session runs the disconnect path, which takes
	// it to unregister.
	for _, deviceID := range stale {
		if m.closeSession(deviceID) {
			continue
		}
		if err := m.UnregisterDevice(deviceID); err != nil {
			log.Printf("[smartphone] Failed to unregister silent device %s: %v", deviceID, err)
		}
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

// RemoteDevices reports that this manager's devices are phones rather than
// readers attached to this machine, so none of them is a candidate to be the
// agent's own reader.
func (m *Manager) RemoteDevices() bool { return true }
