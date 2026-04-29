package deviceserver

import (
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
)

// Config holds configuration for the Device Server.
type Config struct {
	// Reader is the NFC reader instance (hardware NFC)
	Reader *nfc.NFCReader

	// DeviceManager manages external devices (phones, tablets, etc.)
	DeviceManager *remotenfc.Manager

	// Port is the HTTP/WebSocket port to listen on
	Port int

	// APISecret is the API secret required for non-loopback connections.
	// Empty means no auth (legacy / development mode).
	APISecret string

	// AllowedOrigins extends the default same-origin policy. Use ["*"]
	// to disable origin checking entirely (NOT recommended).
	AllowedOrigins []string

	// AllowedCardTypes limits which card types are accepted
	AllowedCardTypes map[string]bool

	// TLS configuration (optional)
	CertFile string // Path to TLS certificate file
	KeyFile  string // Path to TLS private key file
}

// TLSEnabled returns true if TLS is configured.
func (c Config) TLSEnabled() bool {
	return c.CertFile != "" && c.KeyFile != ""
}
