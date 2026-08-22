package main

import (
	"errors"
	"log"
	"os"
	"sync"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/plugin"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/settings"
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

	// DevicePort is the port the agent asks to be served on. What is actually
	// bound is the plugin's business; see ServingPort.
	DevicePort int // Default: 9470

	// plugins is everything that serves this agent, reached through Plugins.
	// The agent does not serve anything itself: the WebSocket endpoints, the
	// pairing page and the control center are plugins the command line
	// registers, and a build that registers none of them still reads cards.
	pluginsMu sync.Mutex
	plugins   *plugin.Host

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

	// stateDone stops the state watcher, closed once on the way out.
	stateDone chan struct{}
	stateOnce sync.Once
}

func NewAgent(nfcManager nfc.Manager) *Agent {
	agent := &Agent{
		Logger:            log.New(os.Stderr, "[agent] ", log.LstdFlags),
		Manager:           nfcManager,
		AllowedCardTypes:  make(map[string]bool),
		ReaderMode:        nfc.ModeReadWrite,
		DevicePort:        9470,
		serverRestartChan: make(chan struct{}, 1),
		stateDone:         make(chan struct{}),
	}

	return agent
}

// Plugins is everything that serves this agent, and the register of addresses
// they hand out. The command line fills it before the agent starts:
//
//	agent.Plugins().Use(wsserver.New(...), pairing.New(...))
//
// The runtime is built on first use rather than in a constructor, so an agent
// assembled field by field — which is how a test builds one — has one too.
// Where the plugins put their menus is settled later, when there is a tray to
// put them on.
func (a *Agent) Plugins() *plugin.Host {
	a.pluginsMu.Lock()
	defer a.pluginsMu.Unlock()

	if a.plugins == nil {
		a.plugins = plugin.New(plugin.Config{
			Logf:      log.Printf,
			Clipboard: copyValue,
			Browser:   openBrowser,
		})
	}
	return a.plugins
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

	// Then everything that serves the reader, in the order it was registered.
	// The agent serves nothing itself, so a build that registered no plugins
	// simply has a reader running.
	//
	// One that will not serve is reported rather than fatal: a pairing port
	// already in use is not a reason to call the agent failed to start, and the
	// reader is what the agent is.
	if err := a.Plugins().Start(); err != nil {
		a.Logger.Printf("Not everything that serves this agent came up: %v", err)
	}

	a.PublishState()
	return nil
}

func (a *Agent) Stop() {
	if a.Reader == nil && !a.Plugins().Running() {
		a.Logger.Println("Agent is not running")
		return
	}

	a.Logger.Println("Stopping agent...")

	// The plugins first, in the reverse of the order they were registered: they
	// are serving the reader, and one of them may still be answering a request
	// against it.
	if err := a.Plugins().Stop(); err != nil {
		a.Logger.Printf("Something did not stop cleanly: %v", err)
	}

	if a.Reader != nil {
		a.Reader.Stop()
		a.Reader = nil
	}

	a.PublishState()
	a.Logger.Println("Agent stopped successfully")
}

// Shutdown stops the agent and releases what outlives a stop.
//
// The manager is built once for the process and survives Stop, since the tray
// and the console can stop and start the agent again against the same one.
// Closing it belongs here, on the way out.
func (a *Agent) Shutdown() {
	a.Stop()
	a.stateOnce.Do(func() {
		if a.stateDone != nil {
			close(a.stateDone)
		}
	})

	// Stop is what a plugin comes back from; this is the one it does not, so a
	// plugin holding a file or a goroutine of its own lets it go here.
	if err := a.Plugins().Close(); err != nil {
		a.Logger.Printf("Something did not close cleanly: %v", err)
	}

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

// RestartServers brings everything serving this agent back up, with whatever
// changed under it: a reissued certificate, a rotated secret, a port that
// moved. The reader keeps running throughout — it is not what is being
// restarted.
//
// The agent does not know what serves it, so this is every plugin, in the
// reverse of the order they were registered and then forward again. A plugin
// with nothing to rebind is stopped and started around a pause, which is the
// price of not naming the one that had.
func (a *Agent) RestartServers() error {
	a.serversMu.Lock()
	defer a.serversMu.Unlock()

	if !a.Plugins().Running() {
		// Nothing is serving, so there is nothing to bring back. Starting it
		// here would serve an agent the operator has stopped.
		a.Logger.Println("Nothing is serving, so there is nothing to restart")
		return nil
	}

	a.Logger.Println("Restarting servers...")

	if err := a.Plugins().Stop(); err != nil {
		a.Logger.Printf("Something did not stop cleanly: %v", err)
	}

	// Brief pause to allow ports to be released
	time.Sleep(100 * time.Millisecond)

	if err := a.Plugins().Start(); err != nil {
		return err
	}

	a.Logger.Println("Servers restarted successfully")
	a.PublishState()

	// Notify listeners of server restart
	select {
	case a.serverRestartChan <- struct{}{}:
	default:
		// Channel full, skip
	}

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
	a.settingsMu.Unlock()

	// Told to whatever is admitting devices, so the policy takes effect on the
	// connections already open rather than on the next restart.
	if admits, ok := plugin.Find[deviceAdmitter](a.Plugins()); ok {
		admits.SetRequirePairedDevice(on)
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

// ServingPort is the port being served on. It matches the configured port
// until one is saved, and again once the listener has been rebound.
//
// The agent asks whatever is serving it rather than holding a listener of its
// own, so a build that serves nothing reports what it would be served on.
func (a *Agent) ServingPort() int {
	if serving, ok := plugin.Find[serverPlugin](a.Plugins()); ok {
		if port := serving.Port(); port > 0 {
			return port
		}
	}
	return a.ConfiguredPort()
}

// Serving reports whether anything is answering on that port.
func (a *Agent) Serving() bool {
	serving, ok := plugin.Find[serverPlugin](a.Plugins())
	return ok && serving.Serving()
}

// LastCard is the tag last seen by whatever is serving clients, nil when there
// is none, or when nothing is serving.
func (a *Agent) LastCard() *nfc.Card {
	serving, ok := plugin.Find[cardReporter](a.Plugins())
	if !ok {
		return nil
	}
	return serving.LastCard()
}

// ConfiguredPort is the port the agent is set to serve on, which it binds the
// next time its listener comes up.
func (a *Agent) ConfiguredPort() int {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.DevicePort
}

// The capabilities the agent looks for among its plugins. It names what it
// needs done rather than which plugin does it, so the plugin that serves this
// agent can be replaced, or left out, without agent.go knowing.
type (
	// serverPlugin is whatever is answering on the agent's port.
	serverPlugin interface {
		Serving() bool
		Port() int
	}

	// deviceAdmitter is whatever decides which devices are let in.
	deviceAdmitter interface {
		SetRequirePairedDevice(on bool)
	}

	// cardReporter is whatever sees the tags as they are read.
	cardReporter interface {
		LastCard() *nfc.Card
	}
)

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
