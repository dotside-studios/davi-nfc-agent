// Package webui serves the agent's control center: a privileged HTTP API under
// /control and the console that drives it, whose source lives in frontend/ and
// whose build is embedded by embed.go.
//
// The package depends on no agent internals. Everything it needs is declared
// here as Host and supplied by the caller, which is what lets the whole feature
// be left out of a build (see the nowebui tag) without the agent noticing.
package webui

import (
	"time"

	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/settings"
)

// Host is what the console needs from the agent it administers.
//
// It is deliberately stated as one interface rather than reaching into an
// agent struct: this is the complete list of what the console can do, so the
// blast radius of the feature is readable in one place.
type Host interface {
	// ---- identity and lifecycle ----

	Running() bool
	ConfigDir() string
	StartAgent() error
	StopAgent()
	QuitAgent()
	RestartServers() error

	// ---- reader ----

	ReaderMode() string
	DevicePath() string
	AvailableDevices() []string
	AllCardTypes() []string
	// CurrentCard reports the tag on the reader, if any.
	CurrentCard() (uid, cardType string, present bool)
	RemoteDevices() (total, active int)
	SelectDevice(devicePath string) error

	// ---- server ----

	Port() int
	BootstrapPort() int
	CertFile() string
	TLSEnabled() bool
	LocalIPs() []string
	ClientCount() int
	Clients() []Client
	DisconnectClient(id string) error

	// ---- credentials and trust ----

	APISecret() string
	RotateAPISecret() (string, error)
	PublicKeyPin() string
	PairingPIN() string
	RotatePairingPIN() (string, error)
	CAInstalled() bool
	CAFingerprint() (string, error)
	RegenerateCertificate() error

	// ---- paired devices ----

	PairedDevices() []PairedDevice
	RevokeDevice(id string) error
	RevokeAllDevices() error
	RequirePairedDevice() bool

	// ---- browser origins ----

	AllowedOrigins() []string
	BlockedOrigins() []string
	OriginCheckDisabled() bool
	AllowOrigin(origin string) error
	RevokeOrigin(origin string) error
	SetOriginCheckDisabled(bool)

	// ---- settings ----

	Settings() settings.Settings
	// SaveSettings persists a mutation and applies it to the running agent.
	SaveSettings(mutate func(*settings.Settings)) (settings.Settings, error)
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

// Config assembles a Server.
type Config struct {
	// Host is the agent under administration. Required.
	Host Host

	// Logs is the ring the console tails. Nil disables the log views.
	Logs *logbuf.Ring

	// Name and Version identify the build to the console.
	Name    string
	Version string
	Dev     bool
}
