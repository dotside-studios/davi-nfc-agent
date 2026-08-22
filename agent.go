package main

import (
	"errors"
	"log"
	"os"
	"sync"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/clientserver"
	"github.com/dotside-studios/davi-nfc-agent/server/deviceserver"
	"github.com/dotside-studios/davi-nfc-agent/server/unifiedserver"
	"github.com/dotside-studios/davi-nfc-agent/settings"
	"github.com/dotside-studios/davi-nfc-agent/surface"
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

type Agent struct {
	Logger           *log.Logger
	Manager          nfc.Manager // NFC device manager (supports hardware and smartphone)
	Reader           *nfc.NFCReader
	AllowedCardTypes map[string]bool // Card type filter; guarded by settingsMu
	APISecret        string
	ConfigDir        string // Config directory; used for persisting the API secret

	// AllowedOrigins extends the same-origin policy on both WebSocket
	// endpoints. A browser page served from anywhere other than the agent's
	// own host:port — which is every hosted console — needs its origin listed
	// here, or the upgrade is rejected as cross-site.
	//
	// Ignored when Origins is set, which is the normal path.
	AllowedOrigins []string

	// Origins is the live allowlist. Unlike AllowedOrigins it can be changed
	// while the agent runs, and reports rejections so they can be surfaced.
	Origins *OriginStore

	// Server architecture. The device and client endpoints are served from a
	// single listener (UnifiedServer) on DevicePort. DeviceServer and
	// ClientServer hold the device/client logic; the unified server fronts
	// both and routes each /ws connection to the right one.
	Bridge        *server.ServerBridge
	UnifiedServer *unifiedserver.Server
	DeviceServer  *deviceserver.Server
	ClientServer  *clientserver.Server
	DevicePort    int // Single agent server port. Default: 9470

	// endpoints is the register of addresses this agent hands out, reached
	// through Endpoints. It is a value rather than a pointer because its zero
	// value is ready to use and an agent without one would leave every
	// publisher checking for nil.
	endpoints surface.Endpoints

	// PublicKeyPin identifies this agent to devices across certificate
	// reissues, so they need no certificate authority to recognize it.
	PublicKeyPin string

	// Devices holds the paired devices and their per-device credentials.
	Devices *DeviceRegistry

	// Console serves the control center's privileged API. Nil disables it.
	Console *Console

	// consoleHost is the adapter the console administers the agent through.
	consoleHost any

	// Bootstrap is the pairing server, or nil when pairing is disabled.
	Bootstrap *tls.BootstrapServer

	// BootstrapPort is the pairing server's port, 0 when disabled.
	BootstrapPort int

	// RequirePairedDevice admits only devices holding a paired credential,
	// withdrawing the shared secret and loopback bypass for device
	// connections. Browser clients are unaffected.
	RequirePairedDevice bool

	// ReaderMode is the access mode the reader runs in, and ReaderFeedback has
	// it flash its LED and sound its buzzer at what it reads and writes. Both
	// are held here as well as on the reader, because the reader is built in
	// Start, after the stored settings have been applied. A preference that
	// only reached the reader would be lost with every reader the agent starts.
	ReaderMode     nfc.ReaderMode
	ReaderFeedback bool

	// PinnedDevice is the reader the operator chose, empty for auto-detect. It
	// is the preference, not the reader in use: with the pinned one absent the
	// agent runs without a reader and takes it up when it appears.
	PinnedDevice string

	// TLS configuration (optional, used by the unified server)
	CertFile   string       // Path to TLS certificate file
	KeyFile    string       // Path to TLS private key file
	TLSManager *tls.Manager // TLS manager for auto-TLS and network watching

	// Internal state
	devicePath string // Current device path

	// explicit marks the settings the launcher set, which nothing this run may
	// change. Assigned once through SetExplicit before the agent serves
	// anything, and read from every goroutine after that.
	explicit settings.Explicit

	// settingsMu guards the settings state: the reader mode, the pinned reader,
	// the card-type filter, the port, and the feedback and pairing preferences.
	// The console changes them from its own goroutines and reads them back for
	// every snapshot it draws, and the tray does the same from its dispatch
	// goroutine.
	//
	// The exported fields it covers are assigned directly at startup, before
	// there is a second goroutine; after that everything goes through the
	// accessors in agent_settings.go. The card-type filter is the one that
	// cannot be left to chance, since a write during a read of a map takes the
	// process down rather than merely racing.
	settingsMu sync.RWMutex

	serversMu         sync.Mutex    // Protects server restart operations
	serverRestartChan chan struct{} // Signals when servers are restarted
}

func NewAgent(nfcManager nfc.Manager) *Agent {
	return &Agent{
		Logger:            log.New(os.Stderr, "[agent] ", log.LstdFlags),
		Manager:           nfcManager,
		AllowedCardTypes:  make(map[string]bool),
		ReaderMode:        nfc.ModeReadWrite,
		DevicePort:        9470,
		serverRestartChan: make(chan struct{}, 1),
	}
}

// ServerRestarts returns a channel that signals when servers are restarted
// due to network changes or certificate regeneration.
func (a *Agent) ServerRestarts() <-chan struct{} {
	return a.serverRestartChan
}

func (a *Agent) Start(devicePath string) error {
	if a.Reader != nil {
		if devicePath == a.Reader.DevicePath() {
			a.Logger.Printf("NFC reader already running on device: %s", devicePath)
			return nil
		}
		return errors.New("agent is already running")
	}

	// A pinned phone is not a reader that has gone missing, it is one that
	// never existed: a phone reports its scans over the device bridge and is
	// never opened here. Left in place it becomes a connection retried for as
	// long as the agent runs.
	if nfc.IsRemoteDevice(a.Manager, devicePath) {
		a.Logger.Printf("Ignoring pinned reader %s: a phone reports its scans over the device bridge rather than being read from", devicePath)
		devicePath = ""
	}

	// If no device path specified, discover available devices
	if devicePath == "" {
		devices, err := nfc.ListReaders(a.Manager)
		if err != nil {
			a.Logger.Printf("Error listing NFC devices: %v", err)
			// Continue without a device - one may connect later
		} else if len(devices) == 0 {
			a.Logger.Println("No NFC devices found - waiting for device connection")
		} else {
			devicePath = devices[0]
			a.Logger.Printf("Auto-selected NFC device: %s", devicePath)
		}
	}

	// Store device path for potential restarts
	a.devicePath = devicePath

	// Create NFC reader with manager (supports both hardware and smartphone devices)
	nfcReader, err := nfc.NewNFCReader(devicePath, a.Manager, 5*time.Second)
	if err != nil {
		a.Logger.Printf("Error initializing NFC reader: %v", err)
		return err
	}

	a.Reader = nfcReader
	a.adoptReaderSettings()

	// Start network watcher if TLS manager is configured
	if a.TLSManager != nil {
		go a.watchNetworkChanges()
	}

	// Start the servers using shared code
	return a.startServers()
}

func (a *Agent) Stop() {
	if a.Reader == nil && a.DeviceServer == nil {
		a.Logger.Println("Agent is not running")
		return
	}

	a.Logger.Println("Stopping agent...")
	a.withdrawEndpoints()

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

	a.Logger.Println("Agent stopped successfully")
}

// Shutdown stops the agent and releases what outlives a stop.
//
// The manager is built once for the process and survives Stop, since the tray
// and the console can stop and start the agent again against the same one.
// Closing it belongs here, on the way out.
func (a *Agent) Shutdown() {
	a.Stop()

	if closer, ok := a.Manager.(interface{ Close() }); ok {
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
	if a.TLSManager == nil {
		return
	}

	ch := a.TLSManager.WatchNetworkChanges()
	for range ch {
		a.Logger.Println("Network change detected, restarting servers with new certificates...")
		if err := a.RestartServers(); err != nil {
			a.Logger.Printf("Failed to restart servers: %v", err)
		}
	}
}

// RestartServers stops and restarts the HTTP/WebSocket servers with current TLS configuration.
// The NFC reader continues running during the restart.
func (a *Agent) RestartServers() error {
	a.serversMu.Lock()
	defer a.serversMu.Unlock()

	a.Logger.Println("Restarting servers...")

	// Stop servers
	a.stopServers()

	// Brief pause to allow ports to be released
	time.Sleep(100 * time.Millisecond)

	// Restart servers
	if err := a.startServers(); err != nil {
		return err
	}

	a.Logger.Println("Servers restarted successfully")

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
	a.withdrawEndpoints()

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

	// Create device server (handles NFC device connections)
	requirePaired := a.RequiresPairedDevice()

	a.DeviceServer = deviceserver.New(deviceserver.Config{
		Reader:           a.Reader,
		DeviceManager:    findDeviceDriver(a.Manager),
		APISecret:        a.APISecret,
		AllowedCardTypes: a.AllowedCardTypes,
		AllowedOrigins:   a.AllowedOrigins,
		OriginPolicy:     a.originPolicy(),
		PublicKeyPin:     a.PublicKeyPin,
		TokenVerifier:    a.tokenVerifier(),

		RequirePairedDevice: requirePaired,
	}, a.Bridge)

	// Create client server (handles web client connections)
	a.ClientServer = clientserver.New(clientserver.Config{
		APISecret:      a.APISecret,
		AllowedOrigins: a.AllowedOrigins,
		OriginPolicy:   a.originPolicy(),
		TokenVerifier:  a.tokenVerifier(),
		OnChange:       a.clientsChanged(),
	}, a.Bridge)

	// Single listener fronts the device, client, control and console handlers.
	port := a.ConfiguredPort()
	a.UnifiedServer = unifiedserver.New(unifiedserver.Config{
		Port:           port,
		CertFile:       a.CertFile,
		KeyFile:        a.KeyFile,
		ControlHandler: consoleRoutes(a.Console),
		UIHandler:      consoleAssets(),
	}, a.DeviceServer, a.ClientServer)

	go func() {
		if err := a.UnifiedServer.Start(); err != nil {
			a.Logger.Printf("Unified server error: %v", err)
		}
	}()

	a.Logger.Printf("Server started on port %d (NFC devices + web clients)", port)

	// The addresses go out once there is something answering on them, and are
	// withdrawn again in stopServers.
	a.publishEndpoints()
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
	if a.ConfigDir == "" {
		return "", errors.New("config dir not configured")
	}

	fresh, err := rotateAPISecret(a.ConfigDir)
	if err != nil {
		return "", err
	}

	a.APISecret = fresh
	a.Logger.Println("API secret rotated; restarting servers…")
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

// SetCardTypeFilter replaces the whole filter, which is what an operator
// picking types is doing. An empty list is no filter at all.
func (a *Agent) SetCardTypeFilter(cardTypes []string) {
	next := settings.Normalize(settings.Settings{CardTypes: cardTypes}).CardTypes
	if sameCardTypes(a.CardTypeFilter(), next) {
		return
	}
	if a.launcherHolds("the card-type filter", a.Explicit().CardTypes) {
		return
	}

	a.settingsMu.Lock()
	for cardType := range a.AllowedCardTypes {
		delete(a.AllowedCardTypes, cardType)
	}
	for _, cardType := range next {
		a.AllowedCardTypes[cardType] = true
	}
	a.settingsMu.Unlock()

	a.notifyConsole()
}

// ClearCardTypeFilter drops the filter, so every card is accepted. That is not
// the same as allowing each known type: a phone reports the tag types it
// recognizes, which need not be ones this agent enumerates, and listing the
// known types would refuse the rest.
//
// The map is emptied in place, never replaced: the running device server was
// handed this same map at construction.
func (a *Agent) ClearCardTypeFilter() {
	a.settingsMu.Lock()
	for cardType := range a.AllowedCardTypes {
		delete(a.AllowedCardTypes, cardType)
	}
	a.settingsMu.Unlock()

	a.notifyConsole()
}

func (a *Agent) AllowedCardTypesLength() int {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return len(a.AllowedCardTypes)
}

func (a *Agent) AllowCardType(cardType string) {
	a.settingsMu.Lock()
	a.AllowedCardTypes[cardType] = true
	a.settingsMu.Unlock()

	a.notifyConsole()
}

func (a *Agent) DisallowCardType(cardType string) {
	a.settingsMu.Lock()
	delete(a.AllowedCardTypes, cardType)
	a.settingsMu.Unlock()

	a.notifyConsole()
}

// IsCardTypeAllowed answers the question the device server asks of the filter:
// an empty filter admits everything, including a type this agent does not know.
func (a *Agent) IsCardTypeAllowed(cardType string) bool {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return len(a.AllowedCardTypes) == 0 || a.AllowedCardTypes[cardType]
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
	if a.RequiresPairedDevice() == on {
		return
	}
	if a.launcherHolds("the paired-device requirement", a.Explicit().RequirePairedDevice) {
		return
	}

	a.settingsMu.Lock()
	a.RequirePairedDevice = on
	server := a.DeviceServer
	a.settingsMu.Unlock()

	if server != nil {
		server.SetRequirePairedDevice(on)
	}
	a.notifyConsole()
}

// RequiresPairedDevice reports the requirement as it stands.
func (a *Agent) RequiresPairedDevice() bool {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.RequirePairedDevice
}

// SetReaderMode changes the reader's access mode, on a running reader as well
// as on the next one the agent starts. Accepted with no reader running, because
// the mode is the agent's and Start hands it to the reader it builds.
func (a *Agent) SetReaderMode(mode nfc.ReaderMode) {
	if a.CurrentReaderMode() == mode {
		return
	}
	if a.launcherHolds("the reader mode", a.Explicit().Mode) {
		return
	}

	a.settingsMu.Lock()
	a.ReaderMode = mode
	a.settingsMu.Unlock()

	if a.Reader != nil {
		a.Reader.SetMode(mode)
	}
	a.notifyConsole()
}

// SetReaderFeedback turns the reader's LED and buzzer feedback on or off, on a
// running reader as well as on the next one the agent starts.
func (a *Agent) SetReaderFeedback(on bool) {
	if a.ReaderFeedbackEnabled() == on {
		return
	}
	if a.launcherHolds("reader feedback", a.Explicit().ReaderFeedback) {
		return
	}

	a.settingsMu.Lock()
	a.ReaderFeedback = on
	a.settingsMu.Unlock()

	if a.Reader != nil {
		a.Reader.SetFeedback(on)
	}
	a.notifyConsole()
}

// ServingPort is the port the listener is bound on. It matches the configured
// port until one is saved, and again once the listener has been rebound.
func (a *Agent) ServingPort() int {
	if a.UnifiedServer != nil {
		return a.UnifiedServer.Port()
	}
	return a.ConfiguredPort()
}

// ConfiguredPort is the port the agent is set to serve on, which it binds the
// next time its listener comes up.
func (a *Agent) ConfiguredPort() int {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.DevicePort
}

// clientsChanged returns a hook that refreshes the console when the client list
// moves, or nil when there is no console to refresh.
func (a *Agent) clientsChanged() func() {
	if a.Console == nil {
		return nil
	}
	return a.Console.NotifyChange
}

// tokenVerifier returns the device registry as a token verifier, or nil when
// there is none. As with originPolicy, a typed nil would satisfy the interface
// and defeat the caller's nil check.
func (a *Agent) tokenVerifier() server.TokenVerifier {
	if a.Devices == nil {
		return nil
	}
	return a.Devices
}

func (a *Agent) originPolicy() server.OriginPolicy {
	if a.Origins == nil {
		return nil
	}
	return a.Origins
}
