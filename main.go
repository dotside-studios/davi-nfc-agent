// Package main provides an NFC card reader agent with WebSocket broadcasting capabilities.
// It supports reading NDEF formatted text from Mifare Classic tags and broadcasts the data
// to connected WebSocket clients.
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

	"fyne.io/systray"

	"github.com/dotside-studios/davi-nfc-agent/buildinfo"
	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/multimanager"
	"github.com/dotside-studios/davi-nfc-agent/nfc/pcsc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/settings"
	"github.com/dotside-studios/davi-nfc-agent/tls"
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
		secret, fresh, err := loadOrCreateAPISecret(configDir)
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
	if bootstrapPortFlag > 0 {
		// tlsMgr may be nil here; the CA endpoints report that there is nothing
		// to install and the pairing endpoint works regardless.
		bootstrapServer = tls.NewBootstrapServer(tlsMgr, bootstrapPortFlag)
		bootstrapServer.SetPairingIssuer(NewPairingIssuer(devices, agentPublicKeyPin), devicePortFlag)
		if err := bootstrapServer.Start(); err != nil {
			log.Printf("Warning: Failed to start bootstrap server: %v", err)
		}
	}

	// Initialize smartphone manager
	smartphoneManager := remotenfc.NewManager(remotenfc.DeviceTimeout)

	// Create multi-manager combining hardware and smartphone
	manager := multimanager.NewMultiManager(
		multimanager.ManagerEntry{Name: nfc.ManagerTypeHardware, Manager: pcsc.NewManager()},
		multimanager.ManagerEntry{Name: nfc.ManagerTypeSmartphone, Manager: smartphoneManager},
	)

	// Create agent
	agent := NewAgent(manager)
	agent.DevicePort = devicePortFlag
	agent.APISecret = apiSecretFlag

	// The allowlist persists in the config dir and starts with the first-party
	// consoles, so the shipped console connects on a fresh install. Anything
	// passed on the command line is added to it.
	origins, err := NewOriginStore(configDir)
	if err != nil {
		log.Printf("Warning: failed to load origin allowlist: %v", err)
		origins, _ = NewOriginStore("")
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
	agent.Origins = origins
	agent.ConfigDir = configDir
	agent.PublicKeyPin = agentPublicKeyPin
	agent.Devices = devices
	agent.Bootstrap = bootstrapServer
	agent.BootstrapPort = bootstrapPortFlag
	agent.CertFile = certFileFlag
	agent.KeyFile = keyFileFlag
	agent.TLSManager = tlsMgr // For network change watching and cert regeneration

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		if bootstrapServer != nil {
			bootstrapServer.Stop()
		}
		systray.Quit()
	}()

	// Load persisted preferences. Explicit flags still win: something that
	// passed -device meant it for this run.
	settingsStore, err := settings.New(configDir)
	if err != nil {
		log.Printf("Warning: failed to load settings: %v", err)
		settingsStore, _ = settings.New("")
	}
	stored := settingsStore.Get()

	if devicePathFlag == "" {
		devicePathFlag = stored.DevicePath
	}
	if !isFlagSet("device-port") && stored.Port > 0 {
		agent.DevicePort = stored.Port
	}
	// Asked for on the command line or in the environment, as opposed to
	// remembered from a previous run: a stored preference may raise the
	// requirement but not withdraw one asked for there. The notice waits until
	// both sources are in, so it reports what is actually in force.
	askedForPairing := requirePairedFlag || os.Getenv("DAVI_NFC_REQUIRE_PAIRED_DEVICES") == "1"
	agent.RequirePairedDevice, agent.RequirePairedDeviceLocked = resolveRequirePaired(askedForPairing, stored.RequirePairedDevice)
	if agent.RequirePairedDevice {
		log.Printf("Paired devices required: the shared secret and loopback bypass no longer admit a device")
		if devices.Count() == 0 {
			log.Printf("Warning: no devices are paired yet, so every device connection will be refused until one pairs")
		}
	}
	applySettings(agent, stored)

	// Nil in a -tags nowebui build, which is why everything below tolerates it.
	console := setupConsole(agent, settingsStore, logRing)

	// Redraw the console whenever something changes it from elsewhere.
	origins.OnChange(console.NotifyChange)
	devices.OnChange(console.NotifyChange)

	// Create and run systray app
	app := NewSystrayApp(agent, devicePathFlag, bootstrapPortFlag, bootstrapServer)
	app.AttachConsole(console)
	app.Run()
}

// resolveRequirePaired settles the paired-device requirement from its two
// sources. Either can turn it on. Only the command line locks it on: a stored
// preference that says false must not withdraw a requirement an operator asked
// for on the command line, which is the one direction that costs security
// rather than convenience.
func resolveRequirePaired(askedForPairing, stored bool) (require, locked bool) {
	return askedForPairing || stored, askedForPairing
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
