package nfc

import (
	"strings"
	"testing"
)

// TestInferTagCapabilities_SupportsPassword verifies password capability is
// reported for NTAG21x and not for tag families that lack simple PWD support.
func TestInferTagCapabilities_SupportsPassword(t *testing.T) {
	supported := []string{"NTAG213", "NTAG215", "NTAG216"}
	for _, typ := range supported {
		if !InferTagCapabilities(typ).SupportsPassword {
			t.Errorf("%s: expected SupportsPassword=true", typ)
		}
	}

	unsupported := []string{"MIFARE Classic 1K", "MIFARE Ultralight", "MIFARE DESFire EV1", "Unknown"}
	for _, typ := range unsupported {
		if InferTagCapabilities(typ).SupportsPassword {
			t.Errorf("%s: expected SupportsPassword=false", typ)
		}
	}
}

// TestSetCardPassword_GatedForSupportedTag verifies that on a password-capable
// tag the operation reports it is not yet enabled (pending hardware validation)
// rather than attempting a destructive config write.
func TestSetCardPassword_GatedForSupportedTag(t *testing.T) {
	mockTag := NewMockTag("04AABBCC")
	mockTag.TagType = "NTAG215"
	mockTag.IsConnected = true

	reader := newWriteTestReader(t, mockTag)

	result, err := reader.SetCardPassword([]byte{1, 2, 3, 4}, PasswordOptions{})
	if err == nil {
		t.Fatal("expected SetCardPassword to be gated off")
	}
	if result != nil {
		t.Errorf("expected nil result, got %+v", result)
	}
	if !IsNotSupportedError(err) {
		t.Errorf("expected not-supported error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "hardware validation") {
		t.Errorf("expected 'pending hardware validation' detail, got: %v", err)
	}
}

// TestSetCardPassword_UnsupportedTagType verifies tags that lack password
// support report it clearly.
func TestSetCardPassword_UnsupportedTagType(t *testing.T) {
	mockTag := NewMockTag("04DDEEFF")
	mockTag.TagType = "MIFARE Classic 1K"
	mockTag.IsConnected = true

	reader := newWriteTestReader(t, mockTag)

	_, err := reader.SetCardPassword([]byte{1, 2, 3, 4}, PasswordOptions{})
	if err == nil {
		t.Fatal("expected error for tag without password support")
	}
	if !IsNotSupportedError(err) {
		t.Errorf("expected not-supported error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "does not support password") {
		t.Errorf("expected unsupported-tag detail, got: %v", err)
	}
}

// TestRemoveCardPassword_Gated verifies remove is gated like set.
func TestRemoveCardPassword_Gated(t *testing.T) {
	mockTag := NewMockTag("04112233")
	mockTag.TagType = "NTAG215"
	mockTag.IsConnected = true

	reader := newWriteTestReader(t, mockTag)

	_, err := reader.RemoveCardPassword([]byte{1, 2, 3, 4})
	if err == nil {
		t.Fatal("expected RemoveCardPassword to be gated off")
	}
	if !IsNotSupportedError(err) {
		t.Errorf("expected not-supported error, got: %v", err)
	}
}
