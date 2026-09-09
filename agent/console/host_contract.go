//go:build !nowebui

package console

import (
	"github.com/dotside-studios/davi-nfc-agent/agent"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/logbuf"
)

// Host is what the console needs from the agent it administers. Stating it as
// one interface makes the complete list of what the console can do readable in
// one place.
type Host interface {
	Running() bool
	ConfigDir() string
	StartAgent() error
	StopAgent()
	QuitAgent()

	AvailableDevices() []string
	AllCardTypes() []string
	// CurrentCard reports the tag on the reader, if any.
	CurrentCard() (uid, cardType string, present bool)

	Port() int
	BootstrapPort() int
	CertFile() string
	TLSEnabled() bool
	LocalIPs() []string
	ClientCount() int
	Clients() []Client
	DisconnectClient(id string) error

	APISecret() string
	RotateAPISecret() (string, error)
	PublicKeyPin() string
	PairingPIN() string
	RotatePairingPIN() (string, error)
	CAInstalled() bool
	CAFingerprint() (string, error)
	// InstallCA puts a local authority in the system trust store, which is what
	// makes a browser accept the agent. Prompts for a password.
	InstallCA() error
	RegenerateCertificate() error

	PairedDevices() []PairedDevice
	RevokeDevice(id string) error
	RevokeAllDevices() error

	AllowedOrigins() []string
	BlockedOrigins() []string
	OriginCheckDisabled() bool
	AllowOrigin(origin string) error
	RevokeOrigin(origin string) error
	SetOriginCheckDisabled(bool)

	// Preferences is what the agent is set to, and the console's only source
	// for one, so the console cannot show a preference the agent is not using.
	Preferences() agent.Preferences

	// ApplyPreferences changes the agent and answers with what it holds
	// afterwards, which is not necessarily what was asked for.
	ApplyPreferences(mutate func(*agent.Preferences)) agent.Preferences
}

// PairedDevice is the console's view of a stored device credential.
type PairedDevice struct {
	ID       string
	Name     string
	Platform string
	PairedAt time.Time
	LastSeen time.Time
	Online   bool
}

// Client is the console's view of a connected client application.
type Client struct {
	ID          string
	Origin      string
	RemoteAddr  string
	UserAgent   string
	ConnectedAt time.Time
	Writes      int
	Locks       int
}

// serverConfig assembles a Server.
type serverConfig struct {
	// Host is the agent under administration. Required.
	Host Host

	// Logs is the ring the console tails. Nil disables the log views.
	Logs *logbuf.Ring

	Name    string
	Version string
	Dev     bool

	// BasePath is the prefix the control routes mount under; empty selects
	// DefaultBasePath. See [Config.BasePath].
	BasePath string

	// AllowSecretExchange mounts the POST /control/exchange route, which mints
	// a session against the agent's API secret. Off by default; a build with a
	// tray mints its session through the handoff instead.
	AllowSecretExchange bool

	// Secret reads the agent's API secret, live, for the exchange above. Nil is
	// treated as an empty secret, which the exchange refuses.
	Secret func() string
}
