package agent

import (
	"os"
	"path/filepath"

	"github.com/dotside-studios/davi-nfc-agent/buildinfo"
	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/tls"
)

// Default ports. The agent serves devices and clients from one listener.
const (
	DefaultDevicePort    = 9470
	DefaultBootstrapPort = 9472
)

// Options is what an agent is built from: what Setup resolves the agent's own
// state out of, plus the ports a program wires the rest of its plugins with.
// BootstrapPort is the second kind, read by whoever builds the pairing plugin
// rather than by Setup.
//
// The shipped command fills this from its flags; a program embedding the agent
// builds one directly, starting from DefaultOptions rather than the zero value,
// which would ask for no TLS and port 0.
//
// Setup resolves what it can: a blank ConfigDir becomes the platform default, a
// blank APISecret is loaded or generated, and the paired devices are read from
// disk. What it cannot resolve it passes through, so a
// field here that names an agent setting arrives on the agent unchanged.
type Options struct {
	DevicePath string
	DevicePort int

	// BootstrapPort is the pairing listener the launcher asked for, 0 for
	// none. Setup does not read it: whether an agent pairs devices, and what
	// displays the PIN, is the program's decision. See [PairingFor].
	BootstrapPort int

	APISecret string

	// CertFile and KeyFile are a certificate provisioned outside this agent,
	// which turns AutoTLS off. Setup does not read them either: what serves a
	// certificate is the program's decision, so it passes them to whatever
	// does, as [listener.Config] on a [ServerPlugin].
	CertFile string
	KeyFile  string

	AutoTLS             bool
	ConfigDir           string
	AllowedOrigins      string
	InstallCA           bool
	RequirePairedDevice bool

	// Mode is the access mode the reader runs in, CardTypes the types a scan
	// may carry, and ReaderFeedback has the reader announce what it does. They
	// go to the agent as they are: nothing is read back from a file, so what a
	// launcher sets is what the agent starts with.
	Mode           nfc.ReaderMode
	CardTypes      []string
	ReaderFeedback bool

	// Info is what this build calls itself; see agent.Config.Info. It decides
	// the default config directory, so a program with its own identity should
	// set it before Setup resolves any paths.
	Info buildinfo.Info

	// Logs, when set, is the ring the console reads the agent's log from. A
	// caller that wants startup captured installs it as the log sink before
	// calling Setup; Setup itself does not touch the process logger.
	Logs *logbuf.Ring
}

// DefaultOptions returns the settings the shipped command's flags default to.
// It is the starting point for building an agent without a command line: take
// it, change what you need, and hand it to Setup.
func DefaultOptions() *Options {
	return &Options{
		DevicePort:    DefaultDevicePort,
		BootstrapPort: DefaultBootstrapPort,
		AutoTLS:       true,
	}
}

// Runtime is what Setup built. It carries only what is not already reachable
// through the agent: the device registry lives on Agent, and reads of it go
// through rt.Agent so there is one copy to keep true.
type Runtime struct {
	Agent *Agent

	// Logs is the ring passed in through Options, for the console to display.
	Logs *logbuf.Ring

	// DevicePath is the reader to open at startup, as Options named it. Empty
	// selects the first reader, and waits if none is attached yet.
	DevicePath string

	// Certificates is the certificate the agent manages for itself, nil for a
	// build serving one provisioned elsewhere. Wrap it in a [TrustPlugin] for
	// the tray entry that installs the authority behind it, and hand it to
	// [NewPairingPlugin] as the authority a pairing device is given.
	Certificates *tls.Manager

	// CertFile and KeyFile are the certificate a listener should serve: the one
	// named by Options, or the one Certificates manages, or empty for a build
	// serving plain HTTP. Resolved here so every build does not repeat the
	// fallback; put them on [ServerPlugin.Config].
	CertFile string
	KeyFile  string

	// AllowedOrigins is what Options named, parsed. Put it on
	// [ServerPlugin.AllowedOrigins]: the allowlist belongs to what serves the
	// connections it admits.
	AllowedOrigins []string
}

// Setup builds a configured agent from opts, reading and writing the config
// directory as it goes. manager supplies the readers, chosen by the caller,
// which keeps this package independent of any particular NFC backend.
//
// It does not build the console: that lives in agent/console, which imports
// this package. Wiring the two is the caller's job, and the only thing that
// needs to know about both.
func Setup(opts *Options, manager nfc.Manager) (*Runtime, error) {
	info := opts.Info.OrDefault()
	agentLog.Printf("Starting %s %s", info.Name, info.FullVersion())

	// Resolve the config directory once, for both the TLS manager and the
	// persistent API secret.
	configDir := opts.ConfigDir
	if configDir == "" {
		configDir = DefaultConfigDir(info.DirName)
	}

	// The certificate this agent manages for itself, unless one was
	// provisioned outside it. Setup builds it because it is config-directory
	// state; what serves it, hands out its authority and offers to install it
	// is the program's business.
	var tlsMgr *tls.Manager
	var agentPublicKeyPin string
	if opts.AutoTLS && opts.CertFile == "" && opts.KeyFile == "" {
		tlsMgr = tls.NewManager(configDir)
		tlsMgr.UseCA(opts.InstallCA)
		if _, _, err := tlsMgr.EnsureCertificates(); err != nil {
			agentWarn.Printf("Auto-TLS failed: %v (running without TLS)", err)
			tlsMgr = nil
		} else if pin, err := tlsMgr.PublicKeyPin(); err == nil {
			// Native devices authenticate the agent by this value rather than
			// by a trust store, so log it where a first run will show it.
			agentLog.Printf("Agent public key pin: %s", pin)
			agentPublicKeyPin = pin
		}
	}

	// Resolve the API secret. Explicit -api-secret takes precedence;
	// otherwise we load (or first-run generate) one persisted under
	// the config directory so phones paired across restarts keep
	// working without manual reconfiguration.
	apiSecret := opts.APISecret
	if apiSecret == "" {
		secret, fresh, err := loadOrCreateAPISecret(configDir)
		if err != nil {
			agentWarn.Printf("failed to load API secret: %v (running without auth)", err)
		} else {
			apiSecret = secret
			if fresh {
				agentLog.Printf("Generated new API secret at %s", configDir)
			}
		}
	}

	// Load the paired-device registry. Each device gets its own credential, so
	// one can be revoked without logging out the rest.
	devices, err := NewDeviceRegistry(configDir)
	if err != nil {
		agentWarn.Printf("failed to load paired devices: %v", err)
		devices, _ = NewDeviceRegistry("")
	}

	// Asked for on the command line or in the environment, as opposed to
	// remembered from a previous run. The distinction matters below: a stored
	// preference may raise the requirement but not withdraw one set here.
	askedForPairing := opts.RequirePairedDevice || os.Getenv("DAVI_NFC_REQUIRE_PAIRED_DEVICES") == "1"

	devicePort := opts.DevicePort
	if devicePort == 0 {
		// Options built by hand rather than from flags: the default, not a
		// listener on whatever the kernel hands out.
		devicePort = DefaultDevicePort
	}

	if askedForPairing {
		agentLog.Printf("Paired devices required: the shared secret and loopback bypass no longer admit a device")
		if devices.Count() == 0 {
			agentWarn.Printf("no devices are paired yet, so every device connection will be refused until one pairs")
		}
	}

	// Everything the agent runs on is settled by this point, which is why it
	// can be handed over in one piece.
	//
	// Not the listener: that is a plugin the caller registers, which is what
	// lets a build decide what it serves. See [ServerPlugin].
	// The pair a listener serves: the one Options named, or the one the manager
	// keeps. As a pair, since half a certificate is not something to complete
	// from somewhere else.
	certFile, keyFile := opts.CertFile, opts.KeyFile
	if certFile == "" && keyFile == "" && tlsMgr != nil {
		certFile, keyFile = tlsMgr.GetCertFile(), tlsMgr.GetKeyFile()
	}

	a := New(Config{
		Manager:             manager,
		Info:                info,
		DevicePort:          devicePort,
		APISecret:           apiSecret,
		ConfigDir:           configDir,
		Devices:             devices,
		PublicKeyPin:        agentPublicKeyPin,
		Logs:                opts.Logs,
		RequirePairedDevice: askedForPairing,
		ReaderFeedback:      opts.ReaderFeedback,
		Mode:                opts.Mode,
		CardTypes:           opts.CardTypes,
		DevicePath:          opts.DevicePath,
	})

	return &Runtime{
		Agent:        a,
		Logs:         opts.Logs,
		DevicePath:   opts.DevicePath,
		Certificates: tlsMgr,
		CertFile:     certFile,
		KeyFile:      keyFile,

		AllowedOrigins: server.ParseAllowedOrigins(opts.AllowedOrigins),
	}, nil
}

// DefaultConfigDir returns the platform-specific config directory for an
// application of the given directory name. Pass buildinfo.Default().DirName for
// the agent's own.
func DefaultConfigDir(dirName string) string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		// Fallback to home directory
		home, _ := os.UserHomeDir()
		configDir = filepath.Join(home, ".config")
	}
	return filepath.Join(configDir, dirName)
}
