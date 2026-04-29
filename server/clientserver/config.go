package clientserver

// Config holds configuration for the Client Server.
type Config struct {
	// Port is the HTTP/WebSocket port to listen on
	Port int

	// APISecret is the API secret required for non-loopback connections.
	// Empty means no auth (legacy / development mode).
	APISecret string

	// AllowedOrigins extends the default same-origin policy. Use ["*"]
	// to disable origin checking entirely (NOT recommended).
	AllowedOrigins []string

	// TLS configuration (optional)
	CertFile string // Path to TLS certificate file
	KeyFile  string // Path to TLS private key file
}

// TLSEnabled returns true if TLS is configured.
func (c Config) TLSEnabled() bool {
	return c.CertFile != "" && c.KeyFile != ""
}
