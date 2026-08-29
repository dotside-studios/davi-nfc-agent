package nfc

import (
	"fmt"
	"math"
	"sync"
	"time"
)

// DeviceEventType categorizes device lifecycle events
type DeviceEventType int

const (
	// DeviceConnected indicates successful device connection
	DeviceConnected DeviceEventType = iota

	// DeviceDisconnected indicates device was disconnected
	DeviceDisconnected

	// DeviceReconnecting indicates an automatic reconnection attempt is starting
	DeviceReconnecting

	// DeviceReconnectFailed indicates a reconnection attempt failed
	DeviceReconnectFailed

	// CooldownStarted indicates device entered cooldown period
	CooldownStarted

	// CooldownEnded indicates cooldown period completed
	CooldownEnded

	// DeviceError indicates a recoverable device error occurred
	DeviceError
)

// String returns the event type as a string
func (et DeviceEventType) String() string {
	switch et {
	case DeviceConnected:
		return "DeviceConnected"
	case DeviceDisconnected:
		return "DeviceDisconnected"
	case DeviceReconnecting:
		return "DeviceReconnecting"
	case DeviceReconnectFailed:
		return "DeviceReconnectFailed"
	case CooldownStarted:
		return "CooldownStarted"
	case CooldownEnded:
		return "CooldownEnded"
	case DeviceError:
		return "DeviceError"
	default:
		return fmt.Sprintf("Unknown(%d)", et)
	}
}

// DeviceEvent represents a device lifecycle event
type DeviceEvent struct {
	Type      DeviceEventType
	Timestamp time.Time
	Device    Device // nil if disconnected
	Message   string // Human-readable description
	Err       error  // Associated error, if any
}

// DeviceManager handles device lifecycle, connection management, and reconnection logic.
// It maintains a connection to a single NFC device and handles recovery from errors.
type DeviceManager struct {
	manager    Manager
	device     Device
	devicePath string
	hasDevice  bool

	// assignedPath is the lane this reader was created to drive. Clearing the
	// working devicePath after an unrecoverable error resets to this, not to
	// empty: a reader that owns a specific reader must reconnect to that reader,
	// never fall back to auto-discovering and adopt a different lane's device. It
	// is empty only for a standalone reader created with no path, where clearing
	// to empty is what enables auto-discovery.
	assignedPath string

	// Reconnection state
	retryCount    int // Tracks retry attempts for timeout/closed errors
	inCooldown    bool
	cooldownTimer Timer // Timer interface for testability
	clock         Clock // Clock abstraction for time operations

	// Event broadcasting
	events   chan DeviceEvent // Buffered channel for device events
	eventMux sync.RWMutex     // Protects event channel

	// lastErr holds the last error reported here, so a condition that persists
	// across polls is logged once rather than on every one. HandleError is
	// reached from the poll loop, which runs continuously, so a standing fault
	// would otherwise be the only thing in the log.
	lastErr string

	// Status tracking
	mu sync.RWMutex
}

// NewDeviceManager creates a new DeviceManager for managing an NFC device connection.
// If clock is nil, a RealClock is used by default.
func NewDeviceManager(manager Manager, devicePath string, clock Clock) *DeviceManager {
	if clock == nil {
		clock = &RealClock{}
	}

	timer := clock.NewTimer(0)
	// Drain the timer to ensure it's stopped
	if !timer.Stop() {
		select {
		case <-timer.C():
		default:
		}
	}

	return &DeviceManager{
		manager:       manager,
		devicePath:    devicePath,
		assignedPath:  devicePath,
		hasDevice:     false,
		clock:         clock,
		cooldownTimer: timer,
		events:        make(chan DeviceEvent, 10), // Buffered to prevent blocking
	}
}

// Events returns a read-only channel for device lifecycle events.
func (dm *DeviceManager) Events() <-chan DeviceEvent {
	dm.eventMux.RLock()
	defer dm.eventMux.RUnlock()
	return dm.events
}

// emitEvent sends an event to the event channel without blocking.
// If the channel is full, the event is dropped with a warning log.
func (dm *DeviceManager) emitEvent(eventType DeviceEventType, message string, err error) {
	dm.eventMux.RLock()
	eventChan := dm.events
	dm.eventMux.RUnlock()

	if eventChan == nil {
		return
	}

	dm.mu.RLock()
	device := dm.device // May be nil
	dm.mu.RUnlock()

	event := DeviceEvent{
		Type:      eventType,
		Timestamp: dm.clock.Now(),
		Device:    device,
		Message:   message,
		Err:       err,
	}

	select {
	case eventChan <- event:
		readerLog.Printf("Device event emitted: %s - %s", eventType, message)
	default:
		readerWarn.Printf("Warning: Device event channel full, dropping event: %s", eventType)
	}
}

// Device returns the current active device, or nil if not connected.
func (dm *DeviceManager) Device() Device {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	return dm.device
}

// HasDevice returns true if a device is currently connected.
func (dm *DeviceManager) HasDevice() bool {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	return dm.hasDevice
}

// InCooldown returns true if the device manager is in a cooldown period.
func (dm *DeviceManager) InCooldown() bool {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	return dm.inCooldown
}

// DevicePath returns the path of the device being managed.
func (dm *DeviceManager) DevicePath() string {
	dm.mu.RLock()
	defer dm.mu.RUnlock()
	return dm.devicePath
}

// SetDevicePath sets the device path to use for connections.
func (dm *DeviceManager) SetDevicePath(path string) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	dm.devicePath = path
}

// Manager returns the underlying NFC manager for device discovery.
func (dm *DeviceManager) Manager() Manager {
	return dm.manager
}

// TryConnect attempts to connect to the device. If the device is already connected
// and responsive, it returns nil. Otherwise, it attempts to open and initialize the device.
func (dm *DeviceManager) TryConnect() error {
	dm.mu.Lock()
	hasDev := dm.hasDevice
	currentDevice := dm.device
	dm.mu.Unlock()

	if hasDev && currentDevice != nil {
		// Quick check if device is responsive (if it supports health checking)
		if checker, ok := currentDevice.(DeviceHealthChecker); ok {
			if healthErr := checker.IsHealthy(); healthErr == nil {
				readerLog.Println("Device already connected and responsive.")
				return nil
			} else {
				readerWarn.Printf("Device was marked connected, but health check failed: %v. Attempting full reconnect.", healthErr)
			}
		} else {
			// Device doesn't support health checking, assume it's still good
			readerLog.Println("Device already connected (no health check available).")
			return nil
		}
		dm.mu.Lock()
		_ = currentDevice.Close() // Ignore error
		dm.device = nil
		dm.hasDevice = false
		dm.mu.Unlock()
	}

	devicePathToConnect := dm.devicePath
	if devicePathToConnect == "" {
		return fmt.Errorf("no device path configured")
	}

	newDevice, errOpen := dm.manager.OpenDevice(devicePathToConnect)
	if errOpen != nil {
		return fmt.Errorf("failed to open device %s: %w", devicePathToConnect, errOpen)
	}
	// Note: Device initialization is handled inside OpenDevice()

	dm.mu.Lock()
	dm.device = newDevice
	dm.hasDevice = true
	dm.devicePath = devicePathToConnect
	// The device works, so whatever was last wrong with it is worth reporting
	// again if it comes back.
	dm.lastErr = ""
	dm.mu.Unlock()

	readerLog.Printf("Successfully connected to device: %s", newDevice.String())
	dm.emitEvent(DeviceConnected, fmt.Sprintf("Connected to %s", newDevice.String()), nil)
	return nil
}

// EnsureConnected ensures the device is connected and responsive.
// If not connected, attempts to connect. If in cooldown, returns an error.
// This method manages internal retry state for the device manager.
func (dm *DeviceManager) EnsureConnected(stopChan <-chan struct{}) error {
	dm.mu.RLock()
	inCool := dm.inCooldown
	dm.mu.RUnlock()

	if inCool {
		return fmt.Errorf("device in cooldown period")
	}

	// Try to connect if not already connected
	err := dm.TryConnect()
	if err == nil {
		// Success - reset retry count
		dm.mu.Lock()
		dm.retryCount = 0
		dm.mu.Unlock()
		return nil
	}

	// Connection failed - handle the error using the existing error handling logic
	needsCooldown := dm.HandleError(err, stopChan)
	if needsCooldown {
		return fmt.Errorf("device entered cooldown after error: %w", err)
	}

	// Check if we successfully reconnected during HandleError
	dm.mu.RLock()
	hasDevice := dm.hasDevice
	dm.mu.RUnlock()

	if hasDevice {
		return nil
	}

	return fmt.Errorf("failed to ensure device connection: %w", err)
}

// Reconnect attempts to reconnect to the device with exponential backoff.
func (dm *DeviceManager) Reconnect(stopChan <-chan struct{}) error {
	return dm.reconnectDevice(false, stopChan)
}

// ForceReconnect attempts to force reconnect with device reset wait time.
func (dm *DeviceManager) ForceReconnect(stopChan <-chan struct{}) error {
	return dm.reconnectDevice(true, stopChan)
}

// reconnectDevice attempts to reconnect to the NFC device with configurable retry logic.
func (dm *DeviceManager) reconnectDevice(forceMode bool, stopChan <-chan struct{}) error {
	logPrefix := "Reconnect"
	maxAttempts := MaxReconnectTries
	if forceMode {
		logPrefix = "Force reconnect"
		maxAttempts = 3
	}

	readerLog.Printf("%s: Attempting to reconnect device (path hint: %s)...", logPrefix, dm.devicePath)

	// Close existing device
	dm.mu.Lock()
	if dm.hasDevice && dm.device != nil {
		readerLog.Printf("%s: Closing existing device connection.", logPrefix)
		_ = dm.device.Close()
		dm.device = nil
		dm.hasDevice = false
	}
	dm.mu.Unlock()

	// For force mode, wait for device reset
	if forceMode {
		readerLog.Println("Waiting for device to reset after close...")
		select {
		case <-dm.clock.After(DeviceResetWaitTime):
		case <-stopChan:
			readerLog.Printf("%s: Stop signal received during wait, aborting.", logPrefix)
			return fmt.Errorf("reconnection aborted by stop signal")
		}
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		connectErr := dm.TryConnect()
		if connectErr == nil {
			readerLog.Printf("%s: Attempt %d successful.", logPrefix, attempt)
			return nil
		}

		lastErr = connectErr
		readerWarn.Printf("%s: Attempt %d failed: %v", logPrefix, attempt, connectErr)

		// Calculate backoff delay
		var backoffDelay time.Duration
		if forceMode {
			backoffDelay = time.Second * time.Duration(attempt)
		} else {
			backoffDelay = ReconnectDelay * time.Duration(attempt)
		}

		select {
		case <-stopChan:
			readerLog.Printf("%s: Stop signal received, aborting reconnection.", logPrefix)
			return fmt.Errorf("reconnection aborted by stop signal")
		case <-dm.clock.After(backoffDelay):
		}
	}

	errMsg := fmt.Sprintf("%s failed after %d attempts: %v", logPrefix, maxAttempts, lastErr)
	readerFail.Println(errMsg)
	return fmt.Errorf("%s", errMsg)
}

// Close closes the current device connection.
func (dm *DeviceManager) Close() {
	dm.mu.Lock()
	shouldEmit := dm.hasDevice && dm.device != nil
	if shouldEmit {
		readerLog.Println("Closing device in DeviceManager.")
		if err := dm.device.Close(); err != nil {
			readerFail.Printf("Error closing device: %v", err)
		}
		dm.device = nil
		dm.hasDevice = false
	}
	dm.mu.Unlock()

	if shouldEmit {
		dm.emitEvent(DeviceDisconnected, "Device manager closing", nil)
	}
}

// recordError reports whether this error has already been logged, and remembers
// it either way. A fault that persists across polls repeats verbatim, and the
// poll loop is continuous, so logging every occurrence buries everything else.
func (dm *DeviceManager) recordError(err error) bool {
	message := err.Error()

	dm.mu.Lock()
	defer dm.mu.Unlock()

	repeat := dm.lastErr == message
	dm.lastErr = message
	return repeat
}

// clearLastError forgets the last reported error, so the same one is logged
// again if it returns after the device has been working.
func (dm *DeviceManager) clearLastError() {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	dm.lastErr = ""
}

// HandleError processes device errors and determines the appropriate recovery action.
// Returns whether a cooldown was initiated. Retry state is now managed internally.
func (dm *DeviceManager) HandleError(err error, stopChan <-chan struct{}) (needsCooldown bool) {
	// "No card present" is a normal condition for NFC readers, not a device error.
	// Don't log it as an error - the caller will simply retry.
	if IsNoCardError(err) {
		dm.clearLastError()
		return false
	}

	repeat := dm.recordError(err)
	if !repeat {
		readerFail.Printf("Device error: %v", err)
	}

	// Handle IO/Config errors
	if IsIOError(err) || IsDeviceConfigError(err) {
		if !repeat {
			readerFail.Printf("Device error detected (IO/Config): %v. Closing device.", err)
		}
		dm.mu.Lock()
		if dm.hasDevice && dm.device != nil {
			_ = dm.device.Close()
		}
		dm.device = nil
		dm.hasDevice = false
		dm.mu.Unlock()

		dm.emitEvent(DeviceDisconnected, "Device closed due to IO/Config error", err)

		// Check for ACR122-specific errors that need cooldown
		if IsACR122Error(err) {
			dm.mu.Lock()
			if !dm.inCooldown {
				dm.inCooldown = true
				readerWarn.Printf("ACR122-like error. Entering cooldown for %v", DeviceErrorCooldownPeriod)
				dm.cooldownTimer.Reset(DeviceErrorCooldownPeriod)
			}
			dm.mu.Unlock()
			dm.emitEvent(CooldownStarted, fmt.Sprintf("Entering cooldown for %v", DeviceErrorCooldownPeriod), err)
			return true
		}

		readerLog.Println("Attempting force reconnect after IO/Config error...")
		dm.clock.Sleep(PostErrorPauseTime)
		if errReconnect := dm.ForceReconnect(stopChan); errReconnect != nil {
			readerFail.Printf("Force reconnection failed after IO/Config error: %v", errReconnect)
			// Reset to the assigned lane so reconnection targets this reader's own
			// device. For a standalone reader (no assigned path) this is empty and
			// enables auto-discovery; for one that owns a lane it must not adopt a
			// different lane's device.
			dm.mu.Lock()
			dm.devicePath = dm.assignedPath
			dm.mu.Unlock()
			readerLog.Println("Device path reset to assigned lane, waiting for it to return...")
		}
		return false
	}

	// Handle Timeout/Closed errors with retry logic using internal retry count
	if IsTimeoutError(err) || IsDeviceClosedError(err) {
		if !repeat {
			readerFail.Printf("Device error (Timeout/Closed): %v", err)
		}

		dm.mu.Lock()
		currentRetry := dm.retryCount
		dm.mu.Unlock()

		delay := time.Duration(math.Pow(2, float64(currentRetry))) * BaseDelay
		if currentRetry < MaxRetries {
			dm.mu.Lock()
			dm.retryCount++
			newRetry := dm.retryCount
			dm.mu.Unlock()

			readerWarn.Printf("Retrying connection (attempt %d/%d) in %v...", newRetry, MaxRetries, delay)
			dm.emitEvent(DeviceReconnecting, fmt.Sprintf("Retry attempt %d/%d", newRetry, MaxRetries), nil)

			select {
			case <-dm.clock.After(delay):
			case <-stopChan:
				return false
			}
			if errReconnect := dm.Reconnect(stopChan); errReconnect != nil {
				readerFail.Printf("Device reconnection failed: %v", errReconnect)
				dm.emitEvent(DeviceReconnectFailed, fmt.Sprintf("Reconnection attempt %d failed", newRetry), errReconnect)
			} else {
				readerLog.Println("Reconnected successfully.")
				dm.mu.Lock()
				dm.retryCount = 0
				dm.lastErr = ""
				dm.mu.Unlock()
			}
		} else {
			readerFail.Printf("Max retries reached for Timeout/Closed error: %v. Closing device.", err)
			dm.mu.Lock()
			if dm.hasDevice && dm.device != nil {
				_ = dm.device.Close()
			}
			dm.device = nil
			dm.hasDevice = false
			dm.devicePath = dm.assignedPath // Reset to this reader's own lane, not a different one
			dm.retryCount = 0               // Reset retry count when entering cooldown
			if !dm.inCooldown {
				dm.inCooldown = true
				dm.cooldownTimer.Reset(MaxRetriesCooldownPeriod)
				readerWarn.Println("Entering long cooldown after max retries for Timeout/Closed error.")
			}
			dm.mu.Unlock()
			readerLog.Println("Device path cleared, waiting for device connection...")
			dm.emitEvent(CooldownStarted, "Max retries reached, entering cooldown", err)
			return true
		}
		return false
	}

	// Unhandled error - caller should handle
	return false
}

// EndCooldown ends the current cooldown period and attempts to reconnect.
func (dm *DeviceManager) EndCooldown(stopChan <-chan struct{}) {
	readerLog.Println("Device cooldown period ended.")
	dm.mu.Lock()
	dm.inCooldown = false
	dm.mu.Unlock()
	dm.emitEvent(CooldownEnded, "Cooldown period ended, attempting reconnect", nil)
	if err := dm.ForceReconnect(stopChan); err != nil {
		readerFail.Printf("Reconnection after cooldown failed: %v.", err)
	}
}

// CooldownChannel returns the cooldown timer channel for select statements.
func (dm *DeviceManager) CooldownChannel() <-chan time.Time {
	return dm.cooldownTimer.C()
}
