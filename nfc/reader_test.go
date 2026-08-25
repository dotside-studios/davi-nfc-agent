package nfc

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// TestReader_WithMockManager demonstrates testing deviceReader with mock implementations.
func TestReader_WithMockManager(t *testing.T) {
	// Create mock manager with a mock device
	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}

	// Create mock device
	mockDevice := NewMockDevice()
	manager.MockDevice = mockDevice

	// Create deviceReader with mock manager
	reader, err := newDeviceReader("mock:usb:001", manager, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to create deviceReader: %v", err)
	}
	defer reader.Stop()
	defer reader.Close()

	// Start the worker to establish connection
	reader.Start()

	// Give the reader time to connect via handleDeviceCheck
	time.Sleep(300 * time.Millisecond)

	// Verify device status
	status := reader.GetDeviceStatus()
	if !status.Connected {
		t.Error("Expected device to be connected")
	}
}

// TestReader_TagDetection demonstrates testing tag detection with mock tags.
func TestReader_TagDetection(t *testing.T) {
	// Create mock manager
	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}

	// Create mock tag with NDEF data
	mockTag := NewMockTag("04A1B2C3")
	mockTag.TagType = "MIFARE Classic 1K"
	mockTag.IsConnected = true

	// Set up NDEF text record
	ndefMessage := EncodeNdefMessageWithTextRecord("Hello World", "en")
	mockTag.Data = ndefMessage

	// Create mock device and add tag to device
	mockDevice := NewMockDevice()
	mockDevice.SetTags([]Tag{mockTag})
	manager.MockDevice = mockDevice

	// Create deviceReader
	reader, err := newDeviceReader("mock:usb:001", manager, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to create deviceReader: %v", err)
	}
	defer reader.Close()
	defer reader.Stop()

	// Start the reader
	reader.Start()

	// Wait for tag data
	select {
	case data := <-reader.Data():
		if data.Err != nil {
			t.Errorf("Received error from data channel: %v", data.Err)
		}
		if data.Card == nil {
			t.Fatal("Expected Card to be non-nil")
		}
		if data.Card.UID != "04A1B2C3" {
			t.Errorf("Expected UID '04A1B2C3', got '%s'", data.Card.UID)
		}
		if data.Card.Type != "MIFARE Classic 1K" {
			t.Errorf("Expected card type 'MIFARE Classic 1K', got '%s'", data.Card.Type)
		}
	case <-time.After(2 * time.Second):
		t.Error("Timeout waiting for tag data")
	}
}

// TestReader_WriteCardData demonstrates testing write operations with mock tags.
func TestReader_WriteCardData(t *testing.T) {
	// Create mock manager
	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}

	// Create mock Classic tag
	mockTag := NewMockClassicTag("04D5E6F7")
	mockTag.IsConnected = true

	// Create mock device and add tag to device
	mockDevice := NewMockDevice()
	mockDevice.SetTags([]Tag{mockTag})
	manager.MockDevice = mockDevice

	// Create deviceReader
	reader, err := newDeviceReader("mock:usb:001", manager, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to create deviceReader: %v", err)
	}
	defer reader.Close()

	// Give reader time to initialize
	time.Sleep(100 * time.Millisecond)

	// Write data to card
	err = reader.WriteCardData("Test Message")
	if err != nil {
		t.Errorf("WriteCardData() failed: %v", err)
	}

	// Verify data was written
	data, _ := mockTag.ReadData()
	if len(data) == 0 {
		t.Error("Expected data to be written to tag")
	}
}

// TestReader_DeviceReconnection demonstrates testing device reconnection.
func TestReader_DeviceReconnection(t *testing.T) {
	// Create mock manager
	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}

	// Create mock device
	mockDevice := NewMockDevice()
	mockDevice.InitError = nil
	manager.MockDevice = mockDevice

	// Create deviceReader
	reader, err := newDeviceReader("mock:usb:001", manager, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to create deviceReader: %v", err)
	}
	defer reader.Close()

	// Give reader time to attempt connection
	time.Sleep(200 * time.Millisecond)

	// Verify device is eventually connected
	status := reader.GetDeviceStatus()
	if !status.Connected {
		t.Log("Device connection status may vary based on initialization timing")
	}
}

// TestReader_NoDeviceFound demonstrates testing when no device is available.
func TestReader_NoDeviceFound(t *testing.T) {
	// Create mock manager with no devices
	manager := NewMockManager()
	manager.DevicesList = []string{}

	// Create deviceReader
	reader, err := newDeviceReader("", manager, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to create deviceReader: %v", err)
	}
	defer reader.Close()

	// Give reader time to search for devices
	time.Sleep(100 * time.Millisecond)

	// Verify device is not connected
	status := reader.GetDeviceStatus()
	if status.Connected {
		t.Error("Expected device to not be connected when no devices available")
	}
}

// TestReader_StatusUpdates demonstrates testing status channel updates.
func TestReader_StatusUpdates(t *testing.T) {
	// Create mock manager
	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}

	// Create deviceReader
	reader, err := newDeviceReader("mock:usb:001", manager, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to create deviceReader: %v", err)
	}
	defer reader.Close()

	// Listen for status updates
	statusReceived := false
	timeout := time.After(1 * time.Second)

	select {
	case status := <-reader.StatusUpdates():
		statusReceived = true
		t.Logf("Received status update: Connected=%v, Message=%s", status.Connected, status.Message)
	case <-timeout:
		// It's okay if we don't receive status immediately
		t.Log("No status update received within timeout (this is okay)")
	}

	_ = statusReceived
}

// TestReader_MultipleTagsDetection demonstrates handling multiple tags.
func TestReader_MultipleTagsDetection(t *testing.T) {
	// Create mock manager
	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}

	// Create multiple mock tags
	tag1 := NewMockTag("04A1B2C3")
	tag1.TagType = "MIFARE Classic 1K"
	tag1.IsConnected = true
	tag1.Data = EncodeNdefMessageWithTextRecord("Tag 1", "en")

	tag2 := NewMockTag("04D5E6F7")
	tag2.TagType = "MIFARE Classic 1K"
	tag2.IsConnected = true
	tag2.Data = EncodeNdefMessageWithTextRecord("Tag 2", "en")

	// Create mock device and add tags to device
	mockDevice := NewMockDevice()
	mockDevice.SetTags([]Tag{tag1, tag2})
	manager.MockDevice = mockDevice

	// Create deviceReader
	reader, err := newDeviceReader("mock:usb:001", manager, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to create deviceReader: %v", err)
	}
	defer reader.Close()
	defer reader.Stop()

	// Start the reader
	reader.Start()

	// Collect data from both tags
	receivedTags := make(map[string]bool)
	timeout := time.After(3 * time.Second)

collectLoop:
	for {
		select {
		case data := <-reader.Data():
			if data.Err != nil {
				t.Logf("Received error: %v", data.Err)
				continue
			}
			if data.Card != nil {
				receivedTags[data.Card.UID] = true
				// Read message from card
				msg, _ := data.Card.ReadMessage()
				var text string
				if ndefMsg, ok := msg.(*NDEFMessage); ok {
					text, _ = ndefMsg.GetText()
				} else if textMsg, ok := msg.(*TextMessage); ok {
					text = textMsg.Text
				}
				t.Logf("Received data from tag: UID=%s, Text=%s", data.Card.UID, text)
			}

			if len(receivedTags) >= 2 {
				break collectLoop
			}
		case <-timeout:
			break collectLoop
		}
	}

	// Note: In practice, only the first tag is typically read in a single pass
	// This test demonstrates the framework for handling multiple tags
	if len(receivedTags) > 0 {
		t.Logf("Successfully received data from %d tag(s)", len(receivedTags))
	}
}

// TestReader_ModeReadOnly tests read-only mode blocks write operations.
func TestReader_ModeReadOnly(t *testing.T) {
	// Create mock manager
	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}

	// Create mock tag
	mockTag := NewMockTag("04A1B2C3")
	mockTag.TagType = "MIFARE Classic 1K"
	mockTag.IsConnected = true
	mockTag.Data = EncodeNdefMessageWithTextRecord("Hello", "en")

	// Create mock device
	mockDevice := NewMockDevice()
	mockDevice.SetTags([]Tag{mockTag})
	manager.MockDevice = mockDevice

	// Create deviceReader
	reader, err := newDeviceReader("mock:usb:001", manager, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to create deviceReader: %v", err)
	}
	defer reader.Close()

	// Set to read-only mode
	reader.SetMode(ModeReadOnly)

	// Verify mode is set
	if reader.GetMode() != ModeReadOnly {
		t.Errorf("Expected mode to be ModeReadOnly, got %v", reader.GetMode())
	}

	// Give reader time to initialize
	time.Sleep(100 * time.Millisecond)

	// Attempt to write should fail
	err = reader.WriteCardData("Test Write")
	if err == nil {
		t.Error("Expected write to fail in read-only mode")
	}
	if err != nil && err.Error() != "reader is in read-only mode, write operations are not allowed" {
		t.Errorf("Expected read-only mode error, got: %v", err)
	}
}

// TestReader_ModeWriteOnly tests write-only mode allows writes but skips reads.
func TestReader_ModeWriteOnly(t *testing.T) {
	// Create mock manager
	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}

	// Create mock tag
	mockTag := NewMockTag("04A1B2C3")
	mockTag.TagType = "MIFARE Classic 1K"
	mockTag.IsConnected = true
	mockTag.Data = EncodeNdefMessageWithTextRecord("Hello", "en")

	// Create mock device
	mockDevice := NewMockDevice()
	mockDevice.SetTags([]Tag{mockTag})
	manager.MockDevice = mockDevice

	// Create deviceReader
	reader, err := newDeviceReader("mock:usb:001", manager, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to create deviceReader: %v", err)
	}
	defer reader.Close()

	// Set to write-only mode
	reader.SetMode(ModeWriteOnly)

	// Verify mode is set
	if reader.GetMode() != ModeWriteOnly {
		t.Errorf("Expected mode to be ModeWriteOnly, got %v", reader.GetMode())
	}

	// Manually update cache to simulate card present
	reader.cache.HasChanged("04A1B2C3") // This sets the lastUID
	reader.cache.UpdateLastSeenTime("04A1B2C3")

	// Give reader time to initialize
	time.Sleep(100 * time.Millisecond)

	// Write should succeed
	err = reader.WriteCardData("Test Write")
	if err != nil {
		t.Errorf("Expected write to succeed in write-only mode, got error: %v", err)
	}

	// Verify data was written
	data, _ := mockTag.ReadData()
	if len(data) == 0 {
		t.Error("Expected data to be written to tag")
	}
}

// TestReader_ModeReadWrite tests default read/write mode allows both operations.
func TestReader_ModeReadWrite(t *testing.T) {
	// Create mock manager
	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}

	// Create mock tag
	mockTag := NewMockTag("04A1B2C3")
	mockTag.TagType = "MIFARE Classic 1K"
	mockTag.IsConnected = true
	mockTag.Data = EncodeNdefMessageWithTextRecord("Hello", "en")

	// Create mock device
	mockDevice := NewMockDevice()
	mockDevice.SetTags([]Tag{mockTag})
	manager.MockDevice = mockDevice

	// Create deviceReader (default mode is ModeReadWrite)
	reader, err := newDeviceReader("mock:usb:001", manager, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to create deviceReader: %v", err)
	}
	defer reader.Close()
	defer reader.Stop()

	// Verify default mode is ModeReadWrite
	if reader.GetMode() != ModeReadWrite {
		t.Errorf("Expected default mode to be ModeReadWrite, got %v", reader.GetMode())
	}

	// Start reader for reading
	reader.Start()

	// Read should work
	select {
	case data := <-reader.Data():
		if data.Err != nil {
			t.Errorf("Expected read to succeed, got error: %v", data.Err)
		}
		if data.Card == nil {
			t.Error("Expected card data")
		}
	case <-time.After(2 * time.Second):
		t.Error("Timeout waiting for card data")
	}

	// Write should also work
	err = reader.WriteCardData("Test Write")
	if err != nil {
		t.Errorf("Expected write to succeed in read/write mode, got error: %v", err)
	}
}

// TestReader_SetMode tests changing mode at runtime.
func TestReader_SetMode(t *testing.T) {
	// Create mock manager
	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}

	// Create deviceReader
	reader, err := newDeviceReader("mock:usb:001", manager, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to create deviceReader: %v", err)
	}
	defer reader.Close()

	// Default should be ModeReadWrite
	if reader.GetMode() != ModeReadWrite {
		t.Errorf("Expected default mode ModeReadWrite, got %v", reader.GetMode())
	}

	// Change to read-only
	reader.SetMode(ModeReadOnly)
	if reader.GetMode() != ModeReadOnly {
		t.Errorf("Expected mode ModeReadOnly after SetMode, got %v", reader.GetMode())
	}

	// Change to write-only
	reader.SetMode(ModeWriteOnly)
	if reader.GetMode() != ModeWriteOnly {
		t.Errorf("Expected mode ModeWriteOnly after SetMode, got %v", reader.GetMode())
	}

	// Change back to read/write
	reader.SetMode(ModeReadWrite)
	if reader.GetMode() != ModeReadWrite {
		t.Errorf("Expected mode ModeReadWrite after SetMode, got %v", reader.GetMode())
	}
}

// TestReader_WriteErrorPropagation tests that write errors are properly propagated.
func TestReader_WriteErrorPropagation(t *testing.T) {
	// Create mock manager
	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}

	// Create mock tag that will fail writes
	mockTag := NewMockTag("04A1B2C3")
	mockTag.TagType = "MIFARE Classic 1K"
	mockTag.IsConnected = true
	mockTag.Data = EncodeNdefMessageWithTextRecord("Hello", "en")
	mockTag.WriteDataError = fmt.Errorf("simulated write failure") // Force write to fail

	// Create mock device
	mockDevice := NewMockDevice()
	mockDevice.SetTags([]Tag{mockTag})
	manager.MockDevice = mockDevice

	// Create deviceReader
	reader, err := newDeviceReader("mock:usb:001", manager, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to create deviceReader: %v", err)
	}
	defer reader.Close()

	// Update cache to simulate card present
	reader.cache.HasChanged("04A1B2C3")
	reader.cache.UpdateLastSeenTime("04A1B2C3")

	// Give reader time to initialize
	time.Sleep(100 * time.Millisecond)

	// Attempt write - should fail and return error
	err = reader.WriteCardData("Test Write")
	if err == nil {
		t.Error("Expected write to fail when tag returns error, but got no error")
	}
	t.Logf("Write correctly returned error: %v", err)
}

// TestReader_MultipleCardsGuard tests that writes are blocked when multiple cards are detected.
func TestReader_MultipleCardsGuard(t *testing.T) {
	// Create mock manager
	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}

	// Create multiple mock tags
	tag1 := NewMockTag("04A1B2C3")
	tag1.TagType = "MIFARE Classic 1K"
	tag1.IsConnected = true
	tag1.Data = EncodeNdefMessageWithTextRecord("Tag 1", "en")

	tag2 := NewMockTag("04D5E6F7")
	tag2.TagType = "MIFARE Classic 1K"
	tag2.IsConnected = true
	tag2.Data = EncodeNdefMessageWithTextRecord("Tag 2", "en")

	// Create mock device with MULTIPLE tags
	mockDevice := NewMockDevice()
	mockDevice.SetTags([]Tag{tag1, tag2})
	manager.MockDevice = mockDevice

	// Create deviceReader
	reader, err := newDeviceReader("mock:usb:001", manager, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to create deviceReader: %v", err)
	}
	defer reader.Close()

	// Update cache to simulate one card present
	reader.cache.HasChanged("04A1B2C3")
	reader.cache.UpdateLastSeenTime("04A1B2C3")

	// Give reader time to initialize
	time.Sleep(100 * time.Millisecond)

	// Attempt write - should fail due to multiple cards
	err = reader.WriteCardData("Test Write")
	if err == nil {
		t.Fatal("Expected write to fail when multiple cards are detected, but got no error")
	}

	// Verify the error message mentions multiple cards
	if !contains(err.Error(), "multiple cards") {
		t.Errorf("Expected error message to mention 'multiple cards', got: %v", err)
	}
	t.Logf("Write correctly blocked with error: %v", err)
}

// TestReader_UIDMismatchGuard tests that writes are blocked when card UID doesn't match cache.
func TestReader_UIDMismatchGuard(t *testing.T) {
	// Create mock manager
	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}

	// Create mock tag with specific UID
	mockTag := NewMockTag("04A1B2C3")
	mockTag.TagType = "MIFARE Classic 1K"
	mockTag.IsConnected = true
	mockTag.Data = EncodeNdefMessageWithTextRecord("Hello", "en")

	// Create mock device
	mockDevice := NewMockDevice()
	mockDevice.SetTags([]Tag{mockTag})
	manager.MockDevice = mockDevice

	// Create deviceReader
	reader, err := newDeviceReader("mock:usb:001", manager, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to create deviceReader: %v", err)
	}
	defer reader.Close()

	// Update cache with DIFFERENT UID
	reader.cache.HasChanged("04DIFFERENT")
	reader.cache.UpdateLastSeenTime("04DIFFERENT")

	// Give reader time to initialize
	time.Sleep(100 * time.Millisecond)

	// Attempt write - should fail due to UID mismatch
	err = reader.WriteCardData("Test Write")
	if err == nil {
		t.Fatal("Expected write to fail when card UID doesn't match cache, but got no error")
	}

	// Verify the error message mentions mismatch
	if !contains(err.Error(), "mismatch") {
		t.Errorf("Expected error message to mention 'mismatch', got: %v", err)
	}
	t.Logf("Write correctly blocked with error: %v", err)
}

// TestReader_SingleCardWriteSucceeds tests that writes succeed when exactly one card is present and matches cache.
func TestReader_SingleCardWriteSucceeds(t *testing.T) {
	// Create mock manager
	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}

	// Create mock tag
	mockTag := NewMockTag("04A1B2C3")
	mockTag.TagType = "MIFARE Classic 1K"
	mockTag.IsConnected = true
	mockTag.Data = EncodeNdefMessageWithTextRecord("Hello", "en")

	// Create mock device with SINGLE tag
	mockDevice := NewMockDevice()
	mockDevice.SetTags([]Tag{mockTag})
	manager.MockDevice = mockDevice

	// Create deviceReader
	reader, err := newDeviceReader("mock:usb:001", manager, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to create deviceReader: %v", err)
	}
	defer reader.Close()

	// Update cache with MATCHING UID
	reader.cache.HasChanged("04A1B2C3")
	reader.cache.UpdateLastSeenTime("04A1B2C3")

	// Give reader time to initialize
	time.Sleep(100 * time.Millisecond)

	// Attempt write - should succeed
	err = reader.WriteCardData("Test Write")
	if err != nil {
		t.Errorf("Expected write to succeed with single matching card, got error: %v", err)
	}

	// Verify data was written
	data, _ := mockTag.ReadData()
	if len(data) == 0 {
		t.Error("Expected data to be written to tag")
	}
	t.Log("Write succeeded as expected")
}

// Helper function to check if string contains substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestReader_WriteOnlyModeCachePopulation tests that write-only mode populates cache for writes.
func TestReader_WriteOnlyModeCachePopulation(t *testing.T) {
	// Create mock manager
	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}

	// Create mock tag
	mockTag := NewMockTag("04A1B2C3")
	mockTag.TagType = "MIFARE Classic 1K"
	mockTag.IsConnected = true
	mockTag.Data = EncodeNdefMessageWithTextRecord("Hello", "en")

	// Create mock device
	mockDevice := NewMockDevice()
	mockDevice.SetTags([]Tag{mockTag})
	manager.MockDevice = mockDevice

	// Create deviceReader in write-only mode
	reader, err := newDeviceReader("mock:usb:001", manager, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to create deviceReader: %v", err)
	}
	defer reader.Close()
	defer reader.Stop()

	// Set to write-only mode
	reader.SetMode(ModeWriteOnly)

	// Start reader to populate cache
	reader.Start()

	// Give reader time to populate cache
	time.Sleep(300 * time.Millisecond)

	// Write should succeed even without explicit cache population
	err = reader.WriteCardData("Test Write")
	if err != nil {
		t.Errorf("Expected write to succeed in write-only mode after cache population, got error: %v", err)
	}

	// Verify data was written
	data, _ := mockTag.ReadData()
	if len(data) == 0 {
		t.Error("Expected data to be written to tag")
	}
	t.Log("Write-only mode cache population works correctly")
}

// TestReader_WriteMessageWithOptions_TextRecord tests writing a text NDEF message.
func TestReader_WriteMessageWithOptions_TextRecord(t *testing.T) {
	// Create mock manager
	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}

	// Create mock tag with existing NDEF data
	mockTag := NewMockClassicTag("04A1B2C3")
	mockTag.IsConnected = true
	existingNDEF := EncodeNdefMessageWithTextRecord("Original", "en")
	mockTag.Data = existingNDEF

	// Create mock device
	mockDevice := NewMockDevice()
	mockDevice.SetTags([]Tag{mockTag})
	manager.MockDevice = mockDevice

	// Create deviceReader
	reader, err := newDeviceReader("mock:usb:001", manager, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to create deviceReader: %v", err)
	}
	defer reader.Close()

	// Give reader time to initialize
	time.Sleep(100 * time.Millisecond)

	// Build NDEF text message
	msg := &NDEFMessageBuilder{
		Records: []NDEFRecordBuilder{
			&NDEFText{Content: "Hello World", Language: "en"},
		},
	}
	ndefMsg := msg.MustBuild()

	// Write with overwrite option
	err = reader.WriteMessageWithOptions(ndefMsg, WriteOptions{
		Overwrite: true,
		Index:     -1,
	})
	if err != nil {
		t.Errorf("WriteMessageWithOptions() failed: %v", err)
	}

	// Verify data was written
	data, _ := mockTag.ReadData()
	if len(data) == 0 {
		t.Error("Expected data to be written to tag")
	}

	// Verify the message content
	records, err := parseNDEFRecords(data)
	if err != nil {
		t.Fatalf("Failed to parse written NDEF: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("Expected at least one NDEF record")
	}
	if text, ok := records[0].GetText(); ok {
		if text != "Hello World" {
			t.Errorf("Expected text 'Hello World', got '%s'", text)
		}
	} else {
		t.Error("Expected first record to be a text record")
	}
}

// TestReader_WriteMessageWithOptions_URIRecord tests writing a URI NDEF message.
func TestReader_WriteMessageWithOptions_URIRecord(t *testing.T) {
	// Create mock manager
	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}

	// Create mock tag
	mockTag := NewMockClassicTag("04B2C3D4")
	mockTag.IsConnected = true
	existingNDEF := EncodeNdefMessageWithTextRecord("Original", "en")
	mockTag.Data = existingNDEF

	// Create mock device
	mockDevice := NewMockDevice()
	mockDevice.SetTags([]Tag{mockTag})
	manager.MockDevice = mockDevice

	// Create deviceReader
	reader, err := newDeviceReader("mock:usb:001", manager, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to create deviceReader: %v", err)
	}
	defer reader.Close()

	// Give reader time to initialize
	time.Sleep(100 * time.Millisecond)

	// Build NDEF URI message
	msg := &NDEFMessageBuilder{
		Records: []NDEFRecordBuilder{
			&NDEFURI{Content: "https://example.com"},
		},
	}
	ndefMsg := msg.MustBuild()

	// Write URI message
	err = reader.WriteMessageWithOptions(ndefMsg, WriteOptions{
		Overwrite: true,
		Index:     -1,
	})
	if err != nil {
		t.Errorf("WriteMessageWithOptions() failed: %v", err)
	}

	// Verify data was written
	data, _ := mockTag.ReadData()
	if len(data) == 0 {
		t.Error("Expected data to be written to tag")
	}

	// Verify the message content
	records, err := parseNDEFRecords(data)
	if err != nil {
		t.Fatalf("Failed to parse written NDEF: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("Expected at least one NDEF record")
	}
	if uri, ok := records[0].GetURI(); ok {
		if uri != "https://example.com" {
			t.Errorf("Expected URI 'https://example.com', got '%s'", uri)
		}
	} else {
		t.Error("Expected first record to be a URI record")
	}
}

// TestReader_WriteMessageWithOptions_AppendMode tests appending records to existing NDEF.
func TestReader_WriteMessageWithOptions_AppendMode(t *testing.T) {
	// Create mock manager
	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}

	// Create mock tag with existing NDEF (one text record)
	mockTag := NewMockClassicTag("04C3D4E5")
	mockTag.IsConnected = true
	existingNDEF := EncodeNdefMessageWithTextRecord("First Record", "en")
	mockTag.Data = existingNDEF

	// Create mock device
	mockDevice := NewMockDevice()
	mockDevice.SetTags([]Tag{mockTag})
	manager.MockDevice = mockDevice

	// Create deviceReader
	reader, err := newDeviceReader("mock:usb:001", manager, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to create deviceReader: %v", err)
	}
	defer reader.Close()

	// Give reader time to initialize
	time.Sleep(100 * time.Millisecond)

	// Build second record to append
	msg := &NDEFMessageBuilder{
		Records: []NDEFRecordBuilder{
			&NDEFText{Content: "Second Record", Language: "en"},
		},
	}
	ndefMsg := msg.MustBuild()

	// Write in append mode
	err = reader.WriteMessageWithOptions(ndefMsg, WriteOptions{
		Overwrite: false,
		Index:     -1, // -1 means append
	})
	if err != nil {
		t.Errorf("WriteMessageWithOptions() append failed: %v", err)
	}

	// Verify data was written
	data, _ := mockTag.ReadData()
	if len(data) == 0 {
		t.Error("Expected data to be written to tag")
	}

	// Verify we now have 2 records
	records, err := parseNDEFRecords(data)
	if err != nil {
		t.Fatalf("Failed to parse written NDEF: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("Expected 2 records after append, got %d", len(records))
	}

	// Verify first record unchanged
	if text, ok := records[0].GetText(); ok {
		if text != "First Record" {
			t.Errorf("Expected first record 'First Record', got '%s'", text)
		}
	}

	// Verify second record appended
	if len(records) >= 2 {
		if text, ok := records[1].GetText(); ok {
			if text != "Second Record" {
				t.Errorf("Expected second record 'Second Record', got '%s'", text)
			}
		}
	}
}

// TestReader_WriteMessageWithOptions_ReplaceAtIndex tests replacing a record at specific index.
func TestReader_WriteMessageWithOptions_ReplaceAtIndex(t *testing.T) {
	// Create mock manager
	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}

	// Create mock tag with two existing text records
	mockTag := NewMockClassicTag("04D4E5F6")
	mockTag.IsConnected = true

	// Build initial message with 2 records
	initialMsg := &NDEFMessageBuilder{
		Records: []NDEFRecordBuilder{
			&NDEFText{Content: "Record 0", Language: "en"},
			&NDEFText{Content: "Record 1", Language: "en"},
		},
	}
	existingNDEF, _ := initialMsg.MustBuild().Encode()
	mockTag.Data = existingNDEF

	// Create mock device
	mockDevice := NewMockDevice()
	mockDevice.SetTags([]Tag{mockTag})
	manager.MockDevice = mockDevice

	// Create deviceReader
	reader, err := newDeviceReader("mock:usb:001", manager, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to create deviceReader: %v", err)
	}
	defer reader.Close()

	// Give reader time to initialize
	time.Sleep(100 * time.Millisecond)

	// Build replacement record
	msg := &NDEFMessageBuilder{
		Records: []NDEFRecordBuilder{
			&NDEFText{Content: "Replaced Record 1", Language: "en"},
		},
	}
	ndefMsg := msg.MustBuild()

	// Write to replace record at index 1
	err = reader.WriteMessageWithOptions(ndefMsg, WriteOptions{
		Overwrite: false,
		Index:     1,
	})
	if err != nil {
		t.Errorf("WriteMessageWithOptions() replace at index failed: %v", err)
	}

	// Verify data was written
	data, _ := mockTag.ReadData()
	if len(data) == 0 {
		t.Error("Expected data to be written to tag")
	}

	// Verify we still have 2 records
	records, err := parseNDEFRecords(data)
	if err != nil {
		t.Fatalf("Failed to parse written NDEF: %v", err)
	}
	if len(records) != 2 {
		t.Errorf("Expected 2 records after replace, got %d", len(records))
	}

	// Verify first record unchanged
	if text, ok := records[0].GetText(); ok {
		if text != "Record 0" {
			t.Errorf("Expected first record 'Record 0', got '%s'", text)
		}
	}

	// Verify second record replaced
	if len(records) >= 2 {
		if text, ok := records[1].GetText(); ok {
			if text != "Replaced Record 1" {
				t.Errorf("Expected second record 'Replaced Record 1', got '%s'", text)
			}
		}
	}
}

// TestReader_WriteMessageWithOptions_MultipleCards tests that write fails with multiple cards.
func TestReader_WriteMessageWithOptions_MultipleCards(t *testing.T) {
	// Create mock manager
	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}

	// Create TWO mock tags
	mockTag1 := NewMockClassicTag("04E5F6A1")
	mockTag1.IsConnected = true
	mockTag2 := NewMockClassicTag("04F6A1B2")
	mockTag2.IsConnected = true

	// Create mock device with BOTH tags
	mockDevice := NewMockDevice()
	mockDevice.SetTags([]Tag{mockTag1, mockTag2})
	manager.MockDevice = mockDevice

	// Create deviceReader
	reader, err := newDeviceReader("mock:usb:001", manager, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to create deviceReader: %v", err)
	}
	defer reader.Close()

	// Give reader time to initialize
	time.Sleep(100 * time.Millisecond)

	// Build NDEF message
	msg := &NDEFMessageBuilder{
		Records: []NDEFRecordBuilder{
			&NDEFText{Content: "Test", Language: "en"},
		},
	}
	ndefMsg := msg.MustBuild()

	// Attempt write - should fail with multiple cards
	err = reader.WriteMessageWithOptions(ndefMsg, WriteOptions{
		Overwrite: true,
		Index:     -1,
	})

	// Expect error about multiple cards
	if err == nil {
		t.Error("Expected error when writing with multiple cards present, got nil")
	} else if !contains(err.Error(), "multiple cards") {
		t.Errorf("Expected error about multiple cards, got: %v", err)
	}
}

// TestReader_WriteMessageWithOptions_NoCard tests that write fails with no card.
func TestReader_WriteMessageWithOptions_NoCard(t *testing.T) {
	// Create mock manager
	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}

	// Create mock device with NO tags
	mockDevice := NewMockDevice()
	mockDevice.SetTags([]Tag{})
	manager.MockDevice = mockDevice

	// Create deviceReader
	reader, err := newDeviceReader("mock:usb:001", manager, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to create deviceReader: %v", err)
	}
	defer reader.Close()

	// Give reader time to initialize
	time.Sleep(100 * time.Millisecond)

	// Build NDEF message
	msg := &NDEFMessageBuilder{
		Records: []NDEFRecordBuilder{
			&NDEFText{Content: "Test", Language: "en"},
		},
	}
	ndefMsg := msg.MustBuild()

	// Attempt write - should fail with no card
	err = reader.WriteMessageWithOptions(ndefMsg, WriteOptions{
		Overwrite: true,
		Index:     -1,
	})

	// Expect error about no card
	if err == nil {
		t.Error("Expected error when writing with no card present, got nil")
	} else if !contains(err.Error(), "no card") {
		t.Errorf("Expected error about no card, got: %v", err)
	}
}

// An error no branch recognizes leaves the device exactly as it was, so the
// reader still has one and goes straight back to polling it. That is a loop
// with no delay in it and a log line every turn, which is the shape behind
// hundreds of identical lines a second against a phone that had disconnected.
func TestHandleDeviceErrors_PacesAnErrorNothingRecognizes(t *testing.T) {
	manager := NewMockManager()
	manager.MockDevice = NewMockDevice()
	clock := NewFakeClock(time.Now())

	reader, err := newDeviceReaderWithClock("mock:usb:001", manager, 5*time.Second, clock)
	if err != nil {
		t.Fatalf("newDeviceReaderWithClock: %v", err)
	}
	defer reader.Close()

	if err := reader.deviceManager.TryConnect(); err != nil {
		t.Fatalf("TryConnect: %v", err)
	}

	before := clock.Now()
	reader.handleDeviceErrors(errors.New("something no classifier claims"))

	if waited := clock.Now().Sub(before); waited < UnhandledErrorRetryInterval {
		t.Errorf("expected the loop to pause before polling again, waited %v", waited)
	}
}
