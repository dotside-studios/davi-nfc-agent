package tray

import (
	"log"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/buildinfo"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
	"github.com/dotside-studios/davi-nfc-agent/traymenu/fynetray"
)

// readerSlotCount bounds the NFC readers offered in the Device submenu, which
// reuses a fixed pool of items rather than rebuilding them per refresh; see
// [traymenu.NewList].
const readerSlotCount = 12

// App manages the system tray interface for the NFC agent
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
}

// New creates the tray on the real system tray.
func New(rt *agent.Runtime) *App {
	return newApp(rt, fynetray.New())
}

// newApp builds the tray on a given menu driver, so a test can drive the menu
// without a desktop.
func newApp(rt *agent.Runtime, driver traymenu.Driver) *App {
	return &App{
		agent:         rt.Agent,
		initialDevice: rt.DevicePath,
		menu:          traymenu.New(driver),
	}
}

// SyncPreferencesToMenu reflects a change made elsewhere.
func (s *App) SyncPreferencesToMenu(next agent.Preferences) {
	if s.modes == nil {
		return
	}

	s.modes.Set(next.Mode)
	s.mModeMenu.SetTitle("Mode: " + modeName(next.Mode))

	s.cardTypes.Set(next.CardTypes)

	if s.mRequirePaired != nil {
		s.mRequirePaired.SetChecked(next.RequirePairedDevice)
	}
	if s.mReaderFeedback != nil {
		s.mReaderFeedback.SetChecked(next.ReaderFeedback)
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
	s.subscribe()
	s.autoStartAgent()
}

// onExit is called when the systray is exiting
func (s *App) onExit() {
	s.agent.Shutdown()
}

// setupUI declares the whole menu, top to bottom. Each item carries the handler
// it triggers.
func (s *App) setupUI() {
	s.menu.SetIcon(iconData)
	s.menu.SetTooltip(buildinfo.DisplayName)

	s.mStatus = s.menu.Add("Starting...", traymenu.Tooltip("Agent Status"), traymenu.Disabled())

	// The plugins add theirs here, under the status and above what this build
	// declares itself: the addresses a listener serves on are what an operator
	// opens this menu for, and they are a plugin's now.
	//
	// Done from inside the menu rather than after it, because a menu item
	// always goes to the end of its parent: activated once Quit was on, every
	// plugin entry would land under it.
	s.activatePlugins()

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

	// The menus open on what the agent is set to, which is not always the
	// default: what the launcher set was decided before the tray existed.
	s.SyncPreferencesToMenu(s.agent.Preferences())

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
		traymenu.OnClick(s.StopAgent),
	)

	s.menu.AddSeparator()
	s.menu.Add("Quit", traymenu.Tooltip("Quit the application"), traymenu.OnClick(s.menu.Quit))
}

// setupDeviceMenu builds the reader picker.
func (s *App) setupDeviceMenu() {
	devices := s.menu.AddSubmenu("Device", traymenu.Tooltip("Select NFC Device"))
	devices.Add("Refresh Devices", traymenu.Tooltip("Refresh device list"), traymenu.OnClick(s.updateDeviceList))
	devices.AddCheckbox("Auto-detect", true, traymenu.Tooltip("Auto-detect device"))

	s.readers = traymenu.NewList[string](devices, readerSlotCount, traymenu.Checkbox(false))
	s.readers.OnActivate(func(row traymenu.Row[string]) {
		if s.agent.CurrentDevicePath() != row.Value {
			s.SwitchDevice(row.Value)
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
// filter, which the All Types entry stands for.
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

// activatePlugins wires the agent's plugins in, handing them the tray's menu
// for their entries. It runs while the menu is being declared, and before the
// agent is started, which a plugin's registrations have to precede.
//
// A failure is logged and left there: the same one comes back from Start, where
// the tray already shows a start that did not happen.
func (s *App) activatePlugins() {
	if err := s.agent.Activate(s.menu); err != nil {
		log.Printf("[systray] %v", err)
	}
}

// autoStartAgent starts the agent automatically
func (s *App) autoStartAgent() {
	go func() {
		// Start with initial device (may be empty for auto-discovery)
		if err := s.agent.Start(s.initialDevice); err != nil {
			s.showStopped("Failed to Start")
		}
		s.updateDeviceList()
	}()
}

// handleStartAgent starts the agent
func (s *App) handleStartAgent() {
	// Use agent's stored device path (or empty for auto-discovery)
	devicePath := s.agent.CurrentDevicePath()
	if err := s.agent.Start(devicePath); err != nil {
		s.showStopped("Failed to Start")
		return
	}

	s.updateDeviceList() // Refresh to show current device
}

// StopAgent stops the agent
func (s *App) StopAgent() { s.agent.Stop() }

// showRunning puts the menu into the state of a running agent: addresses that
// mean something, and Stop as the control that can be clicked.
func (s *App) showRunning() {
	s.updateStatus("Running")
	s.mStart.Disable()
	s.mStop.Enable()
}

// showStopped puts the menu into the state of an agent that is not running,
// with the status line saying why.
func (s *App) showStopped(status string) {
	s.updateStatus(status)
	s.mStart.Enable()
	s.mStop.Disable()
}

// handleModeSwitch applies a mode picked from the menu. The mode belongs to the
// agent rather than to the running reader, so it can be picked with the agent
// stopped, and the console sees it because the console reads the agent.
//
// Session-only, like the card-type filter beside it: the tray changes what the
// agent is doing now, the console changes what it does from now on.
func (s *App) handleModeSwitch(mode nfc.ReaderMode) {
	s.agent.SetReaderMode(mode)

	// From the agent, not from the click: a mode the launcher holds leaves the
	// tick where it was rather than showing a mode the reader is not in.
	s.SyncPreferencesToMenu(s.agent.Preferences())
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
	s.SyncPreferencesToMenu(s.agent.Preferences())
}

// SwitchDevice switches to a different NFC device
func (s *App) SwitchDevice(deviceName string) {
	// Restart agent with new device
	s.agent.Stop()
	if err := s.agent.Start(deviceName); err != nil {
		s.showStopped("Failed to Start")
	}

	s.markCurrentReader()
}

// markCurrentReader moves the checkmark to the reader the agent is actually on.
func (s *App) markCurrentReader() {
	current := s.agent.CurrentDevicePath()

	rows := s.readers.Rows()
	for i := range rows {
		rows[i].Checked = rows[i].Value == current
	}
	s.readers.Set(rows)
}

// updateDeviceList refreshes the list of available devices
func (s *App) updateDeviceList() { s.applyReaders(s.agent.Readers()) }

// applyReaders redraws the reader picker from the readers the agent reports.
func (s *App) applyReaders(devices []string) {
	// Get current device from agent (source of truth)
	currentDevice := s.agent.CurrentDevicePath()

	// If agent is running but no device selected, auto-select first available
	if s.agent.Reader() != nil && currentDevice == "" && len(devices) > 0 {
		log.Printf("[systray] Auto-selecting discovered device: %s", devices[0])
		s.SwitchDevice(devices[0])
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
