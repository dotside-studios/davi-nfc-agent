package nfctest

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/ev2"
)

// Locking a DESFire rewrites the NDEF file's access rights: nothing may write
// it, and nothing may change that again. The content stays readable.
func TestDESFireLock_LeavesTheFileReadableAndUnwritable(t *testing.T) {
	tag := DESFire("04DE5F1RE0").WithText("hello").Tag()

	if _, err := tag.ReadData(); err != nil {
		t.Fatalf("ReadData: %v", err)
	}
	if can, err := tag.CanMakeReadOnly(); err != nil || !can {
		t.Fatalf("CanMakeReadOnly() = (%v, %v), want (true, nil)", can, err)
	}

	if err := tag.MakeReadOnly(); err != nil {
		t.Fatalf("MakeReadOnly: %v", err)
	}

	if _, err := tag.ReadData(); err != nil {
		t.Errorf("ReadData after locking: %v, want the content still readable", err)
	}
	if err := tag.WriteData([]byte{0xD1, 0x01, 0x01, 0x54, 0x00}); !nfc.IsReadOnlyError(err) {
		t.Errorf("WriteData after locking: %v, want a read-only error", err)
	}

	caps := nfc.GetTagCapabilities(tag)
	if caps.CanWrite || !caps.IsReadOnly {
		t.Errorf("CanWrite = %v, IsReadOnly = %v, want false and true", caps.CanWrite, caps.IsReadOnly)
	}
	if err := nfc.AssertCapabilitiesConsistent(tag); err != nil {
		t.Error(err)
	}
}

// The lock denies the change right as well, so nothing reopens the file. A
// second lock has nothing left to work with.
func TestDESFireLock_CannotBeUndone(t *testing.T) {
	tag := DESFire("04DE5F1RE0").WithText("hello").Tag()

	if _, err := tag.ReadData(); err != nil {
		t.Fatalf("ReadData: %v", err)
	}
	if err := tag.MakeReadOnly(); err != nil {
		t.Fatalf("MakeReadOnly: %v", err)
	}

	if can, err := tag.CanMakeReadOnly(); err != nil || can {
		t.Errorf("CanMakeReadOnly() after locking = (%v, %v), want (false, nil)", can, err)
	}
	if err := tag.MakeReadOnly(); !nfc.IsNotSupportedError(err) {
		t.Errorf("MakeReadOnly twice: %v, want a not-supported error", err)
	}
	if caps := nfc.GetTagCapabilities(tag); caps.CanLock {
		t.Error("CanLock is still true after the change right was denied")
	}
}

// A card whose change right names a key is locked through a session under that
// key, with the settings enciphered.
func TestDESFireLock_ThroughASession(t *testing.T) {
	tag := DESFire("04DE5F1RE0").WithText("hello").
		ChangeKeyProtected(ev2.CommMAC).WithDESFireKeys(heldKeys()).Tag()

	if _, err := tag.ReadData(); err != nil {
		t.Fatalf("ReadData: %v", err)
	}
	if can, err := tag.CanMakeReadOnly(); err != nil || !can {
		t.Fatalf("CanMakeReadOnly() = (%v, %v), want (true, nil) with the change key held", can, err)
	}

	if err := tag.MakeReadOnly(); err != nil {
		t.Fatalf("MakeReadOnly: %v", err)
	}
	if err := tag.WriteData([]byte{0xD1}); !nfc.IsReadOnlyError(err) {
		t.Errorf("WriteData after locking: %v, want a read-only error", err)
	}
}

// Without the change key there is nothing to lock with, and the attempt is
// refused rather than sent.
func TestDESFireLock_RefusedWithoutTheChangeKey(t *testing.T) {
	tag := DESFire("04DE5F1RE0").WithText("hello").ChangeKeyProtected(ev2.CommMAC).Tag()

	if _, err := tag.ReadData(); err != nil {
		t.Fatalf("ReadData: %v", err)
	}
	if can, err := tag.CanMakeReadOnly(); err != nil || can {
		t.Errorf("CanMakeReadOnly() = (%v, %v), want (false, nil)", can, err)
	}
	if err := tag.MakeReadOnly(); !nfc.IsNotSupportedError(err) {
		t.Errorf("MakeReadOnly: %v, want a not-supported error", err)
	}
	if err := nfc.AssertCapabilitiesConsistent(tag); err != nil {
		t.Error(err)
	}
}
