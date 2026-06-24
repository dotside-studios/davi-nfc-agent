package nfc

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// newWriteTestReader builds an NFCReader wired to a mock manager/device that
// presents the given tag, waiting for the initial connection to settle.
func newWriteTestReader(t *testing.T, tag Tag) *NFCReader {
	t.Helper()

	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}

	mockDevice := NewMockDevice()
	mockDevice.SetTags([]Tag{tag})
	manager.MockDevice = mockDevice

	reader, err := NewNFCReader("mock:usb:001", manager, 5*time.Second)
	if err != nil {
		t.Fatalf("Failed to create NFCReader: %v", err)
	}
	t.Cleanup(reader.Close)

	// Give the reader time to establish the initial connection.
	time.Sleep(100 * time.Millisecond)
	return reader
}

func textMessage(content string) *NDEFMessage {
	return (&NDEFMessageBuilder{
		Records: []NDEFRecordBuilder{&NDEFText{Content: content, Language: "en"}},
	}).MustBuild()
}

// TestWriteMessageWithResult_VerifiesByDefault confirms that a successful write
// is verified by read-back and reports a populated WriteResult.
func TestWriteMessageWithResult_VerifiesByDefault(t *testing.T) {
	mockTag := NewMockClassicTag("04A1B2C3")
	mockTag.IsConnected = true

	reader := newWriteTestReader(t, mockTag)

	result, err := reader.WriteMessageWithResult(textMessage("Hello World"), WriteOptions{
		Overwrite: true,
		Index:     -1,
	})
	if err != nil {
		t.Fatalf("WriteMessageWithResult() failed: %v", err)
	}
	if result == nil {
		t.Fatal("expected a WriteResult, got nil")
	}
	if !result.Verified {
		t.Error("expected write to be verified by default")
	}
	if result.Attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", result.Attempts)
	}
	if result.BytesWritten == 0 {
		t.Error("expected BytesWritten > 0")
	}
	if result.UID != "04A1B2C3" {
		t.Errorf("expected UID 04A1B2C3, got %q", result.UID)
	}
}

// TestWriteMessageWithResult_RetriesThenSucceeds confirms that a transient write
// failure is retried and the eventual success is reported with the attempt count.
func TestWriteMessageWithResult_RetriesThenSucceeds(t *testing.T) {
	mockTag := NewMockClassicTag("04D1D2D3")
	mockTag.IsConnected = true

	writeCalls := 0
	mockTag.WriteDataFunc = func(_ []byte) error {
		writeCalls++
		if writeCalls == 1 {
			return errors.New("transient RF glitch")
		}
		return nil
	}

	reader := newWriteTestReader(t, mockTag)

	result, err := reader.WriteMessageWithResult(textMessage("retry me"), WriteOptions{
		Overwrite: true,
		Index:     -1,
	})
	if err != nil {
		t.Fatalf("expected eventual success, got error: %v", err)
	}
	if !result.Verified {
		t.Error("expected verified write after retry")
	}
	if result.Attempts != 2 {
		t.Errorf("expected success on attempt 2, got %d", result.Attempts)
	}
}

// TestWriteMessageWithResult_VerificationMismatchFails confirms that a write
// whose read-back never matches is retried and ultimately fails as a write error.
func TestWriteMessageWithResult_VerificationMismatchFails(t *testing.T) {
	mockTag := NewMockClassicTag("04E1E2E3")
	mockTag.IsConnected = true

	// Writes "succeed" at the tag level, but the read-back is always corrupt,
	// so verification can never pass.
	mockTag.ReadDataFunc = func() ([]byte, error) {
		return []byte{0xDE, 0xAD, 0xBE, 0xEF}, nil
	}

	reader := newWriteTestReader(t, mockTag)

	result, err := reader.WriteMessageWithResult(textMessage("never verifies"), WriteOptions{
		Overwrite:        true,
		Index:            -1,
		MaxWriteAttempts: 2,
	})
	if err == nil {
		t.Fatal("expected write to fail on verification mismatch")
	}
	if result != nil {
		t.Errorf("expected nil result on failure, got %+v", result)
	}
	if !IsWriteError(err) {
		t.Errorf("expected a write error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "verification") {
		t.Errorf("expected verification mismatch detail in error, got: %v", err)
	}
}

// TestWriteMessageWithResult_CapacityExceeded confirms the pre-flight capacity
// check rejects oversized messages without attempting a write.
func TestWriteMessageWithResult_CapacityExceeded(t *testing.T) {
	// MIFARE Ultralight reports a 46-byte usable NDEF capacity.
	mockTag := NewMockTag("0411223344")
	mockTag.TagType = "MIFARE Ultralight"
	mockTag.IsConnected = true

	writeAttempted := false
	mockTag.WriteDataFunc = func(_ []byte) error {
		writeAttempted = true
		return nil
	}

	reader := newWriteTestReader(t, mockTag)

	// ~100 characters easily exceeds 46 bytes once NDEF-encoded.
	result, err := reader.WriteMessageWithResult(textMessage(strings.Repeat("A", 100)), WriteOptions{
		Overwrite: true,
		Index:     -1,
	})
	if err == nil {
		t.Fatal("expected capacity-exceeded error")
	}
	if result != nil {
		t.Errorf("expected nil result on capacity failure, got %+v", result)
	}
	if !IsCapacityExceededError(err) {
		t.Errorf("expected capacity-exceeded error, got: %v", err)
	}
	if writeAttempted {
		t.Error("expected no write to be attempted when capacity is exceeded")
	}
}

// TestWriteMessageWithResult_SkipVerify confirms verification can be disabled,
// in which case the result is reported as unverified.
func TestWriteMessageWithResult_SkipVerify(t *testing.T) {
	mockTag := NewMockClassicTag("04F1F2F3")
	mockTag.IsConnected = true

	reader := newWriteTestReader(t, mockTag)

	result, err := reader.WriteMessageWithResult(textMessage("unverified write"), WriteOptions{
		Overwrite:  true,
		Index:      -1,
		SkipVerify: true,
	})
	if err != nil {
		t.Fatalf("WriteMessageWithResult() failed: %v", err)
	}
	if result.Verified {
		t.Error("expected Verified=false when verification is skipped")
	}
	if result.Attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", result.Attempts)
	}
}
