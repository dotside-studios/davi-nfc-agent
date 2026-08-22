// Command davi-nfc-agent is the agent this repository ships: a reader, a tray,
// and the plugins that serve it — the WebSocket endpoints devices and web pages
// connect to, the pairing server, and the control center.
//
// It is one build of many. Everything below the flags is wiring, and a build
// that wants a different set of plugins is a command like this one with
// different Use lines. See docs/custom-builds.md.
package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/buildinfo"
	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/multimanager"
	"github.com/dotside-studios/davi-nfc-agent/nfc/pcsc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/plugin"
	"github.com/dotside-studios/davi-nfc-agent/plugins/console"
	"github.com/dotside-studios/davi-nfc-agent/plugins/pairing"
	"github.com/dotside-studios/davi-nfc-agent/plugins/wsserver"
	"github.com/dotside-studios/davi-nfc-agent/settings"
	"github.com/dotside-studios/davi-nfc-agent/tls"
	"github.com/dotside-studios/davi-nfc-agent/tray"
)

const (
	DEFAULT_DEVICE_PORT    = 9470
	DEFAULT_BOOTSTRAP_PORT = 9472
)

var (
	// CLI flags
	versionFlag        bool
	devicePathFlag     string
	devicePortFlag     int
	bootstrapPortFlag  int
	apiSecretFlag      string
	certFileFlag       string
	keyFileFlag        string
	autoTLSFlag        bool
	configDirFlag      string
	allowedOriginsFlag string
	installCAFlag      bool
	requirePairedFlag  bool
)

func main() {
	// Command line flags
	flag.BoolVar(&versionFlag, "version", false, "Print version information and exit")
	flag.StringVar(&devicePathFlag, "device", "", "Path to NFC device (optional)")
	flag.IntVar(&devicePortFlag, "device-port", DEFAULT_DEVICE_PORT, "Port for the agent server (NFC devices and web clients share this port)")
	flag.IntVar(&bootstrapPortFlag, "bootstrap-port", DEFAULT_BOOTSTRAP_PORT, "Port for CA bootstrap server (0 to disable)")
	flag.StringVar(&apiSecretFlag, "api-secret", "", "API secret for session handshake (optional)")
	flag.StringVar(&certFileFlag, "cert", "", "Path to TLS certificate file (enables HTTPS/WSS)")
	flag.StringVar(&keyFileFlag, "key", "", "Path to TLS private key file (enables HTTPS/WSS)")
	flag.BoolVar(&autoTLSFlag, "auto-tls", true, "Automatically generate and manage TLS certificates")
	flag.BoolVar(&requirePairedFlag, "require-paired-devices", false, "Admit only devices that have paired, withdrawing the shared secret and loopback bypass for device connections. Browser clients are unaffected")
	flag.BoolVar(&installCAFlag, "install-ca", false, "Install a local certificate authority into the system trust store so browsers trust this agent. Not needed for phones, readers, or an externally provisioned certificate")
	flag.StringVar(&configDirFlag, "config-dir", "", "Config directory (default: platform-specific)")
	flag.StringVar(&allowedOriginsFlag, "allowed-origins", "", "Comma-separated browser origins allowed to connect (host:port), e.g. \"app.example.com,localhost:3002\". Use \"*\" to disable the check (not recommended)")
	flag.Parse()

	// Handle --version flag
	if versionFlag {
		fmt.Println(buildinfo.BuildInfo())
		// Which PC/SC backend this binary was built with; the first thing to
		// know when a reader is not being detected.
		fmt.Printf("  PC/SC: %s\n", pcsc.Backend)
		os.Exit(0)
	}

	// Capture log output in memory before anything else logs. Started from a
	// desktop launcher there is no stderr to read, so without this the agent's
	// diagnostics are discarded as they are produced.
	logRing := logbuf.New(logbuf.DefaultCapacity)
	log.SetOutput(io.MultiWriter(os.Stderr, logRing))

	log.Printf("Starting %s %s", buildinfo.Name, buildinfo.FullVersion())

	// Resolve the config directory once — used by both the TLS manager
	// and the persistent API secret.
	configDir := configDirFlag
	if configDir == "" {
		configDir = getDefaultConfigDir()
	}

	// Initialize auto-TLS if enabled (and no manual cert/key provided)
	var tlsMgr *tls.Manager
	var agentPublicKeyPin string
	if autoTLSFlag && certFileFlag == "" && keyFileFlag == "" {
		tlsMgr = tls.NewManager(configDir)
		tlsMgr.UseCA(installCAFlag)
		certFile, keyFile, err := tlsMgr.EnsureCertificates()
		if err != nil {
			log.Printf("Warning: Auto-TLS failed: %v (running without TLS)", err)
			tlsMgr = nil
		} else {
			certFileFlag = certFile
			keyFileFlag = keyFile

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
	if apiSecretFlag == "" {
		secret, fresh, err := agent.LoadOrCreateAPISecret(configDir)
		if err != nil {
			log.Printf("Warning: failed to load API secret: %v (running without auth)", err)
		} else {
			apiSecretFlag = secret
			if fresh {
				log.Printf("Generated new API secret at %s", configDir)
			}
		}
	}

	// Load the paired-device registry. Each device gets its own credential, so
	// one can be revoked without logging out the rest.
	devices, err := agent.NewDeviceRegistry(configDir)
	if err != nil {
		log.Printf("Warning: failed to load paired devices: %v", err)
		devices, _ = agent.NewDeviceRegistry("")
	}

	// Start the pairing/bootstrap server. It runs whenever pairing is possible,
	// not only under auto-TLS: an agent using an externally provisioned
	// certificate has no CA to hand out but still has devices to pair, and
	// coupling the two left that deployment with no way to authenticate one.
	var bootstrapServer *tls.BootstrapServer
	var pairingIssuer tls.PairingIssuer
	if bootstrapPortFlag > 0 {
		// tlsMgr may be nil here; the CA endpoints report that there is nothing
		// to install and the pairing endpoint works regardless.
		bootstrapServer = tls.NewBootstrapServer(tlsMgr, bootstrapPortFlag)
		pairingIssuer = agent.NewPairingIssuer(devices, agentPublicKeyPin)
		bootstrapServer.SetPairingIssuer(pairingIssuer, devicePortFlag)
		// Started by the pairing plugin, with everything else that serves.
	}

	// Initialize smartphone manager
	smartphoneManager := remotenfc.NewManager(remotenfc.DeviceTimeout)

	// Create multi-manager combining hardware and smartphone
	manager := multimanager.NewMultiManager(
		multimanager.ManagerEntry{Name: nfc.ManagerTypeHardware, Manager: pcsc.NewManager()},
		multimanager.ManagerEntry{Name: nfc.ManagerTypeSmartphone, Manager: smartphoneManager},
	)

	// Create agent
	nfcAgent := agent.New(manager)
	nfcAgent.DevicePort = devicePortFlag
	nfcAgent.APISecret = apiSecretFlag

	// The allowlist persists in the config dir and starts with the first-party
	// consoles, so the shipped console connects on a fresh install. Anything
	// passed on the command line is added to it.
	origins, err := agent.NewOriginStore(configDir)
	if err != nil {
		log.Printf("Warning: failed to load origin allowlist: %v", err)
		origins, _ = agent.NewOriginStore("")
	}
	for _, origin := range parseAllowedOrigins(allowedOriginsFlag) {
		if origin == "*" {
			log.Printf("Warning: -allowed-origins \"*\" disables the origin check; any site the operator visits can drive the reader")
			origins.SessionAllowAny(true)
			continue
		}
		if err := origins.Allow(origin); err != nil {
			log.Printf("Warning: failed to allow origin %q: %v", origin, err)
		}
	}
	nfcAgent.Origins = origins
	nfcAgent.ConfigDir = configDir
	nfcAgent.PublicKeyPin = agentPublicKeyPin
	nfcAgent.Devices = devices
	nfcAgent.Bootstrap = bootstrapServer
	nfcAgent.BootstrapPort = bootstrapPortFlag
	nfcAgent.CertFile = certFileFlag
	nfcAgent.KeyFile = keyFileFlag
	nfcAgent.TLSManager = tlsMgr // For network change watching and cert regeneration

	// Set up signal handling for graceful shutdown. The interrupt is acted on
	// below, once there is a tray to quit.
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Load persisted preferences. What the launcher asked for still wins.
	settingsStore, err := settings.New(configDir)
	if err != nil {
		log.Printf("Warning: failed to load settings: %v", err)
		settingsStore, _ = settings.New("")
	}
	stored := settingsStore.Get()

	// What this run was launched with, as opposed to what a previous one
	// remembered. These fields are the launcher's until the agent restarts: the
	// stored file below does not change them, and neither does an operator at
	// the tray or the console, both of which show them as held.
	//
	// The environment is a launcher too, for the one setting that reads it.
	askedForPairing := requirePairedFlag || os.Getenv("DAVI_NFC_REQUIRE_PAIRED_DEVICES") == "1"
	nfcAgent.SetExplicit(settings.Explicit{
		DevicePath:          isFlagSet("device"),
		Port:                isFlagSet("device-port"),
		RequirePairedDevice: isFlagSet("require-paired-devices") || askedForPairing,
	})
	nfcAgent.RequirePairedDevice = askedForPairing
	nfcAgent.SetPinnedDevice(devicePathFlag)

	// The agent holds the settings from here on, and the console and the tray
	// read them back from it, so a mode switched in one shows in the other.
	nfcAgent.ApplySettings(stored)

	// Reported once both sources are in, so it names what is in force rather
	// than what either asked for.
	if nfcAgent.RequiresPairedDevice() {
		log.Printf("Paired devices required: the shared secret and loopback bypass no longer admit a device")
		if devices.Count() == 0 {
			log.Printf("Warning: no devices are paired yet, so every device connection will be refused until one pairs")
		}
	}

	// The reader to open: the flag, or the stored preference it just applied.
	devicePathFlag = nfcAgent.CurrentPinnedDevice()

	// A device that pairs is told where to reconnect, and the stored settings
	// may just have moved the agent to a different port.
	if bootstrapServer != nil && nfcAgent.ConfiguredPort() != devicePortFlag {
		bootstrapServer.SetPairingIssuer(pairingIssuer, nfcAgent.ConfiguredPort())
	}

	// The stores tell the agent when something changed under them, and the
	// agent tells everything that renders it. One path, whatever moved.
	origins.OnChange(nfcAgent.PublishState)
	devices.OnChange(nfcAgent.PublishState)

	// The tray: this agent's user interface, and where the plugins put their
	// menus. A build with no desktop leaves it out and registers no tray.
	app := tray.New(nfcAgent, devicePathFlag)
	app.AttachSettings(settingsStore)
	nfcAgent.SetQuit(app.Quit)

	// What serves the agent. The agent serves nothing itself: it drives a
	// reader and holds what an operator has decided, and everything with a
	// port, a page or a menu of its own is registered here.
	//
	// A build that wants none of it registers none of it. The order is the
	// order they come up in, and the reverse of the order they go down in, so
	// the listener everything else is mounted on is first.
	host := nfcAgent.Plugins()

	if err := host.Use(wsserver.New(wsserver.Config{Agent: wsserver.ForAgent(nfcAgent)})); err != nil {
		log.Printf("Warning: this agent will not serve devices or clients: %v", err)
	}
	if served := console.New(nfcAgent, settingsStore, logRing); served != nil {
		if err := host.Use(served); err != nil {
			log.Printf("Warning: the control center will not be served: %v", err)
		}
	}
	if bootstrapServer != nil {
		err := host.Use(pairing.New(pairing.Config{Server: bootstrapServer, Port: bootstrapPortFlag}))
		if err != nil {
			log.Printf("Warning: phones will not be able to pair: %v", err)
		}
	}

	// And whatever a consumer registered from an init function of their own,
	// which needs no change here to be picked up.
	for _, registered := range plugin.Default().Plugins() {
		if info := registered.Describe(); info.ID != "" {
			if _, taken := host.Lookup(info.ID); taken {
				log.Printf("Warning: a registered plugin replaces the agent's own %q feature", info.ID)
			}
		}
		if err := host.Use(registered); err != nil {
			log.Printf("Warning: a registered plugin was ignored: %v", err)
		}
	}

	// One path from a saved preference to the running agent, whoever saved it.
	// The tray and the console both write to the store and neither applies
	// anything itself, so the two cannot put the agent in different states.
	settingsStore.OnChange(func(next settings.Settings) {
		nfcAgent.ApplySettings(next)
		app.SyncSettings(nfcAgent.Settings())
	})

	// One watcher for every plugin, rather than one per plugin: a card arriving
	// at the reader is announced by nothing, so something has to look.
	nfcAgent.WatchState(0)

	go func() {
		<-sigChan
		app.Quit()
	}()

	app.Run()
}

// isFlagSet reports whether a flag was given on the command line, as opposed to
// holding its default. It is what settings.Explicit is built from: a default
// leaves the stored preference in charge, a flag takes it over.
func isFlagSet(name string) bool {
	found := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

// getDefaultConfigDir returns the platform-specific config directory.
func getDefaultConfigDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		// Fallback to home directory
		home, _ := os.UserHomeDir()
		configDir = filepath.Join(home, ".config")
	}
	return filepath.Join(configDir, buildinfo.DirName)
}

// parseAllowedOrigins turns the comma-separated flag (or DAVI_NFC_ALLOWED_ORIGINS)
// into the host:port list CheckOrigin matches against.
//
// Full URLs are accepted and reduced to their host:port, because that is what
// people paste and the alternative is a silently ignored entry: an origin that
// does not match is indistinguishable from one that was never configured.
func parseAllowedOrigins(flagValue string) []string {
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
