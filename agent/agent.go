package agent

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/buildinfo"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/clientserver"
	"github.com/dotside-studios/davi-nfc-agent/server/tagrouter"
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
// methods of their own -- SetRequirePairedDevice, SetAllowCardType.
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

	// Devices routes operations to paired devices, and DeviceScans carries what
	// they scan. Both come from a driver the caller built, because the caller
	// is what knows one is wanted: an agent with neither serves its own reader
	// and nothing else.
	//
	// The agent takes them as an interface and a channel rather than a driver,
	// so it names no device protocol and cannot reach past what it was given.
	RemoteOps   server.DeviceOps
	RemoteScans <-chan nfc.NFCData

	// DeviceEndpoint builds the handler serving device connections on the
	// shared /ws path.
	//
	// A function, because the policies are the agent's and the wire is the
	// driver's: the agent hands over what it decides and the caller builds the
	// handler, so neither names the other's types. Nil serves clients only.
	DeviceEndpoint func(DeviceEndpointOptions) http.Handler

	// Server is the listener the agent serves from. The caller builds it and
	// mounts whatever else it wants served from the same port, which is how a
	// control center is attached: mount it, or do not and there is none.
	//
	// Nil is an agent with no HTTP surface, for a program driving the reader
	// directly. New mounts the agent's own routes on a non-nil one.
	Server *unifiedserver.Server

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

	// UnifiedServer is the listener the device and client endpoints are served
	// from, routing each /ws connection to the device driver or the client
	// server. It is the one the caller supplied, held for the agent's whole
	// life: a stop leaves it stopped rather than dropping it, so a restart
	// serves from the same listener with the same routes mounted on it.
	//
	// Router decides which tag source a client request applies to, and
	// DeviceAuth gates the device endpoint. Both are nil until Start.
	UnifiedServer *unifiedserver.Server
	ClientServer  *clientserver.Server

	// serving is what the mounted routes dispatch to, replaced on every start
	// and cleared on stop.
	serving atomic.Pointer[endpoints]

	// reader mirrors Reader for the handlers, which read it from their own
	// goroutines and must not take the lifecycle lock to do so.
	reader     atomic.Pointer[nfc.NFCReader]
	Router     *tagrouter.Router
	DeviceAuth *server.DeviceAuth

	// Settled at construction.
	info                buildinfo.Info
	logger              *log.Logger
	manager             nfc.Manager
	deviceEndpoint      http.Handler
	remoteOps           server.DeviceOps
	remoteScans         <-chan nfc.NFCData
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
	onTag             []func(nfc.NFCData)
	clientHooks       []func()
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
		remoteOps:           cfg.RemoteOps,
		remoteScans:         cfg.RemoteScans,
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

	// Built here rather than at start, so a caller can put its device endpoint
	// behind it before anything runs.
	a.DeviceAuth = server.NewDeviceAuth(cfg.APISecret, a.tokenVerifier(), a.requirePairedDevice)
	if cfg.DeviceEndpoint != nil {
		a.deviceEndpoint = cfg.DeviceEndpoint(DeviceEndpointOptions{
			Authenticate:         a.DeviceAuth.Check,
			CheckOrigin:          a.checkOrigin(),
			AllowTagModification: a.TagModificationAllowed,
			PublicKeyPin:         a.publicKeyPin,
		})
	}

	// Registered here rather than left to the caller: the pairing server's
	// lifetime is the agent's, and forgetting to wire it is exactly the class
	// of mistake this component list exists to remove.
	if cfg.Pairing != nil {
		a.components = append(a.components, cfg.Pairing)
	}

	// The agent's own routes go on before the caller's, so a mount cannot
	// displace /ws or the health checks.
	if cfg.Server != nil {
		a.UnifiedServer = cfg.Server
		if err := a.MountOn(cfg.Server); err != nil {
			logger.Printf("Failed to mount the agent's routes: %v", err)
		}
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
	a.reader.Store(nfcReader)

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
	if a.Reader == nil && a.serving.Load() == nil {
		return
	}

	a.logger.Println("Stopping agent...")

	if a.UnifiedServer != nil {
		a.UnifiedServer.Stop()
	}

	a.ClientServer = nil
	a.Router = nil

	a.serving.Store(nil)

	if a.Reader != nil {
		a.Reader.Stop()
		a.Reader = nil
		a.reader.Store(nil)
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
	}

	a.ClientServer = nil
	a.Router = nil

	a.serving.Store(nil)
}

// startServers starts the HTTP/WebSocket servers.
func (a *Agent) startServers() error {
	if a.Reader == nil {
		return errors.New("reader not initialized")
	}

	// Routes each client request to whichever source holds the tag it names.
	a.Router = tagrouter.New(tagrouter.Config{Reader: a.Reader, Devices: a.remoteOps})

	a.ClientServer = clientserver.New(clientserver.Config{
		APISecret:      a.apiSecret,
		AllowedOrigins: a.allowedOrigins,
		OriginPolicy:   a.originPolicy(),
		TokenVerifier:  a.tokenVerifier(),
		Ops:            a.Router,
		OnChange:       a.clientsChanged(),
		OnTag:          a.tagObserver(),
	})

	// The agent's tag sources feed the client server directly. There is no
	// channel between them any more, so there is nothing to drain and nothing
	// to remember to start.
	a.pumpCtx, a.pumpCancel = context.WithCancel(context.Background())
	a.Reader.Start()
	go a.pumpReader(a.pumpCtx, a.Reader, a.ClientServer)
	if a.remoteScans != nil {
		go pumpDevices(a.pumpCtx, a.remoteScans, a.ClientServer)
	}

	// Published as a pair, so a request never sees a client from one start
	// beside a device from the next.
	a.serving.Store(&endpoints{
		client: http.HandlerFunc(a.ClientServer.ServeWS),
		device: a.deviceEndpoint,
	})

	// The listener is the caller's, mounted before the agent starts. A nil one
	// is an agent with no HTTP surface at all, which is what a program driving
	// the reader directly wants.
	if a.UnifiedServer == nil {
		a.logger.Println("No server mounted; serving no HTTP")
		return nil
	}

	// Binds before returning, so a port already in use fails the start rather
	// than leaving the agent reporting itself running with nothing listening.
	if err := a.UnifiedServer.Start(); err != nil {
		return err
	}

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

// checkOrigin admits or rejects a device upgrade by Origin, preferring the
// live policy over the static allowlist so the tray can admit one without
// restarting the listener.
func (a *Agent) checkOrigin() func(r *http.Request) bool {
	if policy := a.originPolicy(); policy != nil {
		return server.CheckOriginPolicy(policy)
	}
	return server.CheckOrigin(a.allowedOrigins)
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
	if a.DeviceAuth != nil {
		a.DeviceAuth.SetRequirePaired(on)
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

// clientsChanged returns the hook that runs when the client list moves, or nil
// when nothing registered one.
func (a *Agent) clientsChanged() func() {
	a.hooksMu.Lock()
	defer a.hooksMu.Unlock()
	if len(a.clientHooks) == 0 {
		return nil
	}
	hooks := make([]func(), len(a.clientHooks))
	copy(hooks, a.clientHooks)
	return func() {
		for _, fn := range hooks {
			fn()
		}
	}
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
