package virtualnfc

import (
	"fmt"
	"regexp"
	"strings"
)

var validHexUID = regexp.MustCompile(`^[0-9A-F]+$`)

// ParseUID normalizes a UID from various formats to colon-separated uppercase
// hex. It accepts "04:AB:CD:EF", "04ABCDEF", "04 AB CD EF", and "04-AB-CD-EF",
// returning "04:AB:CD:EF". It errors on empty input, non-hex characters, or an
// odd number of hex digits.
func ParseUID(uid string) (string, error) {
	if uid == "" {
		return "", fmt.Errorf("empty UID")
	}

	cleaned := strings.ReplaceAll(uid, ":", "")
	cleaned = strings.ReplaceAll(cleaned, " ", "")
	cleaned = strings.ReplaceAll(cleaned, "-", "")
	cleaned = strings.ToUpper(cleaned)

	if !validHexUID.MatchString(cleaned) {
		return "", fmt.Errorf("UID contains invalid characters: %s", uid)
	}
	if len(cleaned)%2 != 0 {
		return "", fmt.Errorf("UID has odd number of hex characters: %s", uid)
	}
	if len(cleaned) < 2 {
		return "", fmt.Errorf("UID too short: %s", uid)
	}

	var result strings.Builder
	for i := 0; i < len(cleaned); i += 2 {
		if i > 0 {
			result.WriteByte(':')
		}
		result.WriteString(cleaned[i : i+2])
	}
	return result.String(), nil
}
