package agent

import (
	"net/http"

	"github.com/dotside-studios/davi-nfc-agent/server"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/dotside-studios/davi-nfc-agent/buildinfo"
	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/server/unifiedserver"
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

	// RemoteOps and RemoteScans connect a driver of paired devices, and
	// DeviceEndpoint serves their connections. All three come from a driver the
	// caller built; leaving them nil is an agent that serves its own reader
	// only. See agent.Config.
	RemoteOps      server.DeviceOps
	RemoteScans    <-chan nfc.NFCData
	DeviceEndpoint func(DeviceEndpointOptions) http.Handler

	// Plugins are activated with the agent, after the server plugin Setup
	// builds. A program that only wants to add endpoints to that server should
	// use Runtime.Servers instead; this is for plugins of its own.
	Plugins []Plugin

	// Explicit marks the fields this launcher set deliberately rather than left
	// at a default. A field marked here belongs to the launcher for the whole
	// run: the stored file does not change it and an operator cannot either.
	// The command builds it from which flags were given; a program embedding
	// the agent sets whichever fields it means.
	Explicit settings.Explicit

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

	// Servers is the plugin that owns the listener and everything served from
	// it. Add endpoints to it before the agent starts, whether a control
	// center or whatever else belongs on the same port:
	//
	//	rt.Servers.Add(agent.Endpoint{Name: "control center", Pattern: "/control/", Handler: c.Routes()})
	//
	// The listener itself does not exist until the plugins are activated.
	Servers *ServerPlugin
}

// Setup builds a configured agent from opts, reading and writing the config
// directory as it goes. manager supplies the readers — the caller chooses it,
// which is what keeps this package independent of any particular NFC backend.
//
// It does not build the console: that lives in agent/console, which imports
// this package. Wiring the two is the caller's job, and the only thing that
// needs to know about both.
func Setup(opts *Options, manager nfc.Manager) (*Runtime, error) {
	info := opts.Info.OrDefault()
	log.Printf("Starting %s %s", info.Name, info.FullVersion())

	// Resolve the config directory once — used by both the TLS manager
	// and the persistent API secret.
	configDir := opts.ConfigDir
	if configDir == "" {
		configDir = DefaultConfigDir(info.DirName)
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

	// Build the pairing server. It is available whenever pairing is possible,
	// not only under auto-TLS: an agent using an externally provisioned
	// certificate has no CA to hand out but still has devices to pair, and
	// coupling the two left that deployment with no way to authenticate one.
	//
	// It is not started here. It is a component now, so the agent starts it
	// with everything else and stops it again on the way down.
	var pairing *PairingServer
	if opts.BootstrapPort > 0 {
		pairing = NewPairingServer(PairingConfig{
			Port:         opts.BootstrapPort,
			CA:           tlsMgr,
			Devices:      devices,
			PublicKeyPin: agentPublicKeyPin,
			AppName:      info.DisplayName,
			AgentPort:    opts.DevicePort,
		})
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

	// Asked for on the command line or in the environment, as opposed to
	// remembered from a previous run. The distinction matters below: a stored
	// preference may raise the requirement but not withdraw one set here.
	askedForPairing := opts.RequirePaired || os.Getenv("DAVI_NFC_REQUIRE_PAIRED_DEVICES") == "1"

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
	if !opts.Explicit.Port && stored.Port > 0 {
		devicePort = stored.Port
	}

	// Either source can turn the requirement on; only a launcher can be relied
	// on to keep it on, which is what Explicit carries.
	explicit := opts.Explicit
	explicit.RequirePairedDevice = explicit.RequirePairedDevice || askedForPairing
	requirePaired := askedForPairing || stored.RequirePairedDevice
	if requirePaired {
		log.Printf("Paired devices required: the shared secret and loopback bypass no longer admit a device")
		if devices.Count() == 0 {
			log.Printf("Warning: no devices are paired yet, so every device connection will be refused until one pairs")
		}
	}

	// The listener and what it serves are a plugin, so a caller can add its own
	// endpoints to the same port before anything binds, and a build that wants
	// a different server can register a different plugin. Pairing rides along
	// as an endpoint of its own: no route, since it binds a port of its own,
	// but a lifetime the agent manages like any other.
	servers := &ServerPlugin{
		Config: unifiedserver.Config{
			Port:            devicePort,
			CertFile:        certFile,
			KeyFile:         keyFile,
			MDNSServiceName: info.DisplayName + " Device",
		},
	}
	if pairing != nil {
		servers.Add(Endpoint{Name: "pairing", Component: pairing})
	}

	// Everything the agent runs on is settled by this point, which is why it
	// can be handed over in one piece.
	a := New(Config{
		RemoteOps:           opts.RemoteOps,
		RemoteScans:         opts.RemoteScans,
		DeviceEndpoint:      opts.DeviceEndpoint,
		Manager:             manager,
		Info:                info,
		DevicePort:          devicePort,
		APISecret:           apiSecret,
		ConfigDir:           configDir,
		Origins:             origins,
		Devices:             devices,
		PublicKeyPin:        agentPublicKeyPin,
		Settings:            settingsStore,
		Logs:                opts.Logs,
		RequirePairedDevice: requirePaired,
		Explicit:            explicit,
		CertFile:            certFile,
		KeyFile:             keyFile,
		TLSManager:          tlsMgr,
	})

	a.ApplySettings(stored)

	// The server plugin goes on first: it publishes the listener the rest
	// mount on, and plugins are activated in the order they were added.
	if err := a.Plugins.Add(servers); err != nil {
		return nil, err
	}
	if err := a.Plugins.Add(opts.Plugins...); err != nil {
		return nil, err
	}

	return &Runtime{
		Agent:      a,
		Settings:   settingsStore,
		Logs:       opts.Logs,
		DevicePath: devicePath,
		Servers:    servers,
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
