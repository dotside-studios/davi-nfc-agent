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

// ListReaders names the devices this agent can open and read from, which is
// what the supervisor opens and what a reader picker offers. Devices that
// report their own scans are left out; connecting to one cannot succeed.
func ListReaders(m Manager) ([]string, error) {
	if m == nil {
		return nil, nil
	}

	listings, err := m.Devices()
	if err != nil {
		return nil, err
	}

	var readers []string
	for _, listing := range listings {
		if listing.Capabilities.CanPoll {
			readers = append(readers, listing.Path)
		}
	}
	return readers, nil
}
