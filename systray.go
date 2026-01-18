package main

import (
	"fmt"
	"log"
	"net"
	"os/exec"
	"runtime"
	"time"

	"fyne.io/systray"

	"github.com/dotside-studios/davi-nfc-agent/buildinfo"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// cardTypeFilterItem holds a menu item and its associated card type
type cardTypeFilterItem struct {
	menuItem *systray.MenuItem
	cardType string
}

// getLocalIPs returns a list of local IP addresses (excluding loopback)
func getLocalIPs() []string {
	var ips []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ips
	}

	for _, addr := range addrs {
		if ipNet, ok := addr.(*net.IPNet); ok && !ipNet.IP.IsLoopback() {
			if ipNet.IP.To4() != nil {
				ips = append(ips, ipNet.IP.String())
			}
		}
	}
	return ips
}

// SystrayApp manages the system tray interface for the NFC agent
type SystrayApp struct {
	agent         *Agent
	initialDevice string
	bootstrapPort int

	// Menu items
	mStatus     *systray.MenuItem
	mCardUID    *systray.MenuItem
	mCardType   *systray.MenuItem
	mStart      *systray.MenuItem
	mStop       *systray.MenuItem
	mDeviceMenu *systray.MenuItem

	// URL menu items
	mURLsMenu         *systray.MenuItem
	mDeviceURL        *systray.MenuItem
	mClientURL        *systray.MenuItem
	mBootstrapURL     *systray.MenuItem
	mCopyDeviceURL    *systray.MenuItem
	mCopyClientURL    *systray.MenuItem
	mCopyBootstrapURL *systray.MenuItem

	deviceMenuItems map[string]*systray.MenuItem

	// Mode menu items
	mModeMenu      *systray.MenuItem
	mReadWriteMode *systray.MenuItem
	mReadMode      *systray.MenuItem
	mWriteMode     *systray.MenuItem

	// Card filter menu items
	mCardFilterMenu *systray.MenuItem
	mFilterAll      *systray.MenuItem
	cardTypeFilters map[string]*cardTypeFilterItem // Maps card type to filter item
}

// NewSystrayApp creates a new systray application
func NewSystrayApp(agent *Agent, initialDevice string, bootstrapPort int) *SystrayApp {
	return &SystrayApp{
		agent:           agent,
		initialDevice:   initialDevice,
		bootstrapPort:   bootstrapPort,
		deviceMenuItems: make(map[string]*systray.MenuItem),
		cardTypeFilters: make(map[string]*cardTypeFilterItem),
	}
}

// Run starts the systray application
func (s *SystrayApp) Run() {
	systray.Run(s.onReady, s.onExit)
}

// onReady is called when the systray is ready
func (s *SystrayApp) onReady() {
	s.setupUI()
	s.autoStartAgent()
	s.startCardInfoUpdater()
	s.startServerRestartListener()
	s.startEventHandler()
}

// onExit is called when the systray is exiting
func (s *SystrayApp) onExit() {
	s.agent.Stop()
}

// setupUI initializes all menu items
func (s *SystrayApp) setupUI() {
	systray.SetIcon(iconData)
	systray.SetTooltip(buildinfo.DisplayName)

	// Status section
	s.mStatus = systray.AddMenuItem("Starting...", "Agent Status")
	s.mStatus.Disable()

	// URLs menu
	s.mURLsMenu = systray.AddMenuItem("Server URLs", "Server addresses")
	s.mDeviceURL = s.mURLsMenu.AddSubMenuItem("Device: Not running", "DeviceServer WebSocket URL")
	s.mDeviceURL.Disable()
	s.mCopyDeviceURL = s.mURLsMenu.AddSubMenuItem("  Copy Device URL", "Copy DeviceServer URL to clipboard")
	s.mClientURL = s.mURLsMenu.AddSubMenuItem("Client: Not running", "ClientServer URL")
	s.mClientURL.Disable()
	s.mCopyClientURL = s.mURLsMenu.AddSubMenuItem("  Copy Client URL", "Copy ClientServer URL to clipboard")
	s.mBootstrapURL = s.mURLsMenu.AddSubMenuItem("CA Cert: Not running", "CA Certificate download URL")
	s.mBootstrapURL.Disable()
	s.mCopyBootstrapURL = s.mURLsMenu.AddSubMenuItem("  Copy CA URL", "Copy CA Certificate URL to clipboard")

	systray.AddSeparator()

	// Card info section
	s.mCardUID = systray.AddMenuItem("Card UID: None", "Current card UID")
	s.mCardUID.Disable()

	s.mCardType = systray.AddMenuItem("Card Type: None", "Current card type")
	s.mCardType.Disable()

	systray.AddSeparator()

	// Device management section
	s.mDeviceMenu = systray.AddMenuItem("Device", "Select NFC Device")
	mRefreshDevices := s.mDeviceMenu.AddSubMenuItem("Refresh Devices", "Refresh device list")
	s.mDeviceMenu.AddSubMenuItemCheckbox("Auto-detect", "Auto-detect device", true)

	systray.AddSeparator()

	// Mode toggle section
	s.mModeMenu = systray.AddMenuItem("Mode: Read/Write", "Change operation mode")
	s.mReadWriteMode = s.mModeMenu.AddSubMenuItemCheckbox("Read/Write Mode", "Allow both read and write", true)
	s.mReadMode = s.mModeMenu.AddSubMenuItemCheckbox("Read Only Mode", "Only allow reading", false)
	s.mWriteMode = s.mModeMenu.AddSubMenuItemCheckbox("Write Only Mode", "Only allow writing", false)

	systray.AddSeparator()

	// Card type filtering section
	s.mCardFilterMenu = systray.AddMenuItem("Card Type Filter", "Filter cards by type")
	s.mFilterAll = s.mCardFilterMenu.AddSubMenuItemCheckbox("All Types", "Allow all card types", true)

	// Create card type filter menu items for each card type
	for _, cardType := range GetAllCardTypeFilterNames() {
		displayName := GetCardTypeFilterDisplayName(cardType)
		tooltip := GetCardTypeFilterTooltip(cardType)
		menuItem := s.mCardFilterMenu.AddSubMenuItemCheckbox(displayName, tooltip, false)
		s.cardTypeFilters[cardType] = &cardTypeFilterItem{
			menuItem: menuItem,
			cardType: cardType,
		}
	}

	systray.AddSeparator()

	// Agent control section
	s.mStart = systray.AddMenuItem("Start Agent", "Start the NFC agent")
	s.mStop = systray.AddMenuItem("Stop Agent", "Stop the NFC agent")
	s.mStart.Disable() // Disable start since we're auto-starting
	s.mStop.Disable()  // Will be enabled once agent starts

	systray.AddSeparator()
	mQuit := systray.AddMenuItem("Quit", "Quit the application")

	// Start the event handler with the menu items
	go s.handleMenuEvents(mRefreshDevices, mQuit)
}

// autoStartAgent starts the agent automatically
func (s *SystrayApp) autoStartAgent() {
	// Set up device change listener
	s.setupDeviceChangeListener()

	go func() {
		// Start with initial device (may be empty for auto-discovery)
		if err := s.agent.Start(s.initialDevice); err == nil {
			s.updateStatus("Running")
			s.updateURLs()
			s.mStop.Enable()
		} else {
			s.updateStatus("Failed to Start")
			s.mStart.Enable()
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

// startCardInfoUpdater starts a goroutine to update card information
func (s *SystrayApp) startCardInfoUpdater() {
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		lastUID := ""
		lastType := ""

		for range ticker.C {
			var card *nfc.Card
			if s.agent.ClientServer != nil {
				card = s.agent.ClientServer.GetLastCard()
			}

			uid, cardType := s.getCardInfo(card)

			if uid != lastUID {
				s.updateCardUID(uid)
				lastUID = uid
			}

			if cardType != lastType {
				s.updateCardType(cardType)
				lastType = cardType
			}
		}
	}()
}

// startServerRestartListener listens for server restart events from the Agent
// and updates the displayed URLs accordingly.
func (s *SystrayApp) startServerRestartListener() {
	go func() {
		for range s.agent.ServerRestarts() {
			log.Printf("[systray] Server restart detected, updating URLs")
			s.updateURLs()
		}
	}()
}

// startEventHandler starts the main event handling loop
func (s *SystrayApp) startEventHandler() {
	// This will be called from handleMenuEvents
}

// handleMenuEvents processes all menu click events
func (s *SystrayApp) handleMenuEvents(mRefreshDevices, mQuit *systray.MenuItem) {
	for {
		select {
		case <-s.mStart.ClickedCh:
			s.handleStartAgent()
		case <-s.mStop.ClickedCh:
			s.handleStopAgent()
		case <-mRefreshDevices.ClickedCh:
			s.updateDeviceList()
		case <-s.mCopyDeviceURL.ClickedCh:
			if url := s.getDeviceURL(); url != "" {
				if err := copyToClipboard(url); err != nil {
					log.Printf("[systray] Failed to copy to clipboard: %v", err)
				} else {
					log.Printf("[systray] Copied DeviceServer URL to clipboard")
				}
			}
		case <-s.mCopyClientURL.ClickedCh:
			if url := s.getClientURL(); url != "" {
				if err := copyToClipboard(url); err != nil {
					log.Printf("[systray] Failed to copy to clipboard: %v", err)
				} else {
					log.Printf("[systray] Copied ClientServer URL to clipboard")
				}
			}
		case <-s.mCopyBootstrapURL.ClickedCh:
			if url := s.getBootstrapURL(); url != "" {
				if err := copyToClipboard(url); err != nil {
					log.Printf("[systray] Failed to copy to clipboard: %v", err)
				} else {
					log.Printf("[systray] Copied CA Certificate URL to clipboard")
				}
			}
		case <-s.mReadWriteMode.ClickedCh:
			s.handleModeSwitch(nfc.ModeReadWrite, "Read/Write")
		case <-s.mReadMode.ClickedCh:
			s.handleModeSwitch(nfc.ModeReadOnly, "Read Only")
		case <-s.mWriteMode.ClickedCh:
			s.handleModeSwitch(nfc.ModeWriteOnly, "Write Only")
		case <-s.mFilterAll.ClickedCh:
			s.handleFilterAll()
		case <-mQuit.ClickedCh:
			systray.Quit()
			return
		}

		// Handle card type filter selection
		s.handleCardFilterSelection()

		// Handle device selection
		s.handleDeviceSelection()
	}
}

// handleStartAgent starts the agent
func (s *SystrayApp) handleStartAgent() {
	// Use agent's stored device path (or empty for auto-discovery)
	devicePath := s.agent.CurrentDevicePath()
	if err := s.agent.Start(devicePath); err == nil {
		s.updateStatus("Running")
		s.updateURLs()
		s.mStart.Disable()
		s.mStop.Enable()
		s.updateDeviceList() // Refresh to show current device
	} else {
		s.updateStatus("Failed to Start")
	}
}

// handleStopAgent stops the agent
func (s *SystrayApp) handleStopAgent() {
	s.agent.Stop()
	s.updateStatus("Stopped")
	s.clearURLs()
	s.mStop.Disable()
	s.mStart.Enable()
}

// handleModeSwitch switches the reader mode
func (s *SystrayApp) handleModeSwitch(mode nfc.ReaderMode, modeName string) {
	if s.agent.Reader == nil {
		return
	}

	s.agent.Reader.SetMode(mode)
	s.mModeMenu.SetTitle("Mode: " + modeName)

	// Update checkboxes
	s.mReadWriteMode.Uncheck()
	s.mReadMode.Uncheck()
	s.mWriteMode.Uncheck()

	switch mode {
	case nfc.ModeReadWrite:
		s.mReadWriteMode.Check()
	case nfc.ModeReadOnly:
		s.mReadMode.Check()
	case nfc.ModeWriteOnly:
		s.mWriteMode.Check()
	}

	log.Printf("Switched to %s mode", modeName)
}

// handleFilterAll enables all card type filters
func (s *SystrayApp) handleFilterAll() {
	s.mFilterAll.Check()

	// Uncheck all individual filters
	for _, filter := range s.cardTypeFilters {
		filter.menuItem.Uncheck()
	}

	s.agent.AllowAllCardTypes()
}

// handleCardFilterSelection processes card type filter menu selections
func (s *SystrayApp) handleCardFilterSelection() {
	for _, filter := range s.cardTypeFilters {
		select {
		case <-filter.menuItem.ClickedCh:
			s.handleCardTypeToggle(filter)
		default:
			// No click event for this filter
		}
	}
}

// handleCardTypeToggle toggles a card type filter
func (s *SystrayApp) handleCardTypeToggle(filter *cardTypeFilterItem) {
	s.mFilterAll.Uncheck()

	// Toggle the card type
	s.agent.SetAllowCardType(filter.cardType, !filter.menuItem.Checked())

	// Update menu item
	if filter.menuItem.Checked() {
		filter.menuItem.Uncheck()
	} else {
		filter.menuItem.Check()
	}

	// If no filters active, revert to All
	if s.agent.AllowedCardTypesLength() == 0 {
		s.mFilterAll.Check()
	}
}

// handleDeviceSelection processes device menu selections
func (s *SystrayApp) handleDeviceSelection() {
	currentDevice := s.agent.CurrentDevicePath()
	for deviceName, menuItem := range s.deviceMenuItems {
		select {
		case <-menuItem.ClickedCh:
			if currentDevice != deviceName {
				s.switchDevice(deviceName)
			}
		default:
			// No click event for this menu item
		}
	}
}

// switchDevice switches to a different NFC device
func (s *SystrayApp) switchDevice(deviceName string) {
	// Restart agent with new device
	s.agent.Stop()
	if err := s.agent.Start(deviceName); err == nil {
		s.updateStatus("Running")
		s.updateURLs()
		s.mStop.Enable()
		s.mStart.Disable()
	} else {
		s.updateStatus("Failed to Start")
		s.clearURLs()
		s.mStart.Enable()
		s.mStop.Disable()
	}

	// Update menu checkmarks based on agent's current device
	currentDevice := s.agent.CurrentDevicePath()
	for name, item := range s.deviceMenuItems {
		if name == currentDevice {
			item.Check()
		} else {
			item.Uncheck()
		}
	}
}

// updateDeviceList refreshes the list of available devices
func (s *SystrayApp) updateDeviceList() {
	// Clear existing device menu items
	for _, item := range s.deviceMenuItems {
		item.Hide()
	}
	s.deviceMenuItems = make(map[string]*systray.MenuItem)

	// Get available devices
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

	// Add device menu items
	for _, device := range devices {
		deviceName := device
		isChecked := (currentDevice == deviceName)
		item := s.mDeviceMenu.AddSubMenuItemCheckbox(deviceName, "Select this device", isChecked)
		s.deviceMenuItems[deviceName] = item
	}
}

// updateStatus updates the status menu item and icon
func (s *SystrayApp) updateStatus(status string) {
	s.mStatus.SetTitle(status)

	// Update icon based on status
	switch status {
	case "Running":
		systray.SetIcon(iconDataConnected)
	case "Failed to Start":
		systray.SetIcon(iconDataError)
	case "Stopped":
		systray.SetIcon(iconDataStopped)
	default:
		// Starting or other states
		systray.SetIcon(iconData)
	}
}

// getCardInfo extracts UID and type from a card
func (s *SystrayApp) getCardInfo(card *nfc.Card) (uid, cardType string) {
	if card != nil {
		uid = card.UID
		cardType = card.Type
	}
	return
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

// updateURLs updates all server URL displays
func (s *SystrayApp) updateURLs() {
	ips := getLocalIPs()
	ip := "localhost"
	if len(ips) > 0 {
		ip = ips[0]
	}

	// Determine protocol based on TLS
	tlsEnabled := s.agent.CertFile != "" && s.agent.KeyFile != ""
	wsProto := "ws"
	if tlsEnabled {
		wsProto = "wss"
	}

	// Device server URL
	devicePort := s.agent.DevicePort
	if devicePort == 0 {
		devicePort = DEFAULT_DEVICE_PORT
	}
	deviceURL := fmt.Sprintf("%s://%s:%d/ws", wsProto, ip, devicePort)
	s.mDeviceURL.SetTitle(fmt.Sprintf("Device: %s", deviceURL))

	// Client server URL
	clientPort := s.agent.ClientPort
	if clientPort == 0 {
		clientPort = DEFAULT_CLIENT_PORT
	}
	clientURL := fmt.Sprintf("%s://%s:%d/ws", wsProto, ip, clientPort)
	s.mClientURL.SetTitle(fmt.Sprintf("Client: %s", clientURL))

	// Bootstrap/CA URL (always HTTP, only if bootstrap port is set)
	if s.bootstrapPort > 0 {
		bootstrapURL := fmt.Sprintf("http://%s:%d", ip, s.bootstrapPort)
		s.mBootstrapURL.SetTitle(fmt.Sprintf("CA Cert: %s", bootstrapURL))
	} else {
		s.mBootstrapURL.SetTitle("CA Cert: Disabled")
	}
}

// clearURLs resets all URL displays to "Not running"
func (s *SystrayApp) clearURLs() {
	s.mDeviceURL.SetTitle("Device: Not running")
	s.mClientURL.SetTitle("Client: Not running")
	s.mBootstrapURL.SetTitle("CA Cert: Not running")
}

// getDeviceURL returns the current DeviceServer URL
func (s *SystrayApp) getDeviceURL() string {
	ips := getLocalIPs()
	ip := "localhost"
	if len(ips) > 0 {
		ip = ips[0]
	}

	tlsEnabled := s.agent.CertFile != "" && s.agent.KeyFile != ""
	wsProto := "ws"
	if tlsEnabled {
		wsProto = "wss"
	}

	devicePort := s.agent.DevicePort
	if devicePort == 0 {
		devicePort = DEFAULT_DEVICE_PORT
	}
	return fmt.Sprintf("%s://%s:%d/ws", wsProto, ip, devicePort)
}

// getClientURL returns the current ClientServer URL
func (s *SystrayApp) getClientURL() string {
	ips := getLocalIPs()
	ip := "localhost"
	if len(ips) > 0 {
		ip = ips[0]
	}

	tlsEnabled := s.agent.CertFile != "" && s.agent.KeyFile != ""
	wsProto := "ws"
	if tlsEnabled {
		wsProto = "wss"
	}

	clientPort := s.agent.ClientPort
	if clientPort == 0 {
		clientPort = DEFAULT_CLIENT_PORT
	}
	return fmt.Sprintf("%s://%s:%d/ws", wsProto, ip, clientPort)
}

// getBootstrapURL returns the CA certificate download URL
func (s *SystrayApp) getBootstrapURL() string {
	if s.bootstrapPort <= 0 {
		return ""
	}

	ips := getLocalIPs()
	ip := "localhost"
	if len(ips) > 0 {
		ip = ips[0]
	}

	return fmt.Sprintf("http://%s:%d", ip, s.bootstrapPort)
}

// copyToClipboard copies text to the system clipboard
func copyToClipboard(text string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("pbcopy")
	case "linux":
		cmd = exec.Command("xclip", "-selection", "clipboard")
	case "windows":
		cmd = exec.Command("clip")
	default:
		return fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		return err
	}

	_, err = stdin.Write([]byte(text))
	if err != nil {
		return err
	}

	stdin.Close()
	return cmd.Wait()
}
