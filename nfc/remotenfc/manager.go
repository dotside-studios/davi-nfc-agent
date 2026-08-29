package remotenfc

import (
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/event"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/server/wsconn"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// Manager implements nfc.Manager for phones and other networked devices, and
// serves the WebSocket endpoint they connect to. See Handler.
//
// It owns both the device registry and the sessions behind it, so a
// registration and its connection cannot outlive one another.
type Manager struct {
	devices           map[string]*Device  // deviceID -> device
	mu                sync.RWMutex        // Protects devices and the policy fields
	cleanupTicker     *time.Ticker        // Periodic sweep for silent devices
	stopped           chan struct{}       // Closed by Close, ending the manager's goroutines
	inactivityTimeout time.Duration       // Device timeout duration
	closed            bool                // Whether Close() has been called
	dataChan          chan nfc.ScannedTag // Scans, drained onto scans
	deviceChangeChan  chan struct{}       // Signals registration and unregistration

	dropped        atomic.Uint64 // Scans and removals the queue could not take
	publishTimeout time.Duration // How long publish waits for room; ScanPublishTimeout

	// Policy supplied by the agent through Handler.
	publicKeyPin         func() string
	allowTagModification func() bool

	sessions    map[string]*wsconn.SafeConn // deviceID -> connection
	sessionConn map[*wsconn.SafeConn]string // reverse lookup
	sessionsMu  sync.RWMutex

	// registerMu serializes registration so the replace-old-then-install-new
	// sequence is atomic. The session check and the session install are separate
	// lock acquisitions; without this, two registrations for one identity can
	// each miss the other and leave both connections live and mapped to the one
	// device — orphaned sessions no operation can reach. Registration is not a hot
	// path, so a single lock across it is cheap.
	registerMu sync.Mutex

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
	scans event.Signal[nfc.ScannedTag]
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
		dataChan:          make(chan nfc.ScannedTag, ScanQueueDepth),
		deviceChangeChan:  make(chan struct{}, 1), // Buffered to prevent blocking
		publishTimeout:    ScanPublishTimeout,
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

// deviceTransport is what any device on the bridge can do: it reports its own
// scans rather than being opened and polled. What a device declares at
// registration refines it; see [Device.PhoneCapabilities].
var deviceTransport = nfc.DeviceCapabilities{
	SupportsEvents: true,
	DeviceType:     "smartphone",
}

// Devices lists the devices connected right now, by the identity each holds.
// An aggregate adds the prefix naming this manager.
func (m *Manager) Devices() ([]nfc.DeviceListing, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	listings := make([]nfc.DeviceListing, 0, len(m.devices))
	for deviceID, device := range m.devices {
		if device.IsActive() {
			listings = append(listings, nfc.DeviceListing{Path: deviceID, ID: deviceID, Capabilities: deviceTransport})
		}
	}

	return listings, nil
}

// RegisterDevice registers a device under an identity of this manager's own.
func (m *Manager) RegisterDevice(req DeviceRegistrationRequest) (*Device, error) {
	return m.registerDevice("", req)
}

// registerDevice registers a device under the identity it was admitted with,
// minting one when it was admitted under none. A paired device holds an
// identity already; a fresh one per connection cannot be matched to it.
func (m *Manager) registerDevice(deviceID string, req DeviceRegistrationRequest) (*Device, error) {
	// Validate request
	if req.DeviceName == "" {
		return nil, fmt.Errorf("device name is required")
	}
	// Platform describes the device rather than admitting it: nothing branches
	// on the value, and the bridge carries whatever speaks the protocol.
	if req.Platform == "" {
		req.Platform = "unknown"
	}

	if deviceID == "" {
		deviceID = uuid.New().String()
	}

	// Drop the session this one replaces here: the connection it belongs to
	// ends after this registration and would take the new session with it.
	if conn, ok := m.session(deviceID); ok {
		m.removeSession(deviceID)
		_ = conn.Close()
	}

	// Create device
	device := NewDevice(deviceID, req)

	// Register device
	m.mu.Lock()
	m.devices[deviceID] = device
	m.mu.Unlock()

	managerLog.Printf("Device registered: %s (%s, %s)", device.String(), req.Platform, req.AppVersion)

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
		managerFail.Printf("Error closing device %s: %v", deviceID, err)
	}

	managerLog.Printf("Device unregistered: %s", device.String())

	// Notify listeners
	m.notifyDeviceChange()

	return nil
}

// DisconnectDevice ends a device's live session, reporting whether there was one
// to end. The reason goes out as the WebSocket close reason, so the device can
// tell being turned away from losing its radio.
//
// Closing the socket is the whole teardown: the session goroutine's read fails
// and its deferred endSession unregisters the device, fails its pending requests
// and clears its active tag. Repeating that here would race it.
//
// Credentials are checked once, at the upgrade, so this is what makes a
// credential change reach a device that is already connected.
func (m *Manager) DisconnectDevice(deviceID, reason string) bool {
	conn, ok := m.session(deviceID)
	if !ok {
		return false
	}

	// Best effort: a device that has already gone away cannot be told why.
	_ = conn.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.ClosePolicyViolation, reason))
	_ = conn.Close()

	managerLog.Printf("Device session ended by the agent: %s (%s)", deviceID, reason)
	return true
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

	// The device is heard from whether or not the scan can be published: the
	// queue is not something it can see.
	device.UpdateLastSeen()

	return m.publish(nfc.ScannedTag{Device: deviceID, Tag: tag})
}

// publish hands a scan or a removal to the broadcast loop, waiting up to
// ScanPublishTimeout for room in the queue and returning an error if none comes.
//
// It used to discard on a full queue and return nil: the device was told it had
// succeeded and the only trace was a log line. The caller now gets a retryable
// error to send on.
func (m *Manager) publish(scanned nfc.ScannedTag) error {
	select {
	case m.dataChan <- scanned:
		return nil
	default:
	}

	timer := time.NewTimer(m.publishTimeout)
	defer timer.Stop()

	select {
	case m.dataChan <- scanned:
		return nil
	case <-m.stopped:
		return fmt.Errorf("manager closed")
	case <-timer.C:
		dropped := m.dropped.Add(1)
		managerWarn.Printf("Scan queue full for %s after %s, dropped (%d dropped since start)",
			scanned.Device, m.publishTimeout, dropped)
		return fmt.Errorf("scan queue full after %s: subscribers are not keeping up", m.publishTimeout)
	}
}

// Dropped counts the scans and removals that could not be published within
// ScanPublishTimeout since the manager started. A climbing number means
// subscribers are not keeping up and taps are being lost.
func (m *Manager) Dropped() uint64 { return m.dropped.Load() }

// Scans carries every tag the registered devices report, as reported. What is
// read off the tag is the supervisor's, not this driver's.
func (m *Manager) Scans() *event.Signal[nfc.ScannedTag] { return &m.scans }

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

	device.UpdateLastSeen()

	// A nil Tag signals removal; the UID says which tag it was.
	if err := m.publish(nfc.ScannedTag{Device: deviceID, Tag: nil, RemovedUID: data.UID}); err != nil {
		return err
	}

	managerLog.Printf("Tag removed: device=%s, UID=%s", deviceID, data.UID)
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
			managerFail.Printf("Error closing device %s: %v", deviceID, err)
		}
	}
	m.devices = make(map[string]*Device)
	m.mu.Unlock()

	managerLog.Printf("Manager closed")
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
			managerWarn.Printf("Device silent for %v, dropping: %s", since, device.String())
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
			managerFail.Printf("Failed to unregister silent device %s: %v", deviceID, err)
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
