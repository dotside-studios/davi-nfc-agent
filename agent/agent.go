package agent

import (
	"context"
	"errors"
	"log"
	"os"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/buildinfo"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/clientserver"
	"github.com/dotside-studios/davi-nfc-agent/server/deviceserver"
	"github.com/dotside-studios/davi-nfc-agent/server/unifiedserver"
	"github.com/dotside-studios/davi-nfc-agent/tls"
)

// GetAllCardTypeFilterNames returns all card type filter names from nfc package constants
func GetAllCardTypeFilterNames() []string {
	return nfc.GetAllCardTypes()
}

// GetCardTypeFilterDisplayName returns a user-friendly display name for a card type
func GetCardTypeFilterDisplayName(cardType string) string {
	return cardType
}

// GetCardTypeFilterTooltip returns a tooltip for a card type filter
func GetCardTypeFilterTooltip(cardType string) string {
	return "Allow " + cardType + " only"
}

// Config is the agent's settled configuration. New copies it in, and nothing
// afterwards can change it: the fields below are read through the accessors on
// Agent, so a caller holding a running agent cannot rebind its port, swap its
// origin allowlist or withdraw its pairing requirement behind the servers'
// backs. The few settings that may legitimately change while running have
// methods of their own -- SetRequirePairedDevice, SetAllowCardType, SetConsole.
type Config struct {
	// Manager supplies the readers. Required; New panics without one, because
	// an agent with no way to enumerate a reader cannot be started later.
	Manager nfc.Manager

	// Info is what this build calls itself. Blank fields fall back to the
	// agent's own identity, so a program embedding it can rename just the
	// parts it cares about -- and should at least set DirName, or its
	// configuration lands in this agent's directory.
	Info buildinfo.Info

	// Logger receives the agent's diagnostics. Nil installs one writing to
	// stderr with an [agent] prefix.
	Logger *log.Logger

	// DevicePort is the single listener serving both devices and clients.
	// Zero means DefaultDevicePort.
	DevicePort int

	// APISecret is the shared secret for the session handshake. Empty admits
	// unauthenticated connections, which is the development default.
	APISecret string

	// ConfigDir is where the API secret and other state persist.
	ConfigDir string

	// AllowedOrigins extends the same-origin policy on both WebSocket
	// endpoints. A browser page served from anywhere other than the agent's
	// own host:port -- which is every hosted console -- needs its origin listed
	// here, or the upgrade is rejected as cross-site.
	//
	// Ignored when Origins is set, which is the normal path.
	AllowedOrigins []string

	// Origins is the live allowlist. Unlike AllowedOrigins its contents can
	// change while the agent runs, and it reports rejections so they can be
	// surfaced. The store is mutable; which store is in use is not.
	Origins *OriginStore

	// Devices holds the paired devices and their per-device credentials.
	Devices *DeviceRegistry

	// PublicKeyPin identifies this agent to devices across certificate
	// reissues, so they need no certificate authority to recognize it.
	PublicKeyPin string

	// Pairing is the pairing server, registered as a component so it starts and
	// stops with the agent. Nil disables pairing entirely. Everything it needs
	// lives on PairingConfig rather than here.
	Pairing *PairingServer

	// RequirePairedDevice admits only devices holding a paired credential,
	// withdrawing the shared secret and loopback bypass for device
	// connections. Browser clients are unaffected. Changeable at runtime
	// through SetRequirePairedDevice, which also tells the running server.
	RequirePairedDevice bool

	// RequirePairedDeviceLocked stops anything lowering that requirement for
	// the rest of the run. Set it when the requirement came from the command
	// line or the environment: an operator who asked for it there should not
	// have it withdrawn by a preference file or a toggle in the console.
	RequirePairedDeviceLocked bool

	// TLS configuration, used by the unified server. TLSManager also drives
	// certificate regeneration and network-change watching.
	CertFile   string
	KeyFile    string
	TLSManager *tls.Manager
}

// Agent runs the NFC reader and the servers in front of it. Build one with New;
// its configuration is fixed from that point, and the exported fields below are
// the parts that come and go as it runs.
type Agent struct {
	// Reader is the reader currently open, nil before Start and after Stop.
	Reader *nfc.NFCReader

	// The device and client endpoints are served from a single listener
	// (UnifiedServer) on the configured port. DeviceServer and ClientServer
	// hold the device/client logic; the unified server fronts both and routes
	// each /ws connection to the right one. All are nil until Start.
	Bridge        *server.ServerBridge
	UnifiedServer *unifiedserver.Server
	DeviceServer  *deviceserver.Server
	ClientServer  *clientserver.Server

	// Settled at construction.
	info                buildinfo.Info
	logger              *log.Logger
	manager             nfc.Manager
	apiSecret           string
	configDir           string
	allowedOrigins      []string
	origins             *OriginStore
	devices             *DeviceRegistry
	devicePort          int
	publicKeyPin        string
	pairing             *PairingServer
	certFile            string
	keyFile             string
	tlsManager          *tls.Manager
	requirePairedDevice bool
	requirePairedLocked bool

	// Mutable state. lifecycle carries the state machine, the hooks and the
	// registered components; every transition below runs under its lock.
	lifecycle
	cardTypes *cardTypeFilter

	// pumpCtx bounds the goroutines draining the reader and the paired devices
	// onto the bridge. Cancelled by stopServers, so they end with the bridge
	// they feed.
	pumpCtx           context.Context
	pumpCancel        context.CancelFunc
	console           Console
	onTag             []func(nfc.NFCData)
	devicePath        string
	serverRestartChan chan struct{} // Signals when servers are restarted
}

// New builds an agent from cfg. The configuration is copied, so later changes
// to cfg do not reach the agent.
func New(cfg Config) *Agent {
	if cfg.Manager == nil {
		panic("agent: Config.Manager is required")
	}

	logger := cfg.Logger
	if logger == nil {
		logger = log.New(os.Stderr, "[agent] ", log.LstdFlags)
	}

	port := cfg.DevicePort
	if port == 0 {
		port = DefaultDevicePort
	}

	a := &Agent{
		info:                cfg.Info.OrDefault(),
		logger:              logger,
		manager:             cfg.Manager,
		apiSecret:           cfg.APISecret,
		configDir:           cfg.ConfigDir,
		allowedOrigins:      cfg.AllowedOrigins,
		origins:             cfg.Origins,
		devices:             cfg.Devices,
		devicePort:          port,
		publicKeyPin:        cfg.PublicKeyPin,
		pairing:             cfg.Pairing,
		certFile:            cfg.CertFile,
		keyFile:             cfg.KeyFile,
		tlsManager:          cfg.TLSManager,
		requirePairedDevice: cfg.RequirePairedDevice || cfg.RequirePairedDeviceLocked,
		requirePairedLocked: cfg.RequirePairedDeviceLocked,
		cardTypes:           newCardTypeFilter(),
		serverRestartChan:   make(chan struct{}, 1),
	}

	// Registered here rather than left to the caller: the pairing server's
	// lifetime is the agent's, and forgetting to wire it is exactly the class
	// of mistake this component list exists to remove.
	if cfg.Pairing != nil {
		a.components = append(a.components, cfg.Pairing)
	}

	return a
}

// Configuration readers. These exist because the tray and the console display
// what the agent was built with; none of them can change it.

// Info reports what this build calls itself.
func (a *Agent) Info() buildinfo.Info { return a.info }

func (a *Agent) Manager() nfc.Manager     { return a.manager }
func (a *Agent) Logger() *log.Logger      { return a.logger }
func (a *Agent) APISecret() string        { return a.apiSecret }
func (a *Agent) ConfigDir() string        { return a.configDir }
func (a *Agent) Origins() *OriginStore    { return a.origins }
func (a *Agent) Devices() *DeviceRegistry { return a.devices }
func (a *Agent) DevicePort() int          { return a.devicePort }
func (a *Agent) PublicKeyPin() string     { return a.publicKeyPin }

// Pairing returns the pairing component, or nil when pairing is disabled.
func (a *Agent) Pairing() *PairingServer { return a.pairing }

// Bootstrap returns the pairing server itself, for the PIN and the URLs the
// tray and console show. Nil when pairing is disabled.
func (a *Agent) Bootstrap() *tls.BootstrapServer {
	if a.pairing == nil {
		return nil
	}
	return a.pairing.Server()
}

// BootstrapPort reports the pairing server's port, 0 when disabled.
func (a *Agent) BootstrapPort() int {
	if a.pairing == nil {
		return 0
	}
	return a.pairing.Port()
}
func (a *Agent) CertFile() string          { return a.certFile }
func (a *Agent) KeyFile() string           { return a.keyFile }
func (a *Agent) TLSManager() *tls.Manager  { return a.tlsManager }
func (a *Agent) RequirePairedDevice() bool { return a.requirePairedDevice }

// RequirePairedDeviceLocked reports that the requirement came from the command
// line and cannot be lowered while this agent runs.
func (a *Agent) RequirePairedDeviceLocked() bool { return a.requirePairedLocked }

// Console returns the control center, or nil when there is none.
func (a *Agent) Console() Console { return a.console }

// SetConsole attaches the control center. Pass a nil Console to detach.
//
// Check the concrete value for nil before calling: a typed nil satisfies
// Console and would defeat every nil check downstream.
func (a *Agent) SetConsole(c Console) { a.console = c }

// ServerRestarts returns a channel that signals when servers are restarted
// due to network changes or certificate regeneration.
func (a *Agent) ServerRestarts() <-chan struct{} {
	return a.serverRestartChan
}

// startLocked opens the reader and brings the servers up. The caller holds the
// lifecycle lock and owns the state transition; see Start.
func (a *Agent) startLocked(devicePath string) error {
	// A pinned phone is not a reader that has gone missing, it is one that
	// never existed: a phone reports its scans over the device bridge and is
	// never opened here. Left in place it becomes a connection retried for as
	// long as the agent runs.
	if nfc.IsRemoteDevice(a.manager, devicePath) {
		a.logger.Printf("Ignoring pinned reader %s: a phone reports its scans over the device bridge rather than being read from", devicePath)
		devicePath = ""
	}

	// If no device path specified, discover available devices
	if devicePath == "" {
		devices, err := nfc.ListReaders(a.manager)
		if err != nil {
			a.logger.Printf("Error listing NFC devices: %v", err)
			// Continue without a device - one may connect later
		} else if len(devices) == 0 {
			a.logger.Println("No NFC devices found - waiting for device connection")
		} else {
			devicePath = devices[0]
			a.logger.Printf("Auto-selected NFC device: %s", devicePath)
		}
	}

	// Store device path for potential restarts
	a.devicePath = devicePath

	// Create NFC reader with manager (supports both hardware and smartphone devices)
	nfcReader, err := nfc.NewNFCReader(devicePath, a.manager, 5*time.Second)
	if err != nil {
		a.logger.Printf("Error initializing NFC reader: %v", err)
		return err
	}

	a.Reader = nfcReader

	// Start network watcher if TLS manager is configured
	if a.tlsManager != nil {
		go a.watchNetworkChanges()
	}

	// Start the servers using shared code
	return a.startServers()
}

// stopLocked tears down the servers and the reader. The caller holds the
// lifecycle lock and owns the state transition; see Stop. It is safe to call on
// a partly started agent, which is what makes an aborted Start recoverable.
func (a *Agent) stopLocked() {
	if a.Reader == nil && a.DeviceServer == nil {
		return
	}

	a.logger.Println("Stopping agent...")

	if a.UnifiedServer != nil {
		a.UnifiedServer.Stop()
		a.UnifiedServer = nil
	}

	if a.ClientServer != nil {
		a.ClientServer.Stop()
		a.ClientServer = nil
	}

	if a.DeviceServer != nil {
		a.DeviceServer.Stop()
		a.DeviceServer = nil
	}

	if a.Bridge != nil {
		a.Bridge.Close()
		a.Bridge = nil
	}

	if a.Reader != nil {
		a.Reader.Stop()
		a.Reader = nil
	}

	a.logger.Println("Agent stopped successfully")
}

// Shutdown stops the agent and releases what outlives a stop.
//
// The manager is built once for the process and survives Stop, since the tray
// and the console can stop and start the agent again against the same one.
// Closing it belongs here, on the way out.
func (a *Agent) Shutdown() {
	a.Stop()

	if closer, ok := a.manager.(interface{ Close() }); ok {
		closer.Close()
	}
}

// findDeviceDriver locates the driver serving remote devices, whether the agent
// holds it directly or behind a manager that holds others.
func findDeviceDriver(m nfc.Manager) *remotenfc.Manager {
	if m == nil {
		return nil
	}

	if driver, ok := m.(*remotenfc.Manager); ok {
		return driver
	}

	holder, ok := m.(interface {
		GetManager(string) (nfc.Manager, bool)
	})
	if !ok {
		return nil
	}

	child, exists := holder.GetManager(nfc.ManagerTypeSmartphone)
	if !exists {
		return nil
	}

	driver, _ := child.(*remotenfc.Manager)
	return driver
}

// watchNetworkChanges listens for network changes from TLS manager and restarts servers.
func (a *Agent) watchNetworkChanges() {
	if a.tlsManager == nil {
		return
	}

	ch := a.tlsManager.WatchNetworkChanges()
	for range ch {
		a.logger.Println("Network change detected, restarting servers with new certificates...")
		if err := a.RestartServers(); err != nil {
			a.logger.Printf("Failed to restart servers: %v", err)
		}
	}
}

// RestartServers stops and restarts the HTTP/WebSocket servers with current TLS configuration.
// The NFC reader continues running during the restart.
func (a *Agent) RestartServers() error {
	a.lifecycleMu.Lock()
	defer a.lifecycleMu.Unlock()

	a.logger.Println("Restarting servers...")

	// Stop servers
	a.stopServers()

	// Brief pause to allow ports to be released
	time.Sleep(100 * time.Millisecond)

	// Restart servers
	if err := a.startServers(); err != nil {
		return err
	}

	a.logger.Println("Servers restarted successfully")

	// Notify listeners of server restart
	select {
	case a.serverRestartChan <- struct{}{}:
	default:
		// Channel full, skip
	}

	return nil
}

// stopServers stops only the HTTP/WebSocket servers (not the NFC reader).
func (a *Agent) stopServers() {
	if a.pumpCancel != nil {
		a.pumpCancel()
		a.pumpCtx, a.pumpCancel = nil, nil
	}

	if a.UnifiedServer != nil {
		a.UnifiedServer.Stop()
		a.UnifiedServer = nil
	}

	if a.ClientServer != nil {
		a.ClientServer.Stop()
		a.ClientServer = nil
	}

	if a.DeviceServer != nil {
		a.DeviceServer.Stop()
		a.DeviceServer = nil
	}

	if a.Bridge != nil {
		a.Bridge.Close()
		a.Bridge = nil
	}
}

// startServers starts the HTTP/WebSocket servers.
func (a *Agent) startServers() error {
	if a.Reader == nil {
		return errors.New("reader not initialized")
	}

	// Create bridge for inter-server communication
	a.Bridge = server.NewServerBridge()

	// The reader and the paired devices are the agent's tag sources; it drains
	// them onto the bridge itself rather than a server doing it, since neither
	// has anything to do with serving.
	a.pumpCtx, a.pumpCancel = context.WithCancel(context.Background())
	a.Reader.Start()
	go a.pumpReader(a.pumpCtx, a.Reader, a.Bridge)
	if remote := findDeviceDriver(a.manager); remote != nil {
		go server.PumpTagData(a.pumpCtx, remote.Data(), a.Bridge)
	}

	// Create device server (handles NFC device connections)
	a.DeviceServer = deviceserver.New(deviceserver.Config{
		Reader:         a.Reader,
		DeviceManager:  findDeviceDriver(a.manager),
		APISecret:      a.apiSecret,
		AllowedOrigins: a.allowedOrigins,
		OriginPolicy:   a.originPolicy(),
		PublicKeyPin:   a.publicKeyPin,
		TokenVerifier:  a.tokenVerifier(),

		RequirePairedDevice: a.requirePairedDevice,
	}, a.Bridge)

	// Create client server (handles web client connections)
	a.ClientServer = clientserver.New(clientserver.Config{
		APISecret:      a.apiSecret,
		AllowedOrigins: a.allowedOrigins,
		OriginPolicy:   a.originPolicy(),
		TokenVerifier:  a.tokenVerifier(),
		OnChange:       a.clientsChanged(),
		OnTag:          a.tagObserver(),
	}, a.Bridge)

	// Single listener fronts the device, client, control and console handlers.
	a.UnifiedServer = unifiedserver.New(unifiedserver.Config{
		Port:            a.devicePort,
		CertFile:        a.certFile,
		KeyFile:         a.keyFile,
		ControlHandler:  a.consoleRoutes(),
		UIHandler:       a.consoleAssets(),
		MDNSServiceName: a.info.DisplayName + " Device",
	}, a.DeviceServer, a.ClientServer)

	// Captured, not read from the field: a Stop that lands before this
	// goroutine is scheduled sets a.UnifiedServer to nil, and the goroutine
	// would then call Start on a nil server.
	unified := a.UnifiedServer
	go func() {
		if err := unified.Start(); err != nil {
			a.logger.Printf("Unified server error: %v", err)
		}
	}()

	a.logger.Printf("Server started on port %d (NFC devices + web clients)", a.devicePort)
	return nil
}

// RotateAPISecret generates a fresh API secret, persists it under
// ConfigDir, updates the running servers, and restarts them so the
// new secret takes effect. Existing connections are dropped (clients
// must re-handshake with the new secret).
//
// Returns the new secret. Errors propagate from filesystem ops or
// server restart; on error the previous secret remains in effect.
func (a *Agent) RotateAPISecret() (string, error) {
	if a.configDir == "" {
		return "", errors.New("config dir not configured")
	}

	fresh, err := rotateAPISecret(a.configDir)
	if err != nil {
		return "", err
	}

	a.apiSecret = fresh
	a.logger.Println("API secret rotated; restarting servers…")
	if err := a.RestartServers(); err != nil {
		return fresh, err
	}
	return fresh, nil
}

func (a *Agent) SetAllowCardType(cardType string, allow bool) {
	if allow {
		a.AllowCardType(cardType)
	} else {
		a.DisallowCardType(cardType)
	}
}

func (a *Agent) AllowAllCardTypes() {
	a.cardTypes.allowAll(nfc.GetAllCardTypes())
}

func (a *Agent) AllowedCardTypesLength() int {
	return a.cardTypes.len()
}

func (a *Agent) AllowCardType(cardType string) {
	a.cardTypes.allow(cardType)
}

func (a *Agent) DisallowCardType(cardType string) {
	a.cardTypes.disallow(cardType)
}

func (a *Agent) IsCardTypeAllowed(cardType string) bool {
	return a.cardTypes.explicitlyAllowed(cardType)
}

// CurrentDevicePath returns the current device path from the reader.
// Returns empty string if no reader is active.
func (a *Agent) CurrentDevicePath() string {
	if a.Reader == nil {
		return a.devicePath // Return stored path if reader not started
	}
	return a.Reader.DevicePath()
}

// originPolicy returns the live allowlist as an origin policy, or nil to fall
// back to the static AllowedOrigins list. Returning a typed nil would satisfy
// the interface and defeat that fallback, so the check is explicit.
// SetRequirePairedDevice changes the paired-device requirement on the running
// device server, so the policy can be tried without a restart.
func (a *Agent) SetRequirePairedDevice(on bool) {
	if a.requirePairedLocked && !on {
		// Asked for on the command line. A stored preference or a console
		// toggle may not withdraw it, which is the direction that matters:
		// the operator who set the flag is the one who would be surprised.
		a.logger.Printf("Ignoring request to stop requiring paired devices: it was set on the command line")
		return
	}
	a.requirePairedDevice = on
	if a.DeviceServer != nil {
		a.DeviceServer.SetRequirePairedDevice(on)
	}
}

// OnTag registers fn to receive every scan the agent broadcasts, so a program
// embedding the agent can act on cards without connecting to its own WebSocket
// endpoint. Register before Start: the servers read the set once, when they are
// built.
//
// fn runs on the goroutine that feeds every connected client, so it must not
// block. Hand slow work to a channel of your own.
func (a *Agent) OnTag(fn func(nfc.NFCData)) {
	if fn == nil {
		return
	}
	a.onTag = append(a.onTag, fn)
}

// tagObserver folds the registered observers into one callback, or nil when
// there are none -- the client server checks for nil, and a non-nil func
// calling an empty slice would defeat that.
func (a *Agent) tagObserver() func(nfc.NFCData) {
	if len(a.onTag) == 0 {
		return nil
	}
	observers := a.onTag
	return func(data nfc.NFCData) {
		for _, fn := range observers {
			fn(data)
		}
	}
}

// clientsChanged returns a hook that refreshes the console when the client list
// moves, or nil when there is no console to refresh.
func (a *Agent) clientsChanged() func() {
	if a.console == nil {
		return nil
	}
	return a.console.NotifyChange
}

// tokenVerifier returns the device registry as a token verifier, or nil when
// there is none. As with originPolicy, a typed nil would satisfy the interface
// and defeat the caller's nil check.
func (a *Agent) tokenVerifier() server.TokenVerifier {
	if a.devices == nil {
		return nil
	}
	return a.devices
}

func (a *Agent) originPolicy() server.OriginPolicy {
	if a.origins == nil {
		return nil
	}
	return a.origins
}
