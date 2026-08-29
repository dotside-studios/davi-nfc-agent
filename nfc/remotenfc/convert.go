package remotenfc

import "github.com/dotside-studios/davi-nfc-agent/nfc/virtualnfc"

// ParseUID normalizes a UID from various formats to colon-separated uppercase hex.
// Supports: "04:AB:CD:EF", "04ABCDEF", "04 AB CD EF", "04-AB-CD-EF".
// Returns: normalized colon-separated uppercase hex (e.g., "04:AB:CD:EF").
func ParseUID(uid string) (string, error) {
	return virtualnfc.ParseUID(uid)
}
