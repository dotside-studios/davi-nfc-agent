package agent

import (
	"flag"
	"io"
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

// Options is the command line as parsed. A program embedding the agent can
// build one directly instead of calling ParseFlags — start from
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

	// devicePortSet records whether -device-port was given, so a stored port
	// does not override an explicit one.
	devicePortSet bool
}

// DefaultOptions returns the settings the flags default to. It is the starting
// point for building an agent without a command line: take it, change what you
// need, and hand it to Setup.
func DefaultOptions() *Options {
	return &Options{
		DevicePort:    DefaultDevicePort,
		BootstrapPort: DefaultBootstrapPort,
		AutoTLS:       true,
	}
}

// ParseFlags defines the agent's flags on the default command line and parses
// it. It does not act on -version: the caller prints that, because the version
// banner names the PC/SC backend and choosing that backend is the caller's job.
func ParseFlags() *Options {
	opts := &Options{}
	flag.BoolVar(&opts.Version, "version", false, "Print version information and exit")
	flag.StringVar(&opts.DevicePath, "device", "", "Path to NFC device (optional)")
	flag.IntVar(&opts.DevicePort, "device-port", DefaultDevicePort, "Port for the agent server (NFC devices and web clients share this port)")
	flag.IntVar(&opts.BootstrapPort, "bootstrap-port", DefaultBootstrapPort, "Port for CA bootstrap server (0 to disable)")
	flag.StringVar(&opts.APISecret, "api-secret", "", "API secret for session handshake (optional)")
	flag.StringVar(&opts.CertFile, "cert", "", "Path to TLS certificate file (enables HTTPS/WSS)")
	flag.StringVar(&opts.KeyFile, "key", "", "Path to TLS private key file (enables HTTPS/WSS)")
	flag.BoolVar(&opts.AutoTLS, "auto-tls", true, "Automatically generate and manage TLS certificates")
	flag.BoolVar(&opts.RequirePaired, "require-paired-devices", false, "Admit only devices that have paired, withdrawing the shared secret and loopback bypass for device connections. Browser clients are unaffected")
	flag.BoolVar(&opts.InstallCA, "install-ca", false, "Install a local certificate authority into the system trust store so browsers trust this agent. Not needed for phones, readers, or an externally provisioned certificate")
	flag.StringVar(&opts.ConfigDir, "config-dir", "", "Config directory (default: platform-specific)")
	flag.StringVar(&opts.AllowedOrigins, "allowed-origins", "", "Comma-separated browser origins allowed to connect (host:port), e.g. \"app.example.com,localhost:3002\". Use \"*\" to disable the check (not recommended)")
	flag.Parse()

	opts.devicePortSet = isFlagSet("device-port")
	return opts
}

// Runtime is everything Setup built: a configured agent, plus the stores and
// servers a front end needs to drive and display it.
type Runtime struct {
	Agent    *Agent
	Settings *settings.Store
	Logs     *logbuf.Ring
	Origins  *OriginStore
	Devices  *DeviceRegistry

	// Bootstrap is the pairing server, nil when pairing is disabled.
	Bootstrap     *tls.BootstrapServer
	BootstrapPort int

	// DevicePath is the reader to open at startup, once the flag and the
	// stored preference have been reconciled.
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
	// Capture log output in memory before anything else logs. Started from a
	// desktop launcher there is no stderr to read, so without this the agent's
	// diagnostics are discarded as they are produced.
	logRing := logbuf.New(logbuf.DefaultCapacity)
	log.SetOutput(io.MultiWriter(os.Stderr, logRing))

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

	a := NewAgent(manager)
	a.DevicePort = opts.DevicePort
	if a.DevicePort == 0 {
		// A hand-built Options that never set a port means the default, not a
		// listener on whatever the kernel hands out.
		a.DevicePort = DefaultDevicePort
	}
	a.APISecret = apiSecret

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
	a.Origins = origins
	a.ConfigDir = configDir
	a.PublicKeyPin = agentPublicKeyPin
	a.Devices = devices
	a.Bootstrap = bootstrapServer
	a.BootstrapPort = opts.BootstrapPort
	a.RequirePairedDevice = opts.RequirePaired || os.Getenv("DAVI_NFC_REQUIRE_PAIRED_DEVICES") == "1"

	if a.RequirePairedDevice {
		log.Printf("Paired devices required: the shared secret and loopback bypass no longer admit a device")
		if devices.Count() == 0 {
			log.Printf("Warning: no devices are paired yet, so every device connection will be refused until one pairs")
		}
	}
	a.CertFile = certFile
	a.KeyFile = keyFile
	a.TLSManager = tlsMgr // For network change watching and cert regeneration

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
	if !opts.devicePortSet && stored.Port > 0 {
		a.DevicePort = stored.Port
	}
	if !opts.RequirePaired && os.Getenv("DAVI_NFC_REQUIRE_PAIRED_DEVICES") != "1" && stored.RequirePairedDevice {
		a.RequirePairedDevice = true
	}
	a.ApplySettings(stored)

	return &Runtime{
		Agent:         a,
		Settings:      settingsStore,
		Logs:          logRing,
		Origins:       origins,
		Devices:       devices,
		Bootstrap:     bootstrapServer,
		BootstrapPort: opts.BootstrapPort,
		DevicePath:    devicePath,
	}, nil
}

// isFlagSet reports whether a flag was given on the command line, as opposed to
// holding its default. A stored port must not override an explicit -device-port.
func isFlagSet(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
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
