package nfc

import (
	"fmt"
	"testing"
	"time"
)

// TestDeviceManager_EmitsConnectedEvent tests that DeviceManager emits a DeviceConnected event on successful connection
func TestDeviceManager_EmitsConnectedEvent(t *testing.T) {
	mockManager := NewMockManager()
	fakeClock := NewFakeClock(time.Now())
	dm := NewDeviceManager(mockManager, "mock:usb:001", fakeClock)

	// Start listening for events
	eventChan := dm.Events()

	// Connect to device
	err := dm.TryConnect()
	if err != nil {
		t.Fatalf("Expected successful connection, got error: %v", err)
	}

	// Verify DeviceConnected event was emitted
	select {
	case event := <-eventChan:
		if event.Type != DeviceConnected {
			t.Errorf("Expected DeviceConnected event, got %v", event.Type)
		}
		if event.Message == "" {
			t.Error("Expected non-empty message")
		}
		if event.Device == nil {
			t.Error("Expected device in event")
		}
		if event.Err != nil {
			t.Errorf("Expected no error in event, got: %v", event.Err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for DeviceConnected event")
	}
}

// TestDeviceManager_EmitsCooldownStartedEvent tests that DeviceManager emits CooldownStarted on ACR122 error
func TestDeviceManager_EmitsCooldownStartedEvent(t *testing.T) {
	mockManager := NewMockManager()
	fakeClock := NewFakeClock(time.Now())
	dm := NewDeviceManager(mockManager, "mock:usb:001", fakeClock)

	// Connect first
	_ = dm.TryConnect()

	// Drain initial connected event
	<-dm.Events()

	// Create ACR122-specific error (wrap both ErrIO and ErrACR122Specific)
	acr122Error := fmt.Errorf("%w: %w", ErrIO, ErrACR122Specific)

	// Handle the error
	stopChan := make(chan struct{})
	defer close(stopChan)
	needsCooldown := dm.HandleError(acr122Error, stopChan)

	if !needsCooldown {
		t.Error("Expected ACR122 error to trigger cooldown")
	}

	// Verify DeviceDisconnected event
	select {
	case event := <-dm.Events():
		if event.Type != DeviceDisconnected {
			t.Errorf("Expected DeviceDisconnected event first, got %v", event.Type)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for DeviceDisconnected event")
	}

	// Verify CooldownStarted event
	select {
	case event := <-dm.Events():
		if event.Type != CooldownStarted {
			t.Errorf("Expected CooldownStarted event, got %v", event.Type)
		}
		if event.Err == nil {
			t.Error("Expected error in CooldownStarted event")
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for CooldownStarted event")
	}
}

// TestDeviceManager_EmitsReconnectingEvent tests that DeviceManager emits DeviceReconnecting on retry
func TestDeviceManager_EmitsReconnectingEvent(t *testing.T) {
	mockManager := NewMockManager()
	fakeClock := NewFakeClock(time.Now())
	dm := NewDeviceManager(mockManager, "mock:usb:001", fakeClock)

	// Connect first
	_ = dm.TryConnect()

	// Drain initial connected event
	<-dm.Events()

	// Create timeout error to trigger retry
	timeoutError := ErrTimeout

	stopChan := make(chan struct{})
	defer close(stopChan)

	// Handle error in background
	go func() {
		dm.HandleError(timeoutError, stopChan)
	}()

	// Fast-forward time for retry delay
	go func() {
		time.Sleep(10 * time.Millisecond)
		fakeClock.Advance(500 * time.Millisecond)
	}()

	// Verify DeviceReconnecting event
	select {
	case event := <-dm.Events():
		if event.Type != DeviceReconnecting {
			t.Errorf("Expected DeviceReconnecting event, got %v", event.Type)
		}
		if event.Message == "" {
			t.Error("Expected non-empty message for reconnecting event")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Timeout waiting for DeviceReconnecting event")
	}
}

// TestDeviceManager_EmitsCooldownEndedEvent tests that DeviceManager emits CooldownEnded
func TestDeviceManager_EmitsCooldownEndedEvent(t *testing.T) {
	mockManager := NewMockManager()
	fakeClock := NewFakeClock(time.Now())
	dm := NewDeviceManager(mockManager, "mock:usb:001", fakeClock)

	// Manually set cooldown state
	dm.mu.Lock()
	dm.inCooldown = true
	dm.mu.Unlock()

	stopChan := make(chan struct{})
	defer close(stopChan)

	// Call EndCooldown
	go dm.EndCooldown(stopChan)

	// Fast-forward time for reconnect delays
	go func() {
		time.Sleep(10 * time.Millisecond)
		fakeClock.Advance(1 * time.Second)
		time.Sleep(10 * time.Millisecond)
		fakeClock.Advance(2 * time.Second)
	}()

	// Verify CooldownEnded event
	select {
	case event := <-dm.Events():
		if event.Type != CooldownEnded {
			t.Errorf("Expected CooldownEnded event, got %v", event.Type)
		}
		if event.Message == "" {
			t.Error("Expected non-empty message")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Timeout waiting for CooldownEnded event")
	}
}

// TestDeviceManager_EmitsDisconnectedOnClose tests that DeviceManager emits DeviceDisconnected on Close
func TestDeviceManager_EmitsDisconnectedOnClose(t *testing.T) {
	mockManager := NewMockManager()
	fakeClock := NewFakeClock(time.Now())
	dm := NewDeviceManager(mockManager, "mock:usb:001", fakeClock)

	// Connect first
	_ = dm.TryConnect()

	// Drain initial connected event
	<-dm.Events()

	// Close the device
	dm.Close()

	// Verify DeviceDisconnected event
	select {
	case event := <-dm.Events():
		if event.Type != DeviceDisconnected {
			t.Errorf("Expected DeviceDisconnected event, got %v", event.Type)
		}
		if event.Message != "Device manager closing" {
			t.Errorf("Expected 'Device manager closing' message, got: %s", event.Message)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Timeout waiting for DeviceDisconnected event")
	}
}

// TestDeviceEventType_String tests the String method for DeviceEventType
func TestDeviceEventType_String(t *testing.T) {
	tests := []struct {
		eventType DeviceEventType
		expected  string
	}{
		{DeviceConnected, "DeviceConnected"},
		{DeviceDisconnected, "DeviceDisconnected"},
		{DeviceReconnecting, "DeviceReconnecting"},
		{DeviceReconnectFailed, "DeviceReconnectFailed"},
		{CooldownStarted, "CooldownStarted"},
		{CooldownEnded, "CooldownEnded"},
		{DeviceError, "DeviceError"},
		{DeviceEventType(999), "Unknown(999)"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			result := tt.eventType.String()
			if result != tt.expected {
				t.Errorf("Expected %s, got %s", tt.expected, result)
			}
		})
	}
}

// TestDeviceManager_EventChannelBuffering tests that event channel handles backpressure
func TestDeviceManager_EventChannelBuffering(t *testing.T) {
	mockManager := NewMockManager()
	fakeClock := NewFakeClock(time.Now())
	dm := NewDeviceManager(mockManager, "mock:usb:001", fakeClock)

	// Don't read from the event channel to test buffering

	// Connect multiple times to fill the buffer
	for i := 0; i < 15; i++ {
		dm.emitEvent(DeviceConnected, "Test event", nil)
	}

	// Verify buffer has events (should have 10, rest dropped)
	eventCount := 0
	timeout := time.After(100 * time.Millisecond)
	for {
		select {
		case <-dm.Events():
			eventCount++
		case <-timeout:
			if eventCount != 10 {
				t.Errorf("Expected 10 events in buffer, got %d", eventCount)
			}
			return
		}
	}
}

// TestReader_HandlesDeviceEvents tests that deviceReader properly handles device events
func TestReader_HandlesDeviceEvents(t *testing.T) {
	mockManager := NewMockManager()
	fakeClock := NewFakeClock(time.Now())
	reader, err := newDeviceReaderWithClock("mock:usb:001", mockManager, 5*time.Second, fakeClock)
	if err != nil {
		t.Fatalf("Failed to create deviceReader: %v", err)
	}

	// Start the reader
	reader.Start()
	defer reader.Close()

	// Get status channel
	statusChan := reader.StatusUpdates()

	// Drain initial connected status
	select {
	case status := <-statusChan:
		if !status.Connected {
			t.Error("Expected initial connected status")
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Timeout waiting for initial status")
	}

	// The event was already processed during Start
	// Verify the reader is working correctly by checking HasDevice
	if !reader.deviceManager.HasDevice() {
		t.Error("Expected device to be connected")
	}
}

// TestDeviceManager_UsesClockForTiming tests that DeviceManager uses Clock abstraction
func TestDeviceManager_UsesClockForTiming(t *testing.T) {
	mockManager := NewMockManager()
	fakeClock := NewFakeClock(time.Now())
	dm := NewDeviceManager(mockManager, "mock:usb:001", fakeClock)

	// Verify the clock was set
	if dm.clock == nil {
		t.Error("Expected clock to be set")
	}

	// Verify timer uses clock
	if dm.cooldownTimer == nil {
		t.Error("Expected cooldown timer to be created")
	}
}
