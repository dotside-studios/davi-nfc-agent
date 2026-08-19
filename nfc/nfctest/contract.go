package nfctest

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// AssertTagContract checks the promises every nfc.Tag makes, whatever backend
// built it: that it identifies itself, and that what it advertises matches what
// it does. Both backends run it, because a tag reached through a reader and a
// tag reached through a phone are the same thing to everything above them, and
// the abstraction is only worth having if that holds.
//
// The checks are:
//
//   - UID, type and technology are populated. The technology reaches clients
//     with every scan.
//   - Capabilities agree with the query methods (nfc.AssertCapabilitiesConsistent).
//   - An operation the tag says it does not support fails with a typed
//     not-supported error. The write path and the wire error mapping both
//     branch on that, so an untyped refusal is retried and reaches the client
//     as UNKNOWN_ERROR.
//   - Connect and Disconnect are safe to call.
//
// It therefore calls operations the tag claims not to support. Pass an
// emulated or stubbed tag, never one on real hardware: a tag that advertises
// wrongly would carry out the write or the lock it said it could not, and a
// lock cannot be undone.
func AssertTagContract(t *testing.T, tag nfc.Tag) {
	t.Helper()

	caps := nfc.GetTagCapabilities(tag)

	if tag.UID() == "" {
		t.Error("UID() is empty")
	}
	if tag.Type() == "" {
		t.Error("Type() is empty")
	}
	if caps.Technology == "" {
		t.Error("Capabilities().Technology is empty")
	}
	if !caps.CanRead {
		t.Error("Capabilities().CanRead is false; a tag that cannot be read has nothing to offer")
	}

	if err := nfc.AssertCapabilitiesConsistent(tag); err != nil {
		t.Error(err)
	}

	if !caps.CanTransceive {
		if _, err := tag.Transceive([]byte{0x00}); !nfc.IsNotSupportedError(err) {
			t.Errorf("Transceive with CanTransceive=false: err = %v, want a not-supported error", err)
		}
	}
	if !caps.CanWrite {
		if err := tag.WriteData([]byte{0x00}); !nfc.IsNotSupportedError(err) {
			t.Errorf("WriteData with CanWrite=false: err = %v, want a not-supported error", err)
		}
	}
	if !caps.CanLock {
		if err := tag.MakeReadOnly(); !nfc.IsNotSupportedError(err) {
			t.Errorf("MakeReadOnly with CanLock=false: err = %v, want a not-supported error", err)
		}
	}

	if err := tag.Connect(); err != nil {
		t.Errorf("Connect() = %v, want nil", err)
	}
	if err := tag.Disconnect(); err != nil {
		t.Errorf("Disconnect() = %v, want nil", err)
	}
}
