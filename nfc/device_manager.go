package nfc

import (
	"fmt"
	"log"
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

	// Reconnection state
	retryCount    int           // Tracks retry attempts for timeout/closed errors
	inCooldown    bool
	cooldownTimer Timer         // Timer interface for testability
	clock         Clock         // Clock abstraction for time operations

	// Event broadcasting
	events   chan DeviceEvent // Buffered channel for device events
	eventMux sync.RWMutex     // Protects event channel

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
		log.Printf("Device event emitted: %s - %s", eventType, message)
	default:
		log.Printf("Warning: Device event channel full, dropping event: %s", eventType)
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
				log.Println("Device already connected and responsive.")
				return nil
			} else {
				log.Printf("Device was marked connected, but health check failed: %v. Attempting full reconnect.", healthErr)
			}
		} else {
			// Device doesn't support health checking, assume it's still good
			log.Println("Device already connected (no health check available).")
			return nil
		}
		dm.mu.Lock()
		currentDevice.Close() // Ignore error
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
	dm.mu.Unlock()

	log.Printf("Successfully connected to device: %s", newDevice.String())
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

	log.Printf("%s: Attempting to reconnect device (path hint: %s)...", logPrefix, dm.devicePath)

	// Close existing device
	dm.mu.Lock()
	if dm.hasDevice && dm.device != nil {
		log.Printf("%s: Closing existing device connection.", logPrefix)
		dm.device.Close()
		dm.device = nil
		dm.hasDevice = false
	}
	dm.mu.Unlock()

	// For force mode, wait for device reset
	if forceMode {
		log.Println("Waiting for device to reset after close...")
		select {
		case <-dm.clock.After(DeviceResetWaitTime):
		case <-stopChan:
			log.Printf("%s: Stop signal received during wait, aborting.", logPrefix)
			return fmt.Errorf("reconnection aborted by stop signal")
		}
	}

	var lastErr error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		connectErr := dm.TryConnect()
		if connectErr == nil {
			log.Printf("%s: Attempt %d successful.", logPrefix, attempt)
			return nil
		}

		lastErr = connectErr
		log.Printf("%s: Attempt %d failed: %v", logPrefix, attempt, connectErr)

		// Calculate backoff delay
		var backoffDelay time.Duration
		if forceMode {
			backoffDelay = time.Second * time.Duration(attempt)
		} else {
			backoffDelay = ReconnectDelay * time.Duration(attempt)
		}

		select {
		case <-stopChan:
			log.Printf("%s: Stop signal received, aborting reconnection.", logPrefix)
			return fmt.Errorf("reconnection aborted by stop signal")
		case <-dm.clock.After(backoffDelay):
		}
	}

	errMsg := fmt.Sprintf("%s failed after %d attempts: %v", logPrefix, maxAttempts, lastErr)
	log.Println(errMsg)
	return fmt.Errorf("%s", errMsg)
}

// Close closes the current device connection.
func (dm *DeviceManager) Close() {
	dm.mu.Lock()
	shouldEmit := dm.hasDevice && dm.device != nil
	if shouldEmit {
		log.Println("Closing device in DeviceManager.")
		if err := dm.device.Close(); err != nil {
			log.Printf("Error closing device: %v", err)
		}
		dm.device = nil
		dm.hasDevice = false
	}
	dm.mu.Unlock()

	if shouldEmit {
		dm.emitEvent(DeviceDisconnected, "Device manager closing", nil)
	}
}

// HandleError processes device errors and determines the appropriate recovery action.
// Returns whether a cooldown was initiated. Retry state is now managed internally.
func (dm *DeviceManager) HandleError(err error, stopChan <-chan struct{}) (needsCooldown bool) {
	// "No card present" is a normal condition for NFC readers, not a device error.
	// Don't log it as an error - the caller will simply retry.
	if IsNoCardError(err) {
		return false
	}

	log.Printf("Device error: %v", err)

	// Handle IO/Config errors
	if IsIOError(err) || IsDeviceConfigError(err) {
		log.Printf("Device error detected (IO/Config): %v. Closing device.", err)
		dm.mu.Lock()
		if dm.hasDevice && dm.device != nil {
			dm.device.Close()
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
				log.Printf("ACR122-like error. Entering cooldown for %v", DeviceErrorCooldownPeriod)
				dm.cooldownTimer.Reset(DeviceErrorCooldownPeriod)
			}
			dm.mu.Unlock()
			dm.emitEvent(CooldownStarted, fmt.Sprintf("Entering cooldown for %v", DeviceErrorCooldownPeriod), err)
			return true
		}

		log.Println("Attempting force reconnect after IO/Config error...")
		dm.clock.Sleep(PostErrorPauseTime)
		if errReconnect := dm.ForceReconnect(stopChan); errReconnect != nil {
			log.Printf("Force reconnection failed after IO/Config error: %v", errReconnect)
		}
		return false
	}

	// Handle Timeout/Closed errors with retry logic using internal retry count
	if IsTimeoutError(err) || IsDeviceClosedError(err) {
		log.Printf("Device error (Timeout/Closed): %v", err)

		dm.mu.Lock()
		currentRetry := dm.retryCount
		dm.mu.Unlock()

		delay := time.Duration(math.Pow(2, float64(currentRetry))) * BaseDelay
		if currentRetry < MaxRetries {
			dm.mu.Lock()
			dm.retryCount++
			newRetry := dm.retryCount
			dm.mu.Unlock()

			log.Printf("Retrying connection (attempt %d/%d) in %v...", newRetry, MaxRetries, delay)
			dm.emitEvent(DeviceReconnecting, fmt.Sprintf("Retry attempt %d/%d", newRetry, MaxRetries), nil)

			select {
			case <-dm.clock.After(delay):
			case <-stopChan:
				return false
			}
			if errReconnect := dm.Reconnect(stopChan); errReconnect != nil {
				log.Printf("Device reconnection failed: %v", errReconnect)
				dm.emitEvent(DeviceReconnectFailed, fmt.Sprintf("Reconnection attempt %d failed", newRetry), errReconnect)
			} else {
				log.Println("Reconnected successfully.")
				dm.mu.Lock()
				dm.retryCount = 0
				dm.mu.Unlock()
			}
		} else {
			log.Printf("Max retries reached for Timeout/Closed error: %v. Closing device.", err)
			dm.mu.Lock()
			if dm.hasDevice && dm.device != nil {
				dm.device.Close()
			}
			dm.device = nil
			dm.hasDevice = false
			dm.retryCount = 0 // Reset retry count when entering cooldown
			if !dm.inCooldown {
				dm.inCooldown = true
				dm.cooldownTimer.Reset(MaxRetriesCooldownPeriod)
				log.Println("Entering long cooldown after max retries for Timeout/Closed error.")
			}
			dm.mu.Unlock()
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
	log.Println("Device cooldown period ended.")
	dm.mu.Lock()
	dm.inCooldown = false
	dm.mu.Unlock()
	dm.emitEvent(CooldownEnded, "Cooldown period ended, attempting reconnect", nil)
	if err := dm.ForceReconnect(stopChan); err != nil {
		log.Printf("Reconnection after cooldown failed: %v.", err)
	}
}

// CooldownChannel returns the cooldown timer channel for select statements.
func (dm *DeviceManager) CooldownChannel() <-chan time.Time {
	return dm.cooldownTimer.C()
}
