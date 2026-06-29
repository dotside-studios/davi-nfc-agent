package nfc

import "testing"

// minimalTag is a read-only tag that implements only the required identity and
// read methods, inheriting everything else from BaseTag. It also proves BaseTag
// is enough to satisfy the full Tag interface with no extra boilerplate.
type minimalTag struct {
	BaseTag
	data []byte
}

func (t *minimalTag) UID() string               { return "04AABBCC" }
func (t *minimalTag) Type() string              { return "MinimalTag" }
func (t *minimalTag) NumericType() int          { return 0 }
func (t *minimalTag) ReadData() ([]byte, error) { return t.data, nil }

// Compile-time assertion: embedding BaseTag + 4 methods satisfies Tag.
var _ Tag = (*minimalTag)(nil)

func TestBaseTagDefaults(t *testing.T) {
	tag := &minimalTag{data: []byte("hi")}

	if err := tag.Connect(); err != nil {
		t.Errorf("Connect() should be a no-op, got %v", err)
	}
	if err := tag.Disconnect(); err != nil {
		t.Errorf("Disconnect() should be a no-op, got %v", err)
	}

	if err := tag.WriteData([]byte("x")); !IsNotSupportedError(err) {
		t.Errorf("WriteData() should be NotSupported, got %v", err)
	}
	if _, err := tag.Transceive([]byte{0x01}); !IsNotSupportedError(err) {
		t.Errorf("Transceive() should be NotSupported, got %v", err)
	}
	if err := tag.MakeReadOnly(); !IsNotSupportedError(err) {
		t.Errorf("MakeReadOnly() should be NotSupported, got %v", err)
	}

	if w, err := tag.IsWritable(); err != nil || w {
		t.Errorf("IsWritable() should be (false, nil), got (%t, %v)", w, err)
	}
	if c, err := tag.CanMakeReadOnly(); err != nil || c {
		t.Errorf("CanMakeReadOnly() should be (false, nil), got (%t, %v)", c, err)
	}

	if data, err := tag.ReadData(); err != nil || string(data) != "hi" {
		t.Errorf("ReadData() = (%q, %v), want (\"hi\", nil)", data, err)
	}
}

func TestAssertCapabilitiesConsistent(t *testing.T) {
	// minimalTag: Capabilities inferred from "MinimalTag" -> CanLock=false,
	// and CanMakeReadOnly()=false. Consistent.
	if err := AssertCapabilitiesConsistent(&minimalTag{}); err != nil {
		t.Errorf("minimalTag should be consistent, got %v", err)
	}

	// A tag that claims CanLock=true but cannot actually make itself read-only.
	drift := &MockTag{
		TagUID:              "04",
		TagType:             "Custom",
		MockCapabilities:    &TagCapabilities{CanRead: true, CanLock: true},
		CanMakeReadOnlyFunc: func() (bool, error) { return false, nil },
	}
	if err := AssertCapabilitiesConsistent(drift); err == nil {
		t.Error("expected capability drift error when CanLock=true but CanMakeReadOnly()=false")
	}
}
