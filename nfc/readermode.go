package nfc

// The names a ReaderMode travels under: in the control center's JSON, in the
// tray's menu, and anywhere else it leaves the process.
const (
	ModeNameReadWrite = "readwrite"
	ModeNameReadOnly  = "read"
	ModeNameWriteOnly = "write"
)

// String is the name this mode travels under.
func (m ReaderMode) String() string {
	switch m {
	case ModeReadOnly:
		return ModeNameReadOnly
	case ModeWriteOnly:
		return ModeNameWriteOnly
	default:
		return ModeNameReadWrite
	}
}

// ParseReaderMode reads a mode name, falling back to read/write. An unknown
// name leaves the reader usable rather than refusing every operation.
func ParseReaderMode(name string) ReaderMode {
	switch name {
	case ModeNameReadOnly:
		return ModeReadOnly
	case ModeNameWriteOnly:
		return ModeWriteOnly
	default:
		return ModeReadWrite
	}
}
