package clientserver

import "github.com/dotside-studios/davi-nfc-agent/server"

// Config holds configuration for the client handling logic. The HTTP listener
// and TLS are owned by the unified server, so this carries only what the client
// handlers need.
type Config struct {
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
}
