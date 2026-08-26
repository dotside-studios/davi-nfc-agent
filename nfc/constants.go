package nfc

// Manager type constants for identifying different manager implementations
const (
	ManagerTypeHardware   = "hardware"
	ManagerTypeSmartphone = "smartphone"
)

// Card type constants for card type identification and filtering
const (
	CardTypeMifareClassic1K  = "MIFARE Classic 1K"
	CardTypeMifareClassic4K  = "MIFARE Classic 4K"
	CardTypeMifareUltralight = "MIFARE Ultralight"
	CardTypeNtag213          = "NTAG213"
	CardTypeNtag215          = "NTAG215"
	CardTypeNtag216          = "NTAG216"
	CardTypeDesfire          = "DESFire"
	CardTypeType4            = "Type4"
)

// MIFARE Classic key type constants for authentication
const (
	// KeyTypeA is used for MIFARE Classic Key A authentication
	KeyTypeA = 0x60
	// KeyTypeB is used for MIFARE Classic Key B authentication
	KeyTypeB = 0x61
)

// Common MIFARE Classic keys
var (
	// KeyDefault is the factory default key (all 0xFF)
	KeyDefault = []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	// KeyNFCForum is the NFC Forum public key for NDEF
	KeyNFCForum = []byte{0xD3, 0xF7, 0xD3, 0xF7, 0xD3, 0xF7}
	// KeyMAD is the MAD (MIFARE Application Directory) key
	KeyMAD = []byte{0xA0, 0xA1, 0xA2, 0xA3, 0xA4, 0xA5}
)

// GetAllCardTypes returns all supported card type constants
func GetAllCardTypes() []string {
	return []string{
		CardTypeMifareClassic1K,
		CardTypeMifareClassic4K,
		CardTypeMifareUltralight,
		CardTypeNtag213,
		CardTypeNtag215,
		CardTypeNtag216,
		CardTypeDesfire,
		CardTypeType4,
	}
}

// RemoteManager is implemented by a manager whose devices are not readers
// attached to this machine. A phone reports the tags it scans over the device
// bridge, so it is never opened and polled the way a reader is: offering one
// as the agent's reader only produces a device that can never be connected.
//
// Optional: a manager that does not implement it manages local readers.
type RemoteManager interface {
	RemoteDevices() bool
}

// ReaderLister is implemented by a manager that holds others, so it can list
// only the devices eligible to be this agent's reader.
//
// Optional: a manager that does not implement it lists readers from ListDevices.
type ReaderLister interface {
	ListReaders() ([]string, error)
}

// ListReaders returns the devices that can serve as this agent's reader,
// falling back to every device a manager knows for one that draws no
// distinction.
func ListReaders(m Manager) ([]string, error) {
	if m == nil {
		return nil, nil
	}
	if lister, ok := m.(ReaderLister); ok {
		return lister.ListReaders()
	}
	if remote, ok := m.(RemoteManager); ok && remote.RemoteDevices() {
		return nil, nil
	}
	return m.ListDevices()
}
