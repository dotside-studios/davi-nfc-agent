package nfc

import (
	"errors"
	"testing"
)

// TestLockCard_Success verifies a standalone lock makes a lockable tag
// read-only and reports it.
func TestLockCard_Success(t *testing.T) {
	mockTag := NewMockClassicTag("04A1B2C3")
	mockTag.IsConnected = true

	reader := newWriteTestReader(t, mockTag)

	result, err := reader.LockCard()
	if err != nil {
		t.Fatalf("LockCard() failed: %v", err)
	}
	if result == nil || !result.Locked {
		t.Fatalf("expected Locked result, got %+v", result)
	}
	if result.UID != "04A1B2C3" {
		t.Errorf("expected UID 04A1B2C3, got %q", result.UID)
	}
	if !mockTag.IsReadOnly {
		t.Error("expected tag to be read-only after LockCard()")
	}
}

// TestLockCard_NotSupported verifies a tag that cannot be locked surfaces a
// not-supported error.
func TestLockCard_NotSupported(t *testing.T) {
	mockTag := NewMockClassicTag("04D1D2D3")
	mockTag.IsConnected = true
	mockTag.MakeReadOnlyError = NewNotSupportedError("MakeReadOnly")

	reader := newWriteTestReader(t, mockTag)

	result, err := reader.LockCard()
	if err == nil {
		t.Fatal("expected LockCard() to fail for unsupported tag")
	}
	if result != nil {
		t.Errorf("expected nil result, got %+v", result)
	}
	if !IsNotSupportedError(err) {
		t.Errorf("expected not-supported error, got: %v", err)
	}
}

// TestWriteMessageWithResult_WithLock verifies write-then-lock succeeds and
// reports Locked in the result.
func TestWriteMessageWithResult_WithLock(t *testing.T) {
	mockTag := NewMockClassicTag("04E1E2E3")
	mockTag.IsConnected = true

	reader := newWriteTestReader(t, mockTag)

	result, err := reader.WriteMessageWithResult(textMessage("lock me"), WriteOptions{
		Overwrite: true,
		Index:     -1,
		Lock:      true,
	})
	if err != nil {
		t.Fatalf("WriteMessageWithResult() failed: %v", err)
	}
	if !result.Verified {
		t.Error("expected verified write")
	}
	if !result.Locked {
		t.Error("expected Locked=true in result")
	}
	if !mockTag.IsReadOnly {
		t.Error("expected tag to be read-only after write+lock")
	}
}

// TestWriteMessageWithResult_LockFailurePropagates verifies that a write which
// succeeds but whose subsequent lock fails is reported as an error.
func TestWriteMessageWithResult_LockFailurePropagates(t *testing.T) {
	mockTag := NewMockClassicTag("04F1F2F3")
	mockTag.IsConnected = true
	mockTag.MakeReadOnlyError = errors.New("hardware lock glitch")

	reader := newWriteTestReader(t, mockTag)

	result, err := reader.WriteMessageWithResult(textMessage("lock fails"), WriteOptions{
		Overwrite: true,
		Index:     -1,
		Lock:      true,
	})
	if err == nil {
		t.Fatal("expected an error when the lock step fails")
	}
	if result != nil {
		t.Errorf("expected nil result on lock failure, got %+v", result)
	}
}
