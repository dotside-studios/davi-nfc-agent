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

// A device may declare more than the bridge can currently route. Operations
// still go through the WebSocket protocol rather than the Tag interface, so the
// operation bits must stay off or they drift from actual behavior.
func TestTagDoesNotClaimUnroutedOperations(t *testing.T) {
	tag, err := ConvertTagData(TagData{
		UID:        "04:A1:B2:C3",
		Technology: "ISO14443A",
		Type:       "NTAG215",
		Capabilities: &protocol.TagCapabilities{
			CanWrite:      true,
			CanTransceive: true,
			CanLock:       true,
		},
	})
	if err != nil {
		t.Fatalf("ConvertTagData: %v", err)
	}

	caps := nfc.GetTagCapabilities(tag)

	if caps.CanWrite {
		t.Error("CanWrite = true, want false while writes bypass the Tag interface")
	}
	if caps.CanTransceive {
		t.Error("CanTransceive = true, want false while there is no command channel")
	}
	if caps.CanLock {
		t.Error("CanLock = true, want false while locking bypasses the Tag interface")
	}
	if !caps.CanRead {
		t.Error("CanRead = false, want true")
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
