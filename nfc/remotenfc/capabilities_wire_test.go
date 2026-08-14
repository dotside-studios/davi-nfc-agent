package remotenfc

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
)

func TestTagUsesDeclaredCapabilities(t *testing.T) {
	tag, err := ConvertTagData(TagData{
		UID:        "04:A1:B2:C3",
		Technology: "ISO14443A",
		Type:       "NTAG215",
		Capabilities: &protocol.TagCapabilities{
			MemorySize:       540,
			MaxNDEFSize:      504,
			TagFamily:        "NTAG",
			SupportsPassword: true,
			SupportsNDEF:     true,
		},
	})
	if err != nil {
		t.Fatalf("ConvertTagData: %v", err)
	}

	caps := nfc.GetTagCapabilities(tag)

	if caps.MemorySize != 540 {
		t.Errorf("MemorySize = %d, want 540", caps.MemorySize)
	}
	if caps.MaxNDEFSize != 504 {
		t.Errorf("MaxNDEFSize = %d, want 504", caps.MaxNDEFSize)
	}
	if !caps.SupportsPassword {
		t.Error("SupportsPassword = false, want true")
	}
	if caps.TagFamily != "NTAG" {
		t.Errorf("TagFamily = %q, want NTAG", caps.TagFamily)
	}
	// Technology was not declared, so it falls back to the scan field.
	if caps.Technology != "ISO14443A" {
		t.Errorf("Technology = %q, want ISO14443A", caps.Technology)
	}
}

// stubWriter stands in for the device server's route back to a device.
type stubWriter struct {
	canWrite bool
	canLock  bool
	written  []byte
	locked   bool
	err      error
}

func (w *stubWriter) WriteTag(_, _ string, ndef []byte, _ nfc.WriteOptions) error {
	if w.err != nil {
		return w.err
	}
	w.written = ndef
	return nil
}

func (w *stubWriter) LockTag(_, _ string) error {
	if w.err != nil {
		return w.err
	}
	w.locked = true
	return nil
}

func (w *stubWriter) DeviceCanWrite(string) bool { return w.canWrite }
func (w *stubWriter) DeviceCanLock(string) bool  { return w.canLock }

func declaredTag(t *testing.T, caps *protocol.TagCapabilities, writer TagWriter) nfc.Tag {
	t.Helper()

	tag, err := ConvertTagDataWithWriter(TagData{
		UID:          "04:A1:B2:C3",
		Technology:   "ISO14443A",
		Type:         "NTAG215",
		Capabilities: caps,
	}, writer)
	if err != nil {
		t.Fatalf("ConvertTagDataWithWriter: %v", err)
	}
	return tag
}

// A tag with no route back to its device cannot write, whatever it declared.
func TestTagWithoutWriterIsReadOnly(t *testing.T) {
	tag := declaredTag(t, &protocol.TagCapabilities{
		CanWrite:      true,
		CanTransceive: true,
		CanLock:       true,
	}, nil)

	caps := nfc.GetTagCapabilities(tag)

	if caps.CanWrite {
		t.Error("CanWrite = true with no route back to the device")
	}
	if caps.CanLock {
		t.Error("CanLock = true with no route back to the device")
	}
	if caps.CanTransceive {
		t.Error("CanTransceive = true, want false while there is no command channel")
	}
	if !caps.CanRead {
		t.Error("CanRead = false, want true")
	}

	if err := nfc.AssertCapabilitiesConsistent(tag); err != nil {
		t.Errorf("capability drift: %v", err)
	}
}

func TestTagWritesThroughDevice(t *testing.T) {
	writer := &stubWriter{canWrite: true, canLock: true}
	tag := declaredTag(t, &protocol.TagCapabilities{CanWrite: true, CanLock: true}, writer)

	caps := nfc.GetTagCapabilities(tag)
	if !caps.CanWrite {
		t.Error("CanWrite = false for a writable tag on a connected device")
	}
	if !caps.CanLock {
		t.Error("CanLock = false for a lockable tag on a connected device")
	}

	if err := tag.WriteData([]byte{0xD1, 0x01, 0x01, 0x54}); err != nil {
		t.Fatalf("WriteData: %v", err)
	}
	if len(writer.written) != 4 {
		t.Errorf("device received %d bytes, want the encoded message", len(writer.written))
	}

	if err := tag.MakeReadOnly(); err != nil {
		t.Fatalf("MakeReadOnly: %v", err)
	}
	if !writer.locked {
		t.Error("MakeReadOnly did not reach the device")
	}

	if err := nfc.AssertCapabilitiesConsistent(tag); err != nil {
		t.Errorf("capability drift: %v", err)
	}
}

// A tag whose device declared write support but has since gone away must stop
// advertising it, and refuse the write rather than blocking on a dead session.
func TestTagStopsClaimingWriteWhenDeviceGone(t *testing.T) {
	writer := &stubWriter{canWrite: false, canLock: false}
	tag := declaredTag(t, &protocol.TagCapabilities{CanWrite: true, CanLock: true}, writer)

	caps := nfc.GetTagCapabilities(tag)
	if caps.CanWrite || caps.CanLock {
		t.Error("capabilities outlived the device session")
	}

	if err := tag.WriteData([]byte{0x00}); !nfc.IsNotSupportedError(err) {
		t.Errorf("WriteData error = %v, want a not-supported error", err)
	}
	if err := tag.MakeReadOnly(); !nfc.IsNotSupportedError(err) {
		t.Errorf("MakeReadOnly error = %v, want a not-supported error", err)
	}

	if err := nfc.AssertCapabilitiesConsistent(tag); err != nil {
		t.Errorf("capability drift: %v", err)
	}
}

// A tag the device already reported as locked is not writable, even though the
// device itself can write.
func TestTagReadOnlyDeclarationWins(t *testing.T) {
	writer := &stubWriter{canWrite: true, canLock: true}
	tag := declaredTag(t, &protocol.TagCapabilities{
		CanWrite:   true,
		CanLock:    true,
		IsReadOnly: true,
	}, writer)

	caps := nfc.GetTagCapabilities(tag)
	if caps.CanWrite {
		t.Error("CanWrite = true for a tag the device reported as read-only")
	}

	if err := nfc.AssertCapabilitiesConsistent(tag); err != nil {
		t.Errorf("capability drift: %v", err)
	}
}

func TestTagWithoutDeclaredCapabilities(t *testing.T) {
	tag, err := ConvertTagData(TagData{
		UID:        "04:A1:B2:C3",
		Technology: "ISO14443A",
		Type:       "MIFARE Classic 1K",
	})
	if err != nil {
		t.Fatalf("ConvertTagData: %v", err)
	}

	caps := nfc.GetTagCapabilities(tag)

	if caps.TagFamily != "MIFARE Classic 1K" {
		t.Errorf("TagFamily = %q, want the scan type", caps.TagFamily)
	}
	if caps.Technology != "ISO14443A" {
		t.Errorf("Technology = %q, want ISO14443A", caps.Technology)
	}
	if !caps.CanRead {
		t.Error("CanRead = false, want true")
	}
}

func TestDeviceReportsDeclaredTagTypes(t *testing.T) {
	dev := NewDevice("dev1", DeviceRegistrationRequest{
		DeviceName: "PN532 Reader",
		Platform:   "android",
		Capabilities: DeviceCapabilities{
			NFCType:           "nfca",
			DeviceType:        "pn532-serial",
			SupportedTagTypes: []string{"MIFARE Classic", "NTAG", "ISO14443-4"},
		},
	})

	if got := dev.DeviceType(); got != "pn532-serial" {
		t.Errorf("DeviceType = %q, want pn532-serial", got)
	}

	types := dev.SupportedTagTypes()
	if len(types) != 3 {
		t.Fatalf("SupportedTagTypes = %v, want 3 entries", types)
	}

	// The accessor must not hand out the device's own slice.
	types[0] = "mutated"
	if dev.SupportedTagTypes()[0] != "MIFARE Classic" {
		t.Error("SupportedTagTypes returned an aliased slice")
	}
}

// A v0 device declares only nfcType and no deviceType; both fall back.
func TestDeviceCapabilityFallbacks(t *testing.T) {
	dev := NewDevice("dev2", DeviceRegistrationRequest{
		DeviceName:   "Legacy Phone",
		Platform:     "ios",
		Capabilities: DeviceCapabilities{CanRead: true, NFCType: "corenfc"},
	})

	if got := dev.DeviceType(); got != "smartphone" {
		t.Errorf("DeviceType = %q, want smartphone", got)
	}
	if got := dev.SupportedTagTypes(); len(got) != 1 || got[0] != "corenfc" {
		t.Errorf("SupportedTagTypes = %v, want [corenfc]", got)
	}
}
