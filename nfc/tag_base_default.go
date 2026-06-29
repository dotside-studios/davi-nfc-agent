package nfc

// BaseTag provides default implementations of the optional Tag behaviors so
// custom tag types only need to implement the parts they actually support.
//
// Embed BaseTag in your tag struct and override the methods your tag supports.
// The defaults are safe: connection management is a no-op, and
// write/transceive/lock operations report "not supported". You still must
// implement the universally-required identity and read methods yourself, since
// no sensible default exists for them:
//
//   - UID() string
//   - Type() string
//   - NumericType() int
//   - ReadData() ([]byte, error)
//
// This mirrors the capability-based philosophy: advertise what you support via
// Capabilities() (implementing TagCapabilityProvider) and only override the
// methods backing those capabilities.
//
// Example — a read-only tag needs four methods, not eleven:
//
//	type MyTag struct {
//	    nfc.BaseTag
//	    uid  string
//	    data []byte
//	}
//
//	func (t *MyTag) UID() string               { return t.uid }
//	func (t *MyTag) Type() string              { return "MyTag" }
//	func (t *MyTag) NumericType() int          { return 0 }
//	func (t *MyTag) ReadData() ([]byte, error) { return t.data, nil }
//	// Connect/Disconnect/WriteData/Transceive/IsWritable/CanMakeReadOnly/
//	// MakeReadOnly are inherited from BaseTag.
type BaseTag struct{}

func (BaseTag) Connect() error    { return nil }
func (BaseTag) Disconnect() error { return nil }

func (BaseTag) WriteData(data []byte) error { return NewNotSupportedError("WriteData") }

func (BaseTag) Transceive(data []byte) ([]byte, error) {
	return nil, NewNotSupportedError("Transceive")
}

func (BaseTag) IsWritable() (bool, error)      { return false, nil }
func (BaseTag) CanMakeReadOnly() (bool, error) { return false, nil }
func (BaseTag) MakeReadOnly() error            { return NewNotSupportedError("MakeReadOnly") }
