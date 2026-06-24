package nfc

import (
	"testing"
	"time"
)

// TestGetCapabilities_Success verifies the reader reports the present tag's
// capabilities.
func TestGetCapabilities_Success(t *testing.T) {
	mockTag := NewMockTag("04CAFE01")
	mockTag.IsConnected = true
	mockTag.MockCapabilities = &TagCapabilities{
		CanRead:          true,
		CanWrite:         true,
		CanLock:          true,
		SupportsPassword: true,
		MaxNDEFSize:      504,
		TagFamily:        "NTAG",
	}

	reader := newWriteTestReader(t, mockTag)

	caps, err := reader.GetCapabilities()
	if err != nil {
		t.Fatalf("GetCapabilities() failed: %v", err)
	}
	if !caps.CanWrite || !caps.CanLock || !caps.SupportsPassword {
		t.Errorf("unexpected capabilities: %+v", caps)
	}
	if caps.MaxNDEFSize != 504 {
		t.Errorf("expected MaxNDEFSize 504, got %d", caps.MaxNDEFSize)
	}
}

// TestGetCapabilities_MultipleCards verifies the query refuses to guess when
// more than one tag is present.
func TestGetCapabilities_MultipleCards(t *testing.T) {
	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}
	mockDevice := NewMockDevice()
	mockDevice.SetTags([]Tag{NewMockTag("04AAAA01"), NewMockTag("04BBBB02")})
	manager.MockDevice = mockDevice

	reader, err := NewNFCReader("mock:usb:001", manager, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to create NFCReader: %v", err)
	}
	t.Cleanup(reader.Close)
	time.Sleep(100 * time.Millisecond)

	if _, err := reader.GetCapabilities(); err == nil {
		t.Fatal("expected error when multiple cards are present")
	}
}

// TestCardCapabilities verifies a Card reports capabilities from its underlying
// tag, falling back to type-string inference when no tag is attached.
func TestCardCapabilities(t *testing.T) {
	mockTag := NewMockTag("04CCCC03")
	mockTag.MockCapabilities = &TagCapabilities{CanWrite: true, SupportsPassword: true}
	card := NewCard(mockTag)

	caps := card.Capabilities()
	if !caps.CanWrite || !caps.SupportsPassword {
		t.Errorf("expected tag-derived capabilities, got %+v", caps)
	}

	// With no underlying tag, capabilities are inferred from the type string.
	typed := &Card{Type: CardTypeNtag215}
	if !typed.Capabilities().SupportsPassword {
		t.Error("expected NTAG215 type string to infer password support")
	}
}
