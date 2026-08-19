package protocol

import (
	"fmt"
	"regexp"
	"strings"
)

// ParseUID normalizes a UID from various formats to colon-separated uppercase hex.
// Supports: "04:AB:CD:EF", "04ABCDEF", "04 AB CD EF", "04-AB-CD-EF"
// Returns: normalized colon-separated uppercase hex (e.g., "04:AB:CD:EF")
func ParseUID(uid string) (string, error) {
	if uid == "" {
		return "", fmt.Errorf("empty UID")
	}

	// Remove common separators and spaces
	cleaned := strings.ReplaceAll(uid, ":", "")
	cleaned = strings.ReplaceAll(cleaned, " ", "")
	cleaned = strings.ReplaceAll(cleaned, "-", "")
	cleaned = strings.ToUpper(cleaned)

	// Validate hex characters
	validHex := regexp.MustCompile(`^[0-9A-F]+$`)
	if !validHex.MatchString(cleaned) {
		return "", fmt.Errorf("UID contains invalid characters: %s", uid)
	}

	// UID length should be even (each byte = 2 hex chars)
	if len(cleaned)%2 != 0 {
		return "", fmt.Errorf("UID has odd number of hex characters: %s", uid)
	}

	// Typical NFC UID lengths: 4, 7, or 10 bytes (8, 14, or 20 hex chars)
	// But we'll accept any even length
	if len(cleaned) < 2 {
		return "", fmt.Errorf("UID too short: %s", uid)
	}

	// Format as colon-separated pairs
	var result strings.Builder
	for i := 0; i < len(cleaned); i += 2 {
		if i > 0 {
			result.WriteByte(':')
		}
		result.WriteString(cleaned[i : i+2])
	}

	return result.String(), nil
}
