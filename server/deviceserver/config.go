package deviceserver

import (
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

// Config holds configuration for the device handling logic. The HTTP listener
// and TLS are owned by the unified server, so this carries only what the device
// handlers need.
type Config struct {
	// Reader is the NFC reader instance (hardware NFC)
	Reader *nfc.NFCReader

	// DeviceManager is the driver serving external devices (phones, tablets).
	// Nil when none are configured.
	DeviceManager *remotenfc.Manager

	// APISecret is the API secret required for non-loopback connections.
	// Empty means no auth (legacy / development mode).
	APISecret string

	// AllowedOrigins extends the default same-origin policy. Use ["*"]
	// to disable origin checking entirely (NOT recommended).
	AllowedOrigins []string

	// OriginPolicy, when set, decides origin admission instead of
	// AllowedOrigins and is told about rejections.
	OriginPolicy server.OriginPolicy

	// TokenVerifier recognizes per-device credentials issued at pairing, so a
	// device can be admitted -- and revoked -- independently of the shared
	// secret.
	TokenVerifier server.TokenVerifier

	// RequirePairedDevice admits only devices holding a credential issued at
	// pairing, withdrawing both the shared secret and the loopback bypass for
	// device connections. Browser clients are unaffected -- a browser cannot
	// pair, and is gated by the origin allowlist.
	RequirePairedDevice bool

	// PublicKeyPin is reported to devices so they can recognize this agent on
	// later connections without a certificate authority. Empty when the agent
	// is not using a certificate it generated itself.
	PublicKeyPin string

	// AllowedCardTypes limits which card types are accepted
	AllowedCardTypes map[string]bool
}
