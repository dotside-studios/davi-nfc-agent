package tray

import (
	"log"
	"sync"
	"sync/atomic"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/buildinfo"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/plugin"
	"github.com/dotside-studios/davi-nfc-agent/settings"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// readerSlotCount bounds the NFC readers offered in the Device submenu, which
// reuses a fixed pool of items rather than rebuilding them per refresh; see
// [traymenu.NewList].
const readerSlotCount = 12

// App is the agent's tray: its menus, and the plugins' menus beside them.
type App struct {
	agent         *agent.Agent
	initialDevice string

	// menu is the tray itself. Items declare their own click handlers as they
	// are added, so there is no central event loop to keep in step with them.
	menu *traymenu.Menu

	// Status section
	mStatus   *traymenu.Item
	mCardUID  *traymenu.Item
	mCardType *traymenu.Item
	mStart    *traymenu.Item
	mStop     *traymenu.Item

	// Reader selection
	readers *traymenu.List[string]

	// Mode menu
	mModeMenu *traymenu.Item
	modes     *traymenu.Radio[nfc.ReaderMode]

	// Paired device menu items
	mDevicesMenu      *traymenu.Item
	mRevokeAllDevices *traymenu.Item
	mRequirePaired    *traymenu.Item
	pairedDevices     *traymenu.List[string]

	// Origin allowlist menu items
	mOriginAllowAny *traymenu.Item
	origins         *traymenu.List[originRow]

	// Reader feedback toggle
	mReaderFeedback *traymenu.Item

	// Card type filter
	cardTypes *traymenu.Checklist[string]

	// Certificate trust
	mTrustBrowsers *traymenu.Item

	// settings is the store the tray writes its toggles back to. Nil when the
	// agent has no config directory to persist to.
	settings *settings.Store

	// starting is held until the agent's first start attempt has finished, so
	// the menu says Starting... rather than flickering through Stopped on the
	// way up.
	starting atomic.Bool

	// The top-level menus held open for plugins, and how many have been taken.
	// The tray hands them out as [plugin.Menus]; see systray_plugins.go.
	pluginMu     sync.Mutex
	pluginSlots  []*traymenu.Item
	pluginsTaken int
}

// New returns the tray for an agent, drawn on the real desktop.
//
// The tray knows about the agent and nothing else. The servers, pairing, the
// control center and whatever a build adds reach the menu as plugins: the tray
// holds places open for them and draws none of them.
//
// initialDevice is the reader to open, empty to take whatever is found.
func New(agent *agent.Agent, initialDevice string) *App {
	return newApp(agent, initialDevice, traymenu.Fyne())
}

// newApp builds the tray on a given menu driver, so a test can drive the
// menu without a desktop.
func newApp(agent *agent.Agent, initialDevice string, driver traymenu.Driver) *App {
	app := &App{
		agent:         agent,
		initialDevice: initialDevice,
		menu:          traymenu.New(driver),
	}
	app.starting.Store(true)
	return app
}

// persist writes what the agent holds to the settings file.
//
// The tray and the console write the same file: a preference set from a menu is
// the same operator's decision as one set in a browser, and forgetting it at
// exit was an accident of which surface it was clicked in. Without a store the
// menus still work, they are just forgotten.
//
// What is written is the agent's state rather than what was clicked, so a
// change the agent refused is not recorded as if it had happened.
func (s *App) persist() {
	if s.settings == nil {
		return
	}

	inForce, explicit := s.agent.Settings(), s.agent.Explicit()
	_, err := s.settings.Update(func(next *settings.Settings) {
		prev := *next
		*next = inForce
		explicit.Keep(next, prev)
	})
	if err != nil {
		log.Printf("[systray] The change is in effect for this session only: it could not be saved: %v", err)
	}
}

// SyncSettings reflects a settings change made elsewhere, which is what the
// command line calls when the store is written from either surface.
func (s *App) SyncSettings(next settings.Settings) {
	if s.modes == nil {
		return
	}

	mode := settings.ParseMode(next.Mode)
	s.modes.Set(mode)
	s.mModeMenu.SetTitle("Mode: " + modeName(mode))

	s.cardTypes.Set(next.CardTypes)

	if s.mRequirePaired != nil {
		s.mRequirePaired.SetChecked(next.RequirePairedDevice)
	}
	if s.mReaderFeedback != nil {
		s.mReaderFeedback.SetChecked(next.ReaderFeedback)
	}
}

// disableHeldMenus greys out the menus for settings the launcher holds. They
// stay visible: what the agent is set to is worth reading even where it cannot
// be changed, and an item that vanishes reads as a missing feature.
func (s *App) disableHeldMenus() {
	explicit := s.agent.Explicit()
	const heldNote = " (set at launch)"

	if explicit.Mode && s.mModeMenu != nil {
		for _, mode := range []nfc.ReaderMode{nfc.ModeReadWrite, nfc.ModeReadOnly, nfc.ModeWriteOnly} {
			if item := s.modes.Item(mode); item != nil {
				item.Disable()
			}
		}
		s.mModeMenu.SetTooltip("The reader mode was set at launch and holds until the agent is restarted")
	}

	if explicit.CardTypes && s.cardTypes != nil {
		if all := s.cardTypes.All(); all != nil {
			all.Disable()
		}
		for _, cardType := range agent.GetAllCardTypeFilterNames() {
			if item := s.cardTypes.Item(cardType); item != nil {
				item.Disable()
			}
		}
	}

	if explicit.RequirePairedDevice && s.mRequirePaired != nil {
		s.mRequirePaired.Disable()
		s.mRequirePaired.SetTitle("Require Paired Devices" + heldNote)
	}

	if explicit.ReaderFeedback && s.mReaderFeedback != nil {
		s.mReaderFeedback.Disable()
		s.mReaderFeedback.SetTitle("Flash and Beep on Scan" + heldNote)
	}
}

// Quit tears the tray down, which stops the agent on the way out.
func (s *App) Quit() { s.menu.Quit() }

// Run starts the systray application
func (s *App) Run() {
	s.menu.Run(s.onReady, s.onExit)
}

// onReady is called when the systray is ready
func (s *App) onReady() {
	s.setupUI()
	s.autoStartAgent()
	s.startOriginWatcher()
	s.startDeviceWatcher()
}

// onExit is called when the systray is exiting
func (s *App) onExit() {
	// Which stops the plugins and closes them, in the reverse of the order they
	// were registered.
	s.agent.Shutdown()
}

// setupUI declares the whole menu, top to bottom. Each item carries the handler
// it triggers.
func (s *App) setupUI() {
	s.menu.SetIcon(iconData)
	s.menu.SetTooltip(buildinfo.DisplayName)

	s.mStatus = s.menu.Add("Starting...", traymenu.Tooltip("Agent Status"), traymenu.Disabled())

	// Then the menus of whatever serves this agent: the addresses a device
	// connects to, the pairing page, a consumer's own feature. The tray holds
	// the places open and draws none of them.
	s.reservePluginSlots()
	s.agent.Plugins().SetUI(s)

	s.menu.AddSeparator()

	s.mCardUID = s.menu.Add("Card UID: None", traymenu.Tooltip("Current card UID"), traymenu.Disabled())
	s.mCardType = s.menu.Add("Card Type: None", traymenu.Tooltip("Current card type"), traymenu.Disabled())
	s.menu.AddSeparator()

	s.setupDeviceMenu()

	s.menu.AddSeparator()

	s.setupModeMenu()
	s.setupFeedbackMenu()

	s.menu.AddSeparator()

	s.setupCardFilterMenu()

	s.menu.AddSeparator()

	s.setupDevicesMenu()
	s.setupOriginsMenu()

	// Certificate trust, the other half of what a browser needs.
	s.setupTrustMenu()

	s.menu.AddSeparator()

	// The menus open on what the agent is set to, which is not always the
	// default: a mode restored from settings, or one the launcher set, was
	// decided before the tray existed. The ones the launcher holds are shown
	// and not offered.
	s.SyncSettings(s.agent.Settings())
	s.disableHeldMenus()

	s.menu.AddSeparator()

	// Both start disabled: the agent is auto-starting, and Stop becomes
	// available once it has.
	s.mStart = s.menu.Add("Start Agent",
		traymenu.Tooltip("Start the NFC agent"),
		traymenu.Disabled(),
		traymenu.OnClick(s.handleStartAgent),
	)
	s.mStop = s.menu.Add("Stop Agent",
		traymenu.Tooltip("Stop the NFC agent"),
		traymenu.Disabled(),
		traymenu.OnClick(s.handleStopAgent),
	)

	s.menu.AddSeparator()
	s.menu.Add("Quit", traymenu.Tooltip("Quit the application"), traymenu.OnClick(s.menu.Quit))

	// Everything the menu shows of the agent now exists to be kept in step.
	s.watchAgent()

	// Last, so a plugin wires itself up against a menu that is already whole:
	// it may take a menu of its own, declare an address, or read the state from
	// the moment it is handed its context.
	//
	// The plugins are only wired up here, not started. They come up with the
	// agent, and go down with it.
	s.agent.PublishState()
	if err := s.agent.Plugins().Init(); err != nil {
		log.Printf("[systray] Not everything registered could be set up: %v", err)
	}
}

// setupDeviceMenu builds the reader picker.
func (s *App) setupDeviceMenu() {
	devices := s.menu.AddSubmenu("Device", traymenu.Tooltip("Select NFC Device"))
	devices.Add("Refresh Devices", traymenu.Tooltip("Refresh device list"), traymenu.OnClick(s.updateDeviceList))
	devices.AddCheckbox("Auto-detect", true, traymenu.Tooltip("Auto-detect device"))

	s.readers = traymenu.NewList[string](devices, readerSlotCount, traymenu.Checkbox(false))
	s.readers.OnActivate(func(row traymenu.Row[string]) {
		if s.agent.CurrentDevicePath() != row.Value {
			s.switchDevice(row.Value)
		}
	})
}

// setupModeMenu builds the reader mode picker.
func (s *App) setupModeMenu() {
	s.mModeMenu = s.menu.AddSubmenu("Mode: Read/Write", traymenu.Tooltip("Change operation mode"))

	s.modes = traymenu.NewRadio[nfc.ReaderMode](s.mModeMenu)
	s.modes.Add(nfc.ModeReadWrite, "Read/Write Mode", traymenu.Tooltip("Allow both read and write"))
	s.modes.Add(nfc.ModeReadOnly, "Read Only Mode", traymenu.Tooltip("Only allow reading"))
	s.modes.Add(nfc.ModeWriteOnly, "Write Only Mode", traymenu.Tooltip("Only allow writing"))

	s.modes.OnSelect(s.handleModeSwitch)
}

// setupCardFilterMenu builds the card type filter. Nothing ticked means no
// filter, which is what the All Types entry stands for.
func (s *App) setupCardFilterMenu() {
	filters := s.menu.AddSubmenu("Card Type Filter", traymenu.Tooltip("Filter cards by type"))

	s.cardTypes = traymenu.NewChecklist[string](filters)
	s.cardTypes.AddAll("All Types", traymenu.Tooltip("Allow all card types"))
	for _, cardType := range agent.GetAllCardTypeFilterNames() {
		s.cardTypes.Add(cardType,
			agent.GetCardTypeFilterDisplayName(cardType),
			traymenu.Tooltip(agent.GetCardTypeFilterTooltip(cardType)),
		)
	}

	s.cardTypes.OnChange(s.applyCardTypes)
}

// autoStartAgent starts the agent automatically
func (s *App) autoStartAgent() {
	// Set up device change listener
	s.setupDeviceChangeListener()

	go func() {
		// Start with initial device (may be empty for auto-discovery)
		err := s.agent.Start(s.initialDevice)
		s.starting.Store(false)

		if err != nil {
			s.updateStatus("Failed to Start")
			s.mStart.Enable()
		} else {
			s.agent.PublishState()
		}
		s.updateDeviceList()
	}()
}

// setupDeviceChangeListener sets up automatic device list refresh on device changes
func (s *App) setupDeviceChangeListener() {
	notifier, ok := s.agent.Manager.(nfc.DeviceChangeNotifier)
	if !ok {
		return
	}

	go func() {
		for range notifier.DeviceChanges() {
			log.Printf("[systray] Device change detected, refreshing device list")
			s.updateDeviceList()
		}
	}()
}

// watchAgent keeps the menu on the agent's state.
//
// The agent can be started, stopped or moved to another reader from here, from
// the console, or by a plugin, and the menu has to read the same either way. So
// it follows the state rather than each handler painting what it happens to
// know about.
func (s *App) watchAgent() {
	s.agent.Plugins().Watch(func(state plugin.State) {
		if s.starting.Load() {
			// Still coming up. What the controls say is Starting..., and the
			// agent has not finished deciding otherwise.
			s.updateReaderState(state)
			return
		}

		if state.Running {
			s.updateStatus("Running")
			s.mStart.Disable()
			s.mStop.Enable()
		} else {
			s.updateStatus("Stopped")
			s.mStart.Enable()
			s.mStop.Disable()
		}

		s.updateReaderState(state)
	})
}

// updateReaderState is the part of the menu that reads the same whether or not
// the agent has finished starting.
func (s *App) updateReaderState(state plugin.State) {
	s.markReader(state.Device)
	s.updateCardUID(state.Card.UID)
	s.updateCardType(state.Card.Type)

	// CAInstalled is a look at the filesystem, not a decision taken once: a
	// config directory that loses its CA needs the offer to install one back,
	// and one installed from the console leaves nothing to offer.
	s.refreshTrustMenu()
}

// handleStartAgent starts the agent
func (s *App) handleStartAgent() {
	// Use agent's stored device path (or empty for auto-discovery)
	devicePath := s.agent.CurrentDevicePath()
	if err := s.agent.Start(devicePath); err != nil {
		// The state says it is not running; this says why.
		s.updateStatus("Failed to Start")
		return
	}

	s.updateDeviceList() // Refresh to show current device
}

// handleStopAgent stops the agent. What the menu then reads comes from the
// state, which is also how it reads a stop from the console.
func (s *App) handleStopAgent() { s.agent.Stop() }

// handleModeSwitch applies a mode picked from the menu. The mode belongs to the
// agent rather than to the running reader, so it can be picked with the agent
// stopped, and the console sees it because the console reads the agent.
//
// Session-only, like the card-type filter beside it: the tray changes what the
// agent is doing now, the console changes what it does from now on.
func (s *App) handleModeSwitch(mode nfc.ReaderMode) {
	s.agent.SetReaderMode(mode)
	s.persist()

	// From the agent, not from the click: a mode the launcher holds leaves the
	// tick where it was rather than showing a mode the reader is not in.
	s.SyncSettings(s.agent.Settings())
	log.Printf("Reader mode is now %s", modeName(s.agent.CurrentReaderMode()))
}

// modeName is the label a reader mode goes by in the menu.
func modeName(mode nfc.ReaderMode) string {
	switch mode {
	case nfc.ModeReadOnly:
		return "Read Only"
	case nfc.ModeWriteOnly:
		return "Write Only"
	default:
		return "Read/Write"
	}
}

// applyCardTypes narrows the agent to the ticked card types, or opens it back
// up when none of them are.
func (s *App) applyCardTypes(types []string) {
	s.agent.SetCardTypeFilter(types)
	s.persist()
	s.SyncSettings(s.agent.Settings())
}

// switchDevice moves the agent to another reader. The menu follows the state
// this puts the agent in, as it does for a reader picked in the console.
func (s *App) switchDevice(deviceName string) {
	if err := s.agent.SelectDevice(deviceName); err != nil {
		s.updateStatus("Failed to Start")
	}
}

// markReader moves the checkmark to the reader the agent is on.
func (s *App) markReader(current string) {
	rows := s.readers.Rows()
	changed := false
	for i := range rows {
		if checked := rows[i].Value == current; checked != rows[i].Checked {
			rows[i].Checked = checked
			changed = true
		}
	}
	if changed {
		s.readers.Set(rows)
	}
}

// updateDeviceList refreshes the list of available devices
func (s *App) updateDeviceList() {
	devices, err := s.agent.Manager.ListDevices()
	if err != nil {
		log.Printf("Error listing devices: %v", err)
		return
	}

	// Get current device from agent (source of truth)
	currentDevice := s.agent.CurrentDevicePath()

	// If agent is running but no device selected, auto-select first available
	if s.agent.Reader != nil && currentDevice == "" && len(devices) > 0 {
		log.Printf("[systray] Auto-selecting discovered device: %s", devices[0])
		s.switchDevice(devices[0])
		currentDevice = s.agent.CurrentDevicePath()
	}

	rows := make([]traymenu.Row[string], 0, len(devices))
	for _, device := range devices {
		rows = append(rows, traymenu.Row[string]{
			Value:   device,
			Title:   device,
			Tooltip: "Select this device",
			Checked: device == currentDevice,
		})
	}

	if dropped := s.readers.Set(rows); dropped > 0 {
		log.Printf("[systray] %d more readers are attached than the menu can show", dropped)
	}
}

// updateStatus updates the status menu item and icon
func (s *App) updateStatus(status string) {
	s.mStatus.SetTitle(status)

	// Update icon based on status
	switch status {
	case "Running":
		s.menu.SetIcon(iconDataConnected)
	case "Failed to Start":
		s.menu.SetIcon(iconDataError)
	case "Stopped":
		s.menu.SetIcon(iconDataStopped)
	default:
		// Starting or other states
		s.menu.SetIcon(iconData)
	}
}

// updateCardUID updates the card UID display
func (s *App) updateCardUID(uid string) {
	if uid == "" {
		s.mCardUID.SetTitle("Card UID: None")
	} else {
		s.mCardUID.SetTitle("Card UID: " + uid)
	}
}

// updateCardType updates the card type display
func (s *App) updateCardType(cardType string) {
	if cardType == "" {
		s.mCardType.SetTitle("Card Type: None")
	} else {
		s.mCardType.SetTitle("Card Type: " + cardType)
	}
}
