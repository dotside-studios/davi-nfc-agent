package agent

import (
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/dotside-studios/davi-nfc-agent/buildinfo"
	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/settings"
	"github.com/dotside-studios/davi-nfc-agent/tls"
)

// Default ports. The agent serves devices and clients from one listener.
const (
	DefaultDevicePort    = 9470
	DefaultBootstrapPort = 9472
)

// Options is everything Setup needs. The shipped command fills it from its
// flags; a program embedding the agent builds one directly, starting from
// DefaultOptions rather than the zero value, which would ask for no TLS and
// port 0.
type Options struct {
	Version        bool
	DevicePath     string
	DevicePort     int
	BootstrapPort  int
	APISecret      string
	CertFile       string
	KeyFile        string
	AutoTLS        bool
	ConfigDir      string
	AllowedOrigins string
	InstallCA      bool
	RequirePaired  bool

	// DevicePortSet reports that DevicePort is a deliberate choice rather than
	// a default, so a port persisted in settings must not override it. The
	// command sets it when -device-port is given; set it yourself whenever you
	// assign DevicePort and mean it.
	DevicePortSet bool

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
// through the agent: the origin and device stores, the pairing server and its
// port all live on Agent, and reads of those go through rt.Agent so there is
// one copy to keep true.
type Runtime struct {
	Agent    *Agent
	Settings *settings.Store

	// Logs is the ring passed in through Options, for the console to display.
	Logs *logbuf.Ring

	// DevicePath is the reader to open at startup, once the caller's choice
	// and the stored preference have been reconciled.
	DevicePath string
}

// Setup builds a configured agent from opts, reading and writing the config
// directory as it goes. manager supplies the readers — the caller chooses it,
// which is what keeps this package independent of any particular NFC backend.
//
// It does not build the console: that lives in agent/console, which imports
// this package. Wiring the two is the caller's job, and the only thing that
// needs to know about both.
func Setup(opts *Options, manager nfc.Manager) (*Runtime, error) {
	log.Printf("Starting %s %s", buildinfo.Name, buildinfo.FullVersion())

	// Resolve the config directory once — used by both the TLS manager
	// and the persistent API secret.
	configDir := opts.ConfigDir
	if configDir == "" {
		configDir = DefaultConfigDir()
	}

	certFile, keyFile := opts.CertFile, opts.KeyFile

	// Initialize auto-TLS if enabled (and no manual cert/key provided)
	var tlsMgr *tls.Manager
	var agentPublicKeyPin string
	if opts.AutoTLS && certFile == "" && keyFile == "" {
		tlsMgr = tls.NewManager(configDir)
		tlsMgr.UseCA(opts.InstallCA)
		cert, key, err := tlsMgr.EnsureCertificates()
		if err != nil {
			log.Printf("Warning: Auto-TLS failed: %v (running without TLS)", err)
			tlsMgr = nil
		} else {
			certFile, keyFile = cert, key

			// Native devices authenticate the agent by this value rather than
			// by a trust store, so log it where a first run will show it.
			if pin, err := tlsMgr.PublicKeyPin(); err == nil {
				log.Printf("Agent public key pin: %s", pin)
				agentPublicKeyPin = pin
			}
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
			log.Printf("Warning: failed to load API secret: %v (running without auth)", err)
		} else {
			apiSecret = secret
			if fresh {
				log.Printf("Generated new API secret at %s", configDir)
			}
		}
	}

	// Load the paired-device registry. Each device gets its own credential, so
	// one can be revoked without logging out the rest.
	devices, err := NewDeviceRegistry(configDir)
	if err != nil {
		log.Printf("Warning: failed to load paired devices: %v", err)
		devices, _ = NewDeviceRegistry("")
	}

	// Start the pairing/bootstrap server. It runs whenever pairing is possible,
	// not only under auto-TLS: an agent using an externally provisioned
	// certificate has no CA to hand out but still has devices to pair, and
	// coupling the two left that deployment with no way to authenticate one.
	var bootstrapServer *tls.BootstrapServer
	if opts.BootstrapPort > 0 {
		// tlsMgr may be nil here; the CA endpoints report that there is nothing
		// to install and the pairing endpoint works regardless.
		var caReader *tls.Manager
		if tlsMgr != nil {
			caReader = tlsMgr
		}
		bootstrapServer = tls.NewBootstrapServer(caReader, opts.BootstrapPort)
		bootstrapServer.SetPairingIssuer(NewPairingIssuer(devices, agentPublicKeyPin), opts.DevicePort)
		if err := bootstrapServer.Start(); err != nil {
			log.Printf("Warning: Failed to start bootstrap server: %v", err)
		}
	}

	// The allowlist persists in the config dir and starts with the first-party
	// consoles, so the shipped console connects on a fresh install. Anything
	// passed on the command line is added to it.
	origins, err := NewOriginStore(configDir)
	if err != nil {
		log.Printf("Warning: failed to load origin allowlist: %v", err)
		origins, _ = NewOriginStore("")
	}
	for _, origin := range ParseAllowedOrigins(opts.AllowedOrigins) {
		if origin == "*" {
			log.Printf("Warning: -allowed-origins \"*\" disables the origin check; any site the operator visits can drive the reader")
			origins.SessionAllowAny(true)
			continue
		}
		if err := origins.Allow(origin); err != nil {
			log.Printf("Warning: failed to allow origin %q: %v", origin, err)
		}
	}

	askedForPairing := opts.RequirePaired || os.Getenv("DAVI_NFC_REQUIRE_PAIRED_DEVICES") == "1"
	if askedForPairing {
		log.Printf("Paired devices required: the shared secret and loopback bypass no longer admit a device")
		if devices.Count() == 0 {
			log.Printf("Warning: no devices are paired yet, so every device connection will be refused until one pairs")
		}
	}

	// Load persisted preferences. Explicit flags still win: something that
	// passed -device meant it for this run.
	settingsStore, err := settings.New(configDir)
	if err != nil {
		log.Printf("Warning: failed to load settings: %v", err)
		settingsStore, _ = settings.New("")
	}
	stored := settingsStore.Get()

	devicePath := opts.DevicePath
	if devicePath == "" {
		devicePath = stored.DevicePath
	}

	devicePort := opts.DevicePort
	if devicePort == 0 {
		// Options built by hand rather than from flags: the default, not a
		// listener on whatever the kernel hands out.
		devicePort = DefaultDevicePort
	}
	if !opts.DevicePortSet && stored.Port > 0 {
		devicePort = stored.Port
	}

	requirePaired := askedForPairing
	if !askedForPairing && stored.RequirePairedDevice {
		requirePaired = true
	}

	// Everything the agent runs on is settled by this point, which is why it
	// can be handed over in one piece.
	a := New(Config{
		Manager:             manager,
		DevicePort:          devicePort,
		APISecret:           apiSecret,
		ConfigDir:           configDir,
		Origins:             origins,
		Devices:             devices,
		PublicKeyPin:        agentPublicKeyPin,
		Bootstrap:           bootstrapServer,
		BootstrapPort:       opts.BootstrapPort,
		RequirePairedDevice: requirePaired,
		CertFile:            certFile,
		KeyFile:             keyFile,
		TLSManager:          tlsMgr,
	})

	a.ApplySettings(stored)

	return &Runtime{
		Agent:      a,
		Settings:   settingsStore,
		Logs:       opts.Logs,
		DevicePath: devicePath,
	}, nil
}

// DefaultConfigDir returns the platform-specific config directory.
func DefaultConfigDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		// Fallback to home directory
		home, _ := os.UserHomeDir()
		configDir = filepath.Join(home, ".config")
	}
	return filepath.Join(configDir, buildinfo.DirName)
}

// ParseAllowedOrigins turns the comma-separated flag (or DAVI_NFC_ALLOWED_ORIGINS)
// into the host:port list CheckOrigin matches against.
//
// Full URLs are accepted and reduced to their host:port, because that is what
// people paste and the alternative is a silently ignored entry: an origin that
// does not match is indistinguishable from one that was never configured.
func ParseAllowedOrigins(flagValue string) []string {
	raw := flagValue
	if raw == "" {
		raw = os.Getenv("DAVI_NFC_ALLOWED_ORIGINS")
	}
	if raw == "" {
		return nil
	}

	var origins []string
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		if entry == "*" {
			origins = append(origins, entry)
			continue
		}
		if strings.Contains(entry, "://") {
			if u, err := url.Parse(entry); err == nil && u.Host != "" {
				entry = u.Host
			}
		}
		origins = append(origins, strings.TrimSuffix(entry, "/"))
	}
	return origins
}
