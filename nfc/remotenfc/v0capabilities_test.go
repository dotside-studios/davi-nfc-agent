package remotenfc

import (
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

// capableDevice is a route whose device declares everything, so what a tag
// reports is decided by the tag's own declaration and nothing else.
type capableDevice struct{ write, lock, transceive bool }

func (d capableDevice) writeTag(_, _ string, _ []byte, _ nfc.WriteOptions) error { return nil }
func (d capableDevice) lockTag(_, _ string) error                                { return nil }
func (d capableDevice) transceiveTag(_, _ string, _ []byte, _ bool) ([]byte, error) {
	return nil, nil
}
func (d capableDevice) deviceCanWrite(string) bool      { return d.write }
func (d capableDevice) deviceCanLock(string) bool       { return d.lock }
func (d capableDevice) deviceCanTransceive(string) bool { return d.transceive }

var everything = capableDevice{write: true, lock: true, transceive: true}

// TestUndeclaredCapabilitiesDeferToTheDevice is the case the wire protocol
// documents: "When omitted the agent infers them from Type, which is all a v0
// device allows." A v0 device cannot describe a tag it scanned, so a tag that
// declared nothing is unknown rather than incapable, and the device's own
// declaration decides.
func TestUndeclaredCapabilitiesDeferToTheDevice(t *testing.T) {
	caps := nfc.GetTagCapabilities(declaredTag(t, nil, everything))

	if !caps.CanWrite {
		t.Error("a tag that declared nothing, held by a device that can write, is reported unwritable")
	}
	if !caps.CanLock {
		t.Error("a tag that declared nothing, held by a device that can lock, is reported unlockable")
	}
	if !caps.CanTransceive {
		t.Error("a tag that declared nothing, held by a device that can exchange, is reported unable to")
	}
}

// TestUndeclaredCapabilitiesStillNeedTheDevice keeps that fallback honest. It
// defers to the device rather than assuming, so a device that cannot do the
// thing still refuses.
func TestUndeclaredCapabilitiesStillNeedTheDevice(t *testing.T) {
	caps := nfc.GetTagCapabilities(declaredTag(t, nil, capableDevice{}))

	if caps.CanWrite {
		t.Error("a device that declared no write capability holds a tag reported as writable")
	}
	if caps.CanLock {
		t.Error("a device that declared no lock capability holds a tag reported as lockable")
	}
	if caps.CanTransceive {
		t.Error("a device that declared no exchange capability holds a tag reported as able to")
	}
}

// TestUndeclaredWriteReachesTheDevice is the same fact at the operation rather
// than the report: the write must leave the agent instead of being refused here.
func TestUndeclaredWriteReachesTheDevice(t *testing.T) {
	tag := declaredTag(t, nil, everything)

	if err := tag.WriteData([]byte{0x03, 0x00}); err != nil {
		t.Errorf("WriteData on an undeclared tag = %v, want it routed to the device", err)
	}
}

// TestDeclaredIncapabilityIsHonoured is the other side of the distinction. A
// tag that declared its capabilities and said no is information, not the
// absence of it, and is refused however capable the device is.
func TestDeclaredIncapabilityIsHonoured(t *testing.T) {
	tag := declaredTag(t, &protocol.TagCapabilities{CanRead: true}, everything)

	if nfc.GetTagCapabilities(tag).CanWrite {
		t.Error("a tag that declared it cannot be written is reported as writable")
	}
	if err := tag.WriteData([]byte{0x03, 0x00}); !nfc.IsNotSupportedError(err) {
		t.Errorf("WriteData = %v, want a not-supported error", err)
	}
}

// TestDeclaredReadOnlyIsHonoured covers a tag that can be written in general
// but has since been locked, which the device reports per scan.
func TestDeclaredReadOnlyIsHonoured(t *testing.T) {
	tag := declaredTag(t, &protocol.TagCapabilities{
		CanRead: true, CanWrite: true, CanLock: true, IsReadOnly: true,
	}, everything)

	caps := nfc.GetTagCapabilities(tag)
	if caps.CanWrite {
		t.Error("a tag declared read-only is reported as writable")
	}
	if caps.CanLock {
		t.Error("a tag declared read-only is reported as lockable")
	}
	if err := tag.WriteData([]byte{0x03, 0x00}); !nfc.IsNotSupportedError(err) {
		t.Errorf("WriteData = %v, want a not-supported error", err)
	}
}

// TestUndeclaredIsNotReadOnly pins the distinction the other way: saying
// nothing is not a claim that the tag is locked.
func TestUndeclaredIsNotReadOnly(t *testing.T) {
	if nfc.GetTagCapabilities(declaredTag(t, nil, everything)).IsReadOnly {
		t.Error("a tag that declared nothing is reported read-only")
	}
}

// TestSilenceAboutItselfIsNotARefusal is the distinction the device-level
// declaration could not make while it was a value on the wire. An omitted
// capabilities block and one of all falses arrived identically, so a device
// that described the tags it scans while saying nothing about itself had every
// one of those tags reported incapable.
//
// A bridge carries more than smartphones, and describing a tag is saying more
// than a device that cannot, not less. Silence is now unknown, and the
// operation goes out to the only party that can answer it.
func TestSilenceAboutItselfIsNotARefusal(t *testing.T) {
	m := NewManager(30 * time.Second)
	defer m.Close()

	silent, err := m.RegisterDevice(DeviceRegistrationRequest{
		DeviceName: "Bridge that describes its tags",
		Platform:   "web",
	})
	if err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}
	// Reachability is a map lookup, so a bare entry is enough to stand for a
	// live session. Removed before Close, which would otherwise close it.
	m.addSession(silent.DeviceID(), nil)
	defer m.removeSession(silent.DeviceID())

	if _, declared := silent.DeclaredCapabilities(); declared {
		t.Error("a device that sent no capabilities block reads as having declared one")
	}
	if !m.deviceCanWrite(silent.DeviceID()) {
		t.Error("a device that said nothing about itself refuses a write it never declined")
	}
	if !m.deviceCanLock(silent.DeviceID()) {
		t.Error("a device that said nothing about itself refuses a lock it never declined")
	}

	// The other half: a device that did describe itself is taken at its word,
	// which is what the pointer buys. Refusing here is a claim it made.
	declined, err := m.RegisterDevice(DeviceRegistrationRequest{
		DeviceName:   "Reader that only reads",
		Platform:     "web",
		Capabilities: &DeviceCapabilities{CanRead: true},
	})
	if err != nil {
		t.Fatalf("RegisterDevice: %v", err)
	}
	m.addSession(declined.DeviceID(), nil)
	defer m.removeSession(declined.DeviceID())

	if _, declared := declined.DeclaredCapabilities(); !declared {
		t.Error("a device that sent a capabilities block reads as having sent none")
	}
	if m.deviceCanWrite(declined.DeviceID()) {
		t.Error("a device that declared it cannot write is reported able to")
	}
}
