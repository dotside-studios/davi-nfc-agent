package clientserver

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
}
