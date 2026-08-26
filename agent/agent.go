package agent

import (
	"errors"
	"io"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/buildinfo"
	"github.com/dotside-studios/davi-nfc-agent/event"
	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
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

// readerOperationTimeout bounds one tag operation: long enough for a write and
// its read-back on a slow tag, short enough that a card lifted mid-operation
// does not hold the reader.
const readerOperationTimeout = 5 * time.Second

// Config is the agent's settled configuration. New copies it in, and nothing
// afterwards can change it: the fields below are read through the accessors on
// Agent, so a caller holding a running agent cannot rebind its port or withdraw
// its pairing requirement behind the servers' backs. The few settings that may legitimately change while running have
// methods of their own: SetRequirePairedDevice, SetAllowCardType.
type Config struct {
	// Manager supplies the readers. Required; New panics without one, because
	// an agent with no way to enumerate a reader cannot be started later.
	Manager nfc.Manager

	// Info is what this build calls itself. Blank fields fall back to the
	// agent's own identity, so a program embedding it can rename just the
	// parts it cares about, and should at least set DirName, or its
	// configuration lands in this agent's directory.
	Info buildinfo.Info

	// Logger receives the agent's diagnostics, and is what the plugins log
	// through, each under its own name. Nil installs one writing to stderr, and
	// to Logs when there is one, with an [agent] prefix.
	//
	// A logger supplied here is used as it is: where it writes, and whether the
	// console can read it back, is then the caller's to arrange.
	Logger *log.Logger

	// DevicePort is the single listener serving both devices and clients.
	// Zero means DefaultDevicePort.
	DevicePort int

	// APISecret is the shared secret for the session handshake. Empty admits
	// unauthenticated connections, which is the development default.
	APISecret string

	// ConfigDir is where the API secret and other state persist.
	ConfigDir string

	// Devices holds the paired devices and their per-device credentials.
	Devices *DeviceRegistry

	// PublicKeyPin identifies this agent to devices across certificate
	// reissues, so they need no certificate authority to recognize it.
	PublicKeyPin string

	// Plugins are activated once, in order, before the agent first starts.
	// They are what mount the routes and register the components a build is
	// made of; see [Plugin]. More can be added through [Agent.Plugins], up
	// until the agent activates them.
	Plugins []Plugin

	Logs *logbuf.Ring

	// RequirePairedDevice admits only devices holding a paired credential,
	// withdrawing the shared secret and loopback bypass for device
	// connections. Browser clients are unaffected. Changeable at runtime
	// through SetRequirePairedDevice.
	RequirePairedDevice bool

	// ReaderFeedback has the reader flash its LED and sound its buzzer at what
	// it reads and writes. Changeable at runtime through SetReaderFeedback,
	// which also reaches the reader already running.
	ReaderFeedback bool

	// Mode is the access mode the reader runs in, CardTypes the types a scan
	// may carry, and DevicePath the reader to open, empty for auto-detect.
	//
	// Held on the agent as well as on the reader, because the reader is built
	// in Start: a preference that only reached the reader would be lost with
	// every reader the agent starts.
	Mode       nfc.ReaderMode
	CardTypes  []string
	DevicePath string
}

// Agent runs the NFC readers and reports what they see. Build one with New;
// its configuration is fixed from that point, and the exported fields below are
// the parts that come and go as it runs.
type Agent struct {
	// lastCard is the most recent scan the agent reported, kept here rather
	// than in whatever it was reported to: the readers a run opens are rebuilt
	// by every start, and the card on one is still there afterwards.
	lastCard atomic.Pointer[nfc.Card]

	// supervisor operates the readers, nil before Start and after Stop.
	// Atomic because the handlers read it from their own goroutines, and Stop
	// holds the lifecycle lock while the server waits for them to finish.
	supervisor atomic.Pointer[nfc.Supervisor]

	// Plugins is the plugin list, added to before the agent starts and
	// activated once, on the first Start or by a host that activates them
	// itself to give them a real tray menu.
	Plugins *PluginSet

	// trayMenu is the menu a plugin's entries go on when no host supplied one:
	// built on a driver that draws nothing, so a plugin need not ask whether
	// there is a tray. Nil until a plugin asks for a menu.
	trayMenu *traymenu.Menu

	// Settled at construction.
	info                buildinfo.Info
	logger              *log.Logger
	manager             nfc.Manager
	configDir           string
	devices             *DeviceRegistry
	devicePort          int
	publicKeyPin        string
	requirePairedDevice bool
	readerFeedback      bool
	logs                *logbuf.Ring
	suppliedLogger      bool

	// Preferences. Held on the agent as well as on the reader, because Start
	// builds a new reader each time: a preference that only reached the reader
	// would be lost with every restart.
	readerMode   nfc.ReaderMode
	pinnedDevice string

	// apiSecret is guarded rather than settled, because rotating it replaces
	// it while the agent runs. Whatever checks it reads it per request, so a
	// rotation reaches the endpoints without anything being rebuilt.
	apiSecret string

	// settingsMu guards the settings state above. The console changes it from
	// its own goroutines and reads it back for every snapshot it draws, and the
	// tray does the same from its dispatch goroutine. The card-type filter
	// guards itself.
	settingsMu sync.RWMutex

	// Mutable state. lifecycle carries the state machine, the hooks and the
	// registered components; every transition below runs under its lock.
	lifecycle
	cardTypes *cardTypeFilter

	// The agent's subscriptions to what the supervisor reports, held so a stop
	// ends them. Every scan arrives here, whether a reader was polled for it or
	// a device reported it.
	readerScans  *event.Connection
	readerStatus *event.Connection

	// events is the subscription surface, published by Events. Signals are
	// safe from any goroutine, so it needs no lock of its own.
	events Events

	// done ends what runs for the agent's lifetime rather than for a run.
	// Closed by Shutdown.
	done     chan struct{}
	doneOnce sync.Once

	// mounter is what plugins mount their routes on, published by whichever
	// plugin serves them. Nil is an agent serving no HTTP at all.
	mounter Mounter

	// devicePath is the reader Start resolved to, kept so a restart reopens the
	// same one. Atomic for the same reason as reader: the console and the tray
	// ask for it through CurrentDevicePath from their own goroutines while a
	// start is writing it.
	devicePath atomic.Pointer[string]
}

// New builds an agent from cfg. The configuration is copied, so later changes
// to cfg do not reach the agent.
func New(cfg Config) *Agent {
	if cfg.Manager == nil {
		panic("agent: Config.Manager is required")
	}

	logger := cfg.Logger
	suppliedLogger := logger != nil
	if logger == nil {
		logger = log.New(logSink(cfg.Logs, logbuf.LevelInfo), "[agent] ", log.LstdFlags)
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
		devices:             cfg.Devices,
		devicePort:          port,
		publicKeyPin:        cfg.PublicKeyPin,
		requirePairedDevice: cfg.RequirePairedDevice,
		readerMode:          cfg.Mode,
		pinnedDevice:        cfg.DevicePath,
		readerFeedback:      cfg.ReaderFeedback,
		cardTypes:           newCardTypeFilter(cfg.CardTypes),
		logs:                cfg.Logs,
		suppliedLogger:      suppliedLogger,
		done:                make(chan struct{}),
		Plugins:             &PluginSet{},
	}

	a.publishEvents()
	a.watchStores()
	a.watchManager()

	if err := a.Plugins.Add(cfg.Plugins...); err != nil {
		// Only a nil entry can fail here: the set is new, so nothing is sealed.
		logger.Printf("Ignoring a plugin: %v", err)
	}

	return a
}

// logSink is where a logger the agent builds for itself writes: the process's
// stderr, and the ring the console reads back, when the caller supplied one.
//
// Without this the agent's own diagnostics, and every plugin's, reach stderr
// alone, which a program started from a desktop launcher has nowhere to show.
func logSink(logs *logbuf.Ring, level logbuf.Level) io.Writer {
	if logs == nil {
		return os.Stderr
	}
	return io.MultiWriter(os.Stderr, logs.At(level))
}

// LoggerAt is [Agent.Logger] writing at level, for the lines the agent knows
// the severity of rather than leaving the console to read it off the text.
//
// It reports the agent's own logger when a caller supplied one through
// [Config.Logger]: what that logger does with a level is the caller's, and the
// agent does not wrap it to guess.
func (a *Agent) LoggerAt(level logbuf.Level) *log.Logger {
	if a.logs == nil || a.suppliedLogger {
		return a.logger
	}
	return log.New(logSink(a.logs, level), a.logger.Prefix(), a.logger.Flags())
}

// pluginLogger is the log channel a plugin writes on: the agent's own sink at
// level, under the plugin's name in place of the agent's prefix, which is what
// makes [logbuf.Entry.Source] tell them apart.
func (a *Agent) pluginLogger(name string, level logbuf.Level) *log.Logger {
	return log.New(a.LoggerAt(level).Writer(), "["+name+"] ", a.logger.Flags())
}

// Configuration readers. These exist because the tray and the console display
// what the agent was built with; none of them can change it.

// Info reports what this build calls itself.
func (a *Agent) Info() buildinfo.Info { return a.info }

func (a *Agent) Manager() nfc.Manager { return a.manager }

// Readers lists the readers that can be picked, which is what a reader picker
// offers. Phones are left out: pinning the reader to one pins it to a device
// that is never opened.
func (a *Agent) Readers() []string {
	readers, err := nfc.ListReaders(a.manager)
	if err != nil {
		a.logger.Printf("Listing readers failed: %v", err)
		return nil
	}
	return readers
}
func (a *Agent) Logger() *log.Logger      { return a.logger }
func (a *Agent) ConfigDir() string        { return a.configDir }
func (a *Agent) Devices() *DeviceRegistry { return a.devices }

// APISecret is the secret non-loopback connections must present. Read on every
// upgrade by whatever checks it, so it follows a rotation.
func (a *Agent) APISecret() string {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.apiSecret
}

// Logs is the ring the agent's log is captured in, or nil when the program
// installed none.
func (a *Agent) Logs() *logbuf.Ring { return a.logs }

// DevicePort is the port the agent is configured to serve on, which a saved
// preference can change. A listener keeps the port it was built with, so what
// is being served on is [ServerPlugin.Port].
func (a *Agent) DevicePort() int {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.devicePort
}
func (a *Agent) PublicKeyPin() string { return a.publicKeyPin }

func (a *Agent) RequirePairedDevice() bool {
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.requirePairedDevice
}

// Supervisor operates the agent's readers, nil before Start and after Stop.
// Safe to call from any goroutine, though the answer can go stale the moment it
// is returned: a caller acting on it should hold the value it read.
//
// A caller acting on a tag should ask the agent instead: it answers for every
// tag, whether a reader or a device is holding it, and outlives any one
// supervisor.
func (a *Agent) Supervisor() *nfc.Supervisor { return a.supervisor.Load() }

// ReaderFeedback reports whether the reader answers for its own work with its
// LED and buzzer.
func (a *Agent) ReaderFeedback() bool {
	if readers := a.supervisor.Load(); readers != nil {
		return readers.FeedbackEnabled()
	}
	a.settingsMu.RLock()
	defer a.settingsMu.RUnlock()
	return a.readerFeedback
}

// startLocked opens the readers. The caller holds the lifecycle lock and owns
// the state transition; see Start.
func (a *Agent) startLocked(devicePath string) error {
	a.devicePath.Store(&devicePath)

	// A start that names a device is a choice, so it is what the agent is set
	// to: the filter and the preferences agree afterwards rather than the
	// preference reporting one device while the scans come from another.
	//
	// Naming none is auto-detect, which stays auto-detect. It used to pin
	// whichever reader was listed first, so a second one was polled and then
	// filtered out of everything the clients saw.
	if devicePath != "" {
		a.SetPinnedDevice(devicePath)
	}

	readers, err := nfc.NewSupervisor(a.manager, readerOperationTimeout)
	if err != nil {
		a.logger.Printf("Error initializing the readers: %v", err)
		return err
	}

	a.supervisor.Store(readers)
	a.adoptReaderSettings()

	// The agent reports what its readers scan, and what serves clients
	// subscribes to that rather than being fed by the agent.
	a.readerScans = readers.Scans().Connect(a.forwardScan)
	a.readerStatus = readers.Status().Connect(a.fireReaderStatus)

	// After them, so what the readers publish already has somewhere to go.
	if err := readers.Start(); err != nil {
		a.logger.Printf("Error starting the readers: %v", err)
		return err
	}
	return nil
}

// stopLocked closes the readers. The caller holds the lifecycle lock and owns
// the state transition; see Stop. It is safe to call on
// a partly started agent, so an aborted Start is recoverable.
func (a *Agent) stopLocked() {
	if a.supervisor.Load() == nil && a.readerScans == nil {
		return
	}

	a.logger.Println("Stopping agent...")

	a.readerScans.Disconnect()
	a.readerStatus.Disconnect()
	a.readerScans, a.readerStatus = nil, nil

	if readers := a.supervisor.Load(); readers != nil {
		readers.Stop()
		a.supervisor.Store(nil)
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
	a.doneOnce.Do(func() {
		if a.done != nil {
			close(a.done)
		}
	})

	if closer, ok := a.manager.(interface{ Close() }); ok {
		closer.Close()
	}

	// The menu a plugin's entries went on when there was no tray. A host with
	// a real tray closes its own; this is only the stand-in.
	a.lifecycleMu.Lock()
	menu := a.trayMenu
	a.trayMenu = nil
	a.lifecycleMu.Unlock()
	if menu != nil {
		menu.Close()
	}
}

// RotateAPISecret generates a fresh API secret, persists it under ConfigDir
// and returns it. It takes effect on the next connection to either endpoint,
// since both read the secret per request; connections already open are not
// dropped.
//
// On error the previous secret remains in effect.
func (a *Agent) RotateAPISecret() (string, error) {
	if a.configDir == "" {
		return "", errors.New("config dir not configured")
	}

	fresh, err := rotateAPISecret(a.configDir)
	if err != nil {
		return "", err
	}

	a.settingsMu.Lock()
	a.apiSecret = fresh
	a.settingsMu.Unlock()

	a.logger.Println("API secret rotated")
	return fresh, nil
}

func (a *Agent) SetAllowCardType(cardType string, allow bool) {
	if allow {
		a.AllowCardType(cardType)
	} else {
		a.DisallowCardType(cardType)
	}
}

// ClearCardTypeFilter drops the filter, so every card is accepted. That is not
// the same as naming each known type: a phone reports the tag types it
// recognizes, which need not be ones this agent enumerates, and listing the
// known types would refuse the rest.
func (a *Agent) ClearCardTypeFilter() {
	a.cardTypes.clear()
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

// IsCardTypeAllowed answers the question the scan path asks of the filter: an
// empty filter admits everything, including a type this agent does not know.
func (a *Agent) IsCardTypeAllowed(cardType string) bool {
	return a.cardTypes.isAllowed(cardType)
}

// CurrentDevicePath returns the current device path from the reader.
// Returns empty string if no reader is active.
func (a *Agent) CurrentDevicePath() string {
	if pinned := a.CurrentPinnedDevice(); pinned != "" {
		return pinned
	}
	if readers := a.supervisor.Load(); readers != nil {
		if devices := readers.Devices(); len(devices) > 0 {
			return devices[0]
		}
	}
	if stored := a.devicePath.Load(); stored != nil {
		return *stored // The path a previous start resolved to
	}
	return ""
}

// SetRequirePairedDevice changes the paired-device requirement. The device
// endpoint's check reads it per connection, so this takes effect immediately.
func (a *Agent) SetRequirePairedDevice(on bool) {
	a.ApplyPreferences(func(p *Preferences) { p.RequirePairedDevice = on })
}

// SetReaderFeedback turns the reader's LED and buzzer feedback on or off, on a
// running reader as well as on the next one the agent starts.
func (a *Agent) SetReaderFeedback(on bool) {
	a.ApplyPreferences(func(p *Preferences) { p.ReaderFeedback = on })
}

// TokenVerifier recognises the per-device credentials this agent issued at
// pairing, for whatever admits a connection presenting one. Nil on an agent
// built without a registry, which admits nobody on a credential.
//
// Take it from here rather than from Devices: a nil registry assigned to the
// interface is not a nil interface, and the caller's nil check would miss it.
func (a *Agent) TokenVerifier() server.TokenVerifier {
	if a.devices == nil {
		return nil
	}
	return a.devices
}
