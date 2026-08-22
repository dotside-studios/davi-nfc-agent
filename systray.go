package main

import (
	"log"
	"sync"

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

// endpointSlotCount bounds the addresses shown under Server URLs. The pool is
// fixed and reused, as everywhere else the contents change at runtime: a row
// added on a refresh would land under the API secret entries below it.
const endpointSlotCount = 8

// SystrayApp manages the system tray interface for the NFC agent
type SystrayApp struct {
	agent         *Agent
	initialDevice string
	console       *Console // nil if the control center is not built in

	// menu is the tray itself. Items declare their own click handlers as they
	// are added, so there is no central event loop to keep in step with them.
	menu *traymenu.Menu

	// Status section
	mStatus   *traymenu.Item
	mCardUID  *traymenu.Item
	mCardType *traymenu.Item
	mStart    *traymenu.Item
	mStop     *traymenu.Item

	// Addresses. The rows come from the plugins' endpoint register rather than
	// from the tray, so a feature that serves something appears here without
	// the tray knowing what it is.
	endpoints  *traymenu.List[plugin.Endpoint]
	mAPISecret *traymenu.Item

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

	// The top-level menus held open for plugins, and how many have been taken.
	// The tray hands them out as [plugin.Menus]; see systray_plugins.go.
	pluginMu     sync.Mutex
	pluginSlots  []*traymenu.Item
	pluginsTaken int
}

// NewSystrayApp creates a new systray application on the real tray.
//
// The tray knows about the agent and nothing else. Pairing, and anything else a
// build adds, reach the menu as plugins through AttachPlugins.
func NewSystrayApp(agent *Agent, initialDevice string) *SystrayApp {
	return newSystrayApp(agent, initialDevice, traymenu.Fyne())
}

// newSystrayApp builds the tray on a given menu driver, so a test can drive the
// menu without a desktop.
func newSystrayApp(agent *Agent, initialDevice string, driver traymenu.Driver) *SystrayApp {
	return &SystrayApp{
		agent:         agent,
		initialDevice: initialDevice,
		menu:          traymenu.New(driver),
	}
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
func (s *SystrayApp) persist() {
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

// syncSettingsToMenu reflects a settings change made elsewhere.
func (s *SystrayApp) syncSettingsToMenu(next settings.Settings) {
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
func (s *SystrayApp) disableHeldMenus() {
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
		for _, cardType := range GetAllCardTypeFilterNames() {
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
func (s *SystrayApp) Quit() { s.menu.Quit() }

// Run starts the systray application
func (s *SystrayApp) Run() {
	s.menu.Run(s.onReady, s.onExit)
}

// onReady is called when the systray is ready
func (s *SystrayApp) onReady() {
	s.setupUI()
	s.autoStartAgent()
	s.startServerRestartListener()
	s.startOriginWatcher()
	s.startDeviceWatcher()
}

// onExit is called when the systray is exiting
func (s *SystrayApp) onExit() {
	// Which stops the plugins and closes them, in the reverse of the order they
	// were registered.
	s.agent.Shutdown()
}

// setupUI declares the whole menu, top to bottom. Each item carries the handler
// it triggers.
func (s *SystrayApp) setupUI() {
	s.menu.SetIcon(iconData)
	s.menu.SetTooltip(buildinfo.DisplayName)

	s.mStatus = s.menu.Add("Starting...", traymenu.Tooltip("Agent Status"), traymenu.Disabled())

	s.setupURLsMenu()

	s.menu.AddSeparator()

	s.mCardUID = s.menu.Add("Card UID: None", traymenu.Tooltip("Current card UID"), traymenu.Disabled())
	s.mCardType = s.menu.Add("Card Type: None", traymenu.Tooltip("Current card type"), traymenu.Disabled())
	s.watchCard()

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

	// Where a plugin's own menu goes: beside the agent's features rather than
	// under Quit, which is where anything added later would land.
	s.reservePluginSlots()
	s.agent.Plugins().SetMenus(s)

	s.menu.AddSeparator()

	s.setupConsoleMenu()

	// The menus open on what the agent is set to, which is not always the
	// default: a mode restored from settings, or one the launcher set, was
	// decided before the tray existed. The ones the launcher holds are shown
	// and not offered.
	s.syncSettingsToMenu(s.agent.Settings())
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

// setupURLsMenu builds the submenu of addresses.
//
// What is in it comes from the agent's endpoint register, not from here. The
// servers publish theirs as they start, the pairing plugin publishes its page,
// and a consumer's plugin serving something of its own is listed beside them
// without this function being told about it. Clicking a row copies it, which is
// what the separate copy entries here used to be for.
func (s *SystrayApp) setupURLsMenu() {
	urls := s.menu.AddSubmenu("Server URLs", traymenu.Tooltip("Server addresses"))

	s.endpoints = traymenu.NewList[plugin.Endpoint](urls, endpointSlotCount)
	s.endpoints.OnActivate(func(row traymenu.Row[plugin.Endpoint]) {
		copyValue(row.Value.Label+" URL", row.Value.URL)
	})

	// API secret entries, only shown if a secret is configured. The secret is
	// not an address, but it is the other half of what a device needs to be let
	// in, and this is where an operator goes to hand a device its details.
	noSecret := s.agent.APISecret == ""
	s.mAPISecret = urls.Add("API Secret: hidden",
		traymenu.Tooltip("Required from non-loopback phones/clients"),
		traymenu.Disabled(),
		traymenu.HiddenIf(noSecret),
	)
	urls.Add("  Copy API Secret",
		traymenu.Tooltip("Copy the agent's API secret to clipboard"),
		traymenu.HiddenIf(noSecret),
		traymenu.OnClick(func() { copyValue("API secret", s.agent.APISecret) }),
	)
	urls.Add("  Regenerate API Secret",
		traymenu.Tooltip("Generate a fresh secret; all phones must re-handshake"),
		traymenu.HiddenIf(noSecret),
		traymenu.OnClick(s.handleRotateAPISecret),
	)

	// Redrawn whenever something is published or withdrawn, so an address that
	// changes with a restart, or a PIN rotation, does not wait for a click.
	s.agent.Plugins().Endpoints().OnChange(func([]plugin.Endpoint) { s.refreshURLsMenu() })
	s.refreshURLsMenu()
}

// refreshURLsMenu redraws the addresses from the register.
func (s *SystrayApp) refreshURLsMenu() {
	if s.endpoints == nil {
		return
	}

	list := s.agent.Plugins().Endpoints().List()
	rows := make([]traymenu.Row[plugin.Endpoint], 0, len(list))
	for _, endpoint := range list {
		row := traymenu.Row[plugin.Endpoint]{
			Value:   endpoint,
			Title:   endpoint.Label + ": " + endpoint.URL,
			Tooltip: endpoint.Tooltip,
		}
		if row.Tooltip == "" {
			// A row is its own copy entry, so an address that said nothing
			// about itself still says what clicking it does.
			row.Tooltip = "Click to copy"
		}
		if !endpoint.Running() {
			// The label stays: a server that is down is worth reading as down,
			// and it keeps its place for when it comes back.
			row.Title = endpoint.Label + ": Not running"
			row.Tooltip = "Nothing is serving this address"
		}
		rows = append(rows, row)
	}

	if dropped := s.endpoints.Set(rows); dropped > 0 {
		log.Printf("[systray] %d more addresses are published than the menu can show", dropped)
	}
}

// setupDeviceMenu builds the reader picker.
func (s *SystrayApp) setupDeviceMenu() {
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
func (s *SystrayApp) setupModeMenu() {
	s.mModeMenu = s.menu.AddSubmenu("Mode: Read/Write", traymenu.Tooltip("Change operation mode"))

	s.modes = traymenu.NewRadio[nfc.ReaderMode](s.mModeMenu)
	s.modes.Add(nfc.ModeReadWrite, "Read/Write Mode", traymenu.Tooltip("Allow both read and write"))
	s.modes.Add(nfc.ModeReadOnly, "Read Only Mode", traymenu.Tooltip("Only allow reading"))
	s.modes.Add(nfc.ModeWriteOnly, "Write Only Mode", traymenu.Tooltip("Only allow writing"))

	s.modes.OnSelect(s.handleModeSwitch)
}

// setupCardFilterMenu builds the card type filter. Nothing ticked means no
// filter, which is what the All Types entry stands for.
func (s *SystrayApp) setupCardFilterMenu() {
	filters := s.menu.AddSubmenu("Card Type Filter", traymenu.Tooltip("Filter cards by type"))

	s.cardTypes = traymenu.NewChecklist[string](filters)
	s.cardTypes.AddAll("All Types", traymenu.Tooltip("Allow all card types"))
	for _, cardType := range GetAllCardTypeFilterNames() {
		s.cardTypes.Add(cardType,
			GetCardTypeFilterDisplayName(cardType),
			traymenu.Tooltip(GetCardTypeFilterTooltip(cardType)),
		)
	}

	s.cardTypes.OnChange(s.applyCardTypes)
}

// autoStartAgent starts the agent automatically
func (s *SystrayApp) autoStartAgent() {
	// Set up device change listener
	s.setupDeviceChangeListener()

	go func() {
		// Start with initial device (may be empty for auto-discovery)
		if err := s.agent.Start(s.initialDevice); err == nil {
			s.showRunning()
		} else {
			s.showStopped("Failed to Start")
		}
		s.updateDeviceList()
	}()
}

// setupDeviceChangeListener sets up automatic device list refresh on device changes
func (s *SystrayApp) setupDeviceChangeListener() {
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

// watchCard keeps the card labels on the agent's state.
//
// The tray used to poll for this itself. The agent does the looking now, once
// for everything watching, so a plugin sees a card arrive whether or not there
// is a tray in this build.
func (s *SystrayApp) watchCard() {
	s.agent.Plugins().Watch(func(state plugin.State) {
		s.updateCardUID(state.Card.UID)
		s.updateCardType(state.Card.Type)
	})
}

// startServerRestartListener listens for server restart events from the Agent
// and brings the menu back in step with what the listeners are now serving.
func (s *SystrayApp) startServerRestartListener() {
	go func() {
		for range s.agent.ServerRestarts() {
			log.Printf("[systray] Server restart detected, updating the menu")
			s.updateURLs()

			// CAInstalled is a look at the filesystem, not a decision taken
			// once: a config directory that loses its CA needs the offer to
			// install one back, without an agent restart to notice.
			s.refreshTrustMenu()
		}
	}()
}

// handleStartAgent starts the agent
func (s *SystrayApp) handleStartAgent() {
	// Use agent's stored device path (or empty for auto-discovery)
	devicePath := s.agent.CurrentDevicePath()
	if err := s.agent.Start(devicePath); err != nil {
		s.showStopped("Failed to Start")
		return
	}

	s.showRunning()
	s.updateDeviceList() // Refresh to show current device
}

// handleStopAgent stops the agent
func (s *SystrayApp) handleStopAgent() {
	s.agent.Stop()
	s.showStopped("Stopped")
}

// showRunning puts the menu into the state of a running agent: addresses that
// mean something, and Stop as the control that can be clicked.
func (s *SystrayApp) showRunning() {
	s.updateStatus("Running")
	s.updateURLs()
	s.mStart.Disable()
	s.mStop.Enable()
}

// showStopped puts the menu into the state of an agent that is not running,
// with the status line saying why.
func (s *SystrayApp) showStopped(status string) {
	s.updateStatus(status)
	s.clearURLs()
	s.mStart.Enable()
	s.mStop.Disable()
}

// handleRotateAPISecret issues a fresh API secret; every phone must handshake
// again with it.
func (s *SystrayApp) handleRotateAPISecret() {
	fresh, err := s.agent.RotateAPISecret()
	if err != nil {
		log.Printf("[systray] Failed to rotate API secret: %v", err)
		return
	}

	log.Printf("[systray] API secret rotated; servers restarted")
	s.updateAPISecretLabel(fresh)
}

// handleModeSwitch applies a mode picked from the menu. The mode belongs to the
// agent rather than to the running reader, so it can be picked with the agent
// stopped, and the console sees it because the console reads the agent.
//
// Session-only, like the card-type filter beside it: the tray changes what the
// agent is doing now, the console changes what it does from now on.
func (s *SystrayApp) handleModeSwitch(mode nfc.ReaderMode) {
	s.agent.SetReaderMode(mode)
	s.persist()

	// From the agent, not from the click: a mode the launcher holds leaves the
	// tick where it was rather than showing a mode the reader is not in.
	s.syncSettingsToMenu(s.agent.Settings())
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
func (s *SystrayApp) applyCardTypes(types []string) {
	s.agent.SetCardTypeFilter(types)
	s.persist()
	s.syncSettingsToMenu(s.agent.Settings())
}

// switchDevice switches to a different NFC device
func (s *SystrayApp) switchDevice(deviceName string) {
	// Restart agent with new device
	s.agent.Stop()
	if err := s.agent.Start(deviceName); err == nil {
		s.showRunning()
	} else {
		s.showStopped("Failed to Start")
	}

	s.markCurrentReader()
}

// markCurrentReader moves the checkmark to the reader the agent is actually on.
func (s *SystrayApp) markCurrentReader() {
	current := s.agent.CurrentDevicePath()

	rows := s.readers.Rows()
	for i := range rows {
		rows[i].Checked = rows[i].Value == current
	}
	s.readers.Set(rows)
}

// updateDeviceList refreshes the list of available devices
func (s *SystrayApp) updateDeviceList() {
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
func (s *SystrayApp) updateStatus(status string) {
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
func (s *SystrayApp) updateCardUID(uid string) {
	if uid == "" {
		s.mCardUID.SetTitle("Card UID: None")
	} else {
		s.mCardUID.SetTitle("Card UID: " + uid)
	}
}

// updateCardType updates the card type display
func (s *SystrayApp) updateCardType(cardType string) {
	if cardType == "" {
		s.mCardType.SetTitle("Card Type: None")
	} else {
		s.mCardType.SetTitle("Card Type: " + cardType)
	}
}

// updateURLs brings the addresses and the API secret label back in step with
// what the agent is serving.
func (s *SystrayApp) updateURLs() {
	s.refreshURLsMenu()
	s.updateAPISecretLabel(s.agent.APISecret)
}

// updateAPISecretLabel updates the systray label with a redacted view
// of the API secret. The full secret is available via Copy.
func (s *SystrayApp) updateAPISecretLabel(secret string) {
	if secret == "" {
		s.mAPISecret.SetTitle("API Secret: not set")
		return
	}
	// Show first/last 4 chars only — operators can confirm the secret
	// changed after rotation without leaking it on the screen.
	preview := secret
	if len(secret) > 12 {
		preview = secret[:4] + "…" + secret[len(secret)-4:]
	}
	s.mAPISecret.SetTitle("API Secret: " + preview)
}

// clearURLs redraws the addresses of an agent that has stopped. The servers
// withdrew theirs on the way down, so this is the redraw rather than the
// change: what is published is the servers' business, not the tray's.
func (s *SystrayApp) clearURLs() {
	s.refreshURLsMenu()
}
