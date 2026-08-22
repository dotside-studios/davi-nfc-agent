package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/buildinfo"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/settings"
	"github.com/dotside-studios/davi-nfc-agent/tls"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// readerSlotCount bounds the NFC readers offered in the Device submenu, which
// reuses a fixed pool of items rather than rebuilding them per refresh; see
// [traymenu.NewList].
const readerSlotCount = 12

// getLocalIPs returns local non-loopback IP addresses (both IPv4 and IPv6 globals).
// IPv4 addresses come first so callers that pick ips[0] get the most broadly
// compatible address. Link-local and unspecified addresses are skipped.
func getLocalIPs() []string {
	var v4, v6 []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}

	for _, addr := range addrs {
		ipNet, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		ip := ipNet.IP
		if ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			continue
		}
		if ip.To4() != nil {
			v4 = append(v4, ip.String())
		} else {
			v6 = append(v6, ip.String())
		}
	}
	return append(v4, v6...)
}

// hostPort joins a host and port using bracket notation for IPv6 literals.
func hostPort(host string, port int) string {
	return net.JoinHostPort(host, strconv.Itoa(port))
}

// SystrayApp manages the system tray interface for the NFC agent
type SystrayApp struct {
	agent         *Agent
	initialDevice string
	bootstrapPort int
	bootstrap     *tls.BootstrapServer // nil if pairing server is disabled
	console       *Console             // nil if the control center is not built in

	// menu is the tray itself. Items declare their own click handlers as they
	// are added, so there is no central event loop to keep in step with them.
	menu *traymenu.Menu

	// Status section
	mStatus   *traymenu.Item
	mCardUID  *traymenu.Item
	mCardType *traymenu.Item
	mStart    *traymenu.Item
	mStop     *traymenu.Item

	// URL menu items. Only the ones relabelled later are held.
	mDeviceURL    *traymenu.Item
	mClientURL    *traymenu.Item
	mBootstrapURL *traymenu.Item
	mPairingPIN   *traymenu.Item
	mAPISecret    *traymenu.Item

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

	// mu guards the state below, which the console changes from its own
	// goroutines while the tray's handlers read it.
	mu   sync.Mutex
	mode nfc.ReaderMode
}

// NewSystrayApp creates a new systray application on the real tray. bootstrap
// may be nil if the pairing server is disabled (e.g. -bootstrap-port 0); the
// pairing PIN menu item is hidden in that case.
func NewSystrayApp(agent *Agent, initialDevice string, bootstrapPort int, bootstrap *tls.BootstrapServer) *SystrayApp {
	return newSystrayApp(agent, initialDevice, bootstrapPort, bootstrap, traymenu.Fyne())
}

// newSystrayApp builds the tray on a given menu driver, so a test can drive the
// menu without a desktop.
func newSystrayApp(agent *Agent, initialDevice string, bootstrapPort int, bootstrap *tls.BootstrapServer, driver traymenu.Driver) *SystrayApp {
	return &SystrayApp{
		agent:         agent,
		initialDevice: initialDevice,
		bootstrapPort: bootstrapPort,
		bootstrap:     bootstrap,
		menu:          traymenu.New(driver),
		mode:          nfc.ModeReadWrite,
	}
}

// syncSettingsToMenu reflects a settings change made in the console.
func (s *SystrayApp) syncSettingsToMenu(next settings.Settings) {
	if s.modes == nil {
		return
	}

	mode := settings.ParseMode(next.Mode)
	s.setMode(mode)
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
	s.startCardInfoUpdater()
	s.startServerRestartListener()
	s.startOriginWatcher()
	s.startDeviceWatcher()
}

// onExit is called when the systray is exiting
func (s *SystrayApp) onExit() {
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

	s.setupConsoleMenu()

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
}

// setupURLsMenu builds the submenu of addresses and their copy entries.
func (s *SystrayApp) setupURLsMenu() {
	urls := s.menu.AddSubmenu("Server URLs", traymenu.Tooltip("Server addresses"))

	s.mDeviceURL = urls.Add("Device: Not running", traymenu.Tooltip("DeviceServer WebSocket URL"), traymenu.Disabled())
	urls.Add("  Copy Device URL",
		traymenu.Tooltip("Copy DeviceServer URL to clipboard"),
		traymenu.OnClick(func() { s.copyValue("DeviceServer URL", s.urls().device) }),
	)

	s.mClientURL = urls.Add("Client: Not running", traymenu.Tooltip("ClientServer URL"), traymenu.Disabled())
	urls.Add("  Copy Client URL",
		traymenu.Tooltip("Copy ClientServer URL to clipboard"),
		traymenu.OnClick(func() { s.copyValue("ClientServer URL", s.urls().client) }),
	)

	s.mBootstrapURL = urls.Add("Pair Phone: Not running", traymenu.Tooltip("Phone-pairing page URL"), traymenu.Disabled())
	urls.Add("  Copy Pairing URL",
		traymenu.Tooltip("Copy phone-pairing URL to clipboard"),
		traymenu.OnClick(func() { s.copyValue("phone-pairing URL", s.urls().bootstrap) }),
	)

	// The PIN entries only mean anything while the pairing server is running.
	noPairing := s.bootstrap == nil
	s.mPairingPIN = urls.Add("Pairing PIN: --",
		traymenu.Tooltip("PIN required when pairing a phone"),
		traymenu.Disabled(),
		traymenu.HiddenIf(noPairing),
	)
	urls.Add("  Copy Pairing PIN",
		traymenu.Tooltip("Copy 6-digit pairing PIN to clipboard"),
		traymenu.HiddenIf(noPairing),
		traymenu.OnClick(func() {
			if s.bootstrap != nil {
				s.copyValue("pairing PIN", s.bootstrap.PIN())
			}
		}),
	)
	urls.Add("  Regenerate Pairing PIN",
		traymenu.Tooltip("Generate a fresh PIN; existing pairing URLs become invalid"),
		traymenu.HiddenIf(noPairing),
		traymenu.OnClick(s.handleRotatePIN),
	)

	// API secret entries, only shown if a secret is configured.
	noSecret := s.agent.APISecret == ""
	s.mAPISecret = urls.Add("API Secret: hidden",
		traymenu.Tooltip("Required from non-loopback phones/clients"),
		traymenu.Disabled(),
		traymenu.HiddenIf(noSecret),
	)
	urls.Add("  Copy API Secret",
		traymenu.Tooltip("Copy the agent's API secret to clipboard"),
		traymenu.HiddenIf(noSecret),
		traymenu.OnClick(func() { s.copyValue("API secret", s.agent.APISecret) }),
	)
	urls.Add("  Regenerate API Secret",
		traymenu.Tooltip("Generate a fresh secret; all phones must re-handshake"),
		traymenu.HiddenIf(noSecret),
		traymenu.OnClick(s.handleRotateAPISecret),
	)
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
	s.modes.Set(nfc.ModeReadWrite)

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

// handleRotatePIN issues a fresh pairing PIN, which invalidates the URLs that
// carried the old one.
func (s *SystrayApp) handleRotatePIN() {
	if s.bootstrap == nil {
		return
	}

	fresh := s.bootstrap.RotatePIN()
	log.Printf("[systray] Pairing PIN rotated to %s", fresh)
	s.updateURLs()
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

// handleModeSwitch applies a mode picked from the menu.
func (s *SystrayApp) handleModeSwitch(mode nfc.ReaderMode) {
	if s.agent.Reader == nil {
		// Nothing to apply it to. Put the tick back rather than showing a mode
		// the reader is not in.
		s.modes.Set(s.currentMode())
		return
	}

	s.agent.Reader.SetMode(mode)
	s.setMode(mode)
	s.mModeMenu.SetTitle("Mode: " + modeName(mode))

	log.Printf("Switched to %s mode", modeName(mode))
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

func (s *SystrayApp) currentMode() nfc.ReaderMode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

func (s *SystrayApp) setMode(mode nfc.ReaderMode) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.mode = mode
}

// applyCardTypes narrows the agent to the ticked card types, or opens it back
// up when none of them are.
func (s *SystrayApp) applyCardTypes(types []string) {
	if len(types) == 0 {
		s.agent.ClearCardTypeFilter()
		return
	}

	picked := make(map[string]bool, len(types))
	for _, cardType := range types {
		picked[cardType] = true
	}
	for _, cardType := range GetAllCardTypeFilterNames() {
		s.agent.SetAllowCardType(cardType, picked[cardType])
	}
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

// agentURLs are the addresses the menu shows and copies. They are built
// together so that what an entry copies is what the entry above it reads.
type agentURLs struct {
	device    string
	client    string
	bootstrap string // empty when the pairing server is disabled
}

// urls builds the addresses from the agent's current configuration.
func (s *SystrayApp) urls() agentURLs {
	ip := "localhost"
	if ips := getLocalIPs(); len(ips) > 0 {
		ip = ips[0]
	}

	scheme := "ws"
	if s.agent.CertFile != "" && s.agent.KeyFile != "" {
		scheme = "wss"
	}
	port := s.agent.DevicePort
	if port == 0 {
		port = DEFAULT_DEVICE_PORT
	}

	// Devices and clients share the single agent port. Devices connect with
	// ?mode=device; clients use plain /ws.
	client := fmt.Sprintf("%s://%s/ws", scheme, hostPort(ip, port))
	urls := agentURLs{device: client + "?mode=device", client: client}

	// The pairing page is always HTTP, and carries the PIN so a link clicked
	// from chat goes straight through.
	if s.bootstrapPort > 0 {
		urls.bootstrap = fmt.Sprintf("http://%s/", hostPort(ip, s.bootstrapPort))
		if s.bootstrap != nil {
			urls.bootstrap += "?pin=" + url.QueryEscape(s.bootstrap.PIN())
		}
	}
	return urls
}

// updateURLs updates all server URL displays
func (s *SystrayApp) updateURLs() {
	urls := s.urls()

	s.mDeviceURL.SetTitle("Device: " + urls.device)
	s.mClientURL.SetTitle("Client: " + urls.client)

	if urls.bootstrap == "" {
		s.mBootstrapURL.SetTitle("Pair Phone: Disabled")
	} else {
		s.mBootstrapURL.SetTitle("Pair Phone: " + urls.bootstrap)
	}
	if s.bootstrap != nil {
		s.mPairingPIN.SetTitle("Pairing PIN: " + s.bootstrap.PIN())
	}

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

// clearURLs resets all URL displays to "Not running"
func (s *SystrayApp) clearURLs() {
	s.mDeviceURL.SetTitle("Device: Not running")
	s.mClientURL.SetTitle("Client: Not running")
	s.mBootstrapURL.SetTitle("Pair Phone: Not running")
}

// copyValue puts a value on the clipboard and logs what happened, which is the
// only feedback a tray menu has for a copy.
func (s *SystrayApp) copyValue(what, value string) {
	if value == "" {
		return
	}

	if err := copyToClipboard(value); err != nil {
		log.Printf("[systray] Failed to copy %s: %v", what, err)
		return
	}
	log.Printf("[systray] Copied %s to clipboard", what)
}

// clipboardCmd describes one candidate clipboard utility.
type clipboardCmd struct {
	name string
	args []string
}

// copyToClipboard copies text to the system clipboard. On Linux it picks the
// tool matching the active display server (wl-copy under Wayland, xclip/xsel
// under X11), falling back to whichever utility is installed if env vars are
// unset (e.g. headless / virtual sessions).
func copyToClipboard(text string) error {
	candidates, err := clipboardCandidates(runtime.GOOS, os.Getenv)
	if err != nil {
		return err
	}

	var lastErr error
	var tried []string
	for _, c := range candidates {
		path, err := exec.LookPath(c.name)
		if err != nil {
			continue
		}
		tried = append(tried, c.name)
		if err := pipeStringToCommand(path, c.args, text); err != nil {
			lastErr = err
			continue
		}
		return nil
	}

	if lastErr != nil {
		return fmt.Errorf("clipboard write failed (tried %s): %w", strings.Join(tried, ", "), lastErr)
	}
	return clipboardUnavailableError()
}

// clipboardCandidates returns the ordered list of clipboard utilities to try
// for the given OS, using getenv to inspect the current display environment.
// Pure and testable; pass os.Getenv in production.
func clipboardCandidates(goos string, getenv func(string) string) ([]clipboardCmd, error) {
	switch goos {
	case "darwin":
		return []clipboardCmd{{name: "pbcopy"}}, nil
	case "windows":
		return []clipboardCmd{{name: "clip"}}, nil
	case "linux":
		var cands []clipboardCmd
		if getenv("WAYLAND_DISPLAY") != "" {
			cands = append(cands, clipboardCmd{name: "wl-copy"})
		}
		if getenv("DISPLAY") != "" {
			cands = append(cands,
				clipboardCmd{name: "xclip", args: []string{"-selection", "clipboard"}},
				clipboardCmd{name: "xsel", args: []string{"--clipboard", "--input"}},
			)
		}
		// Env didn't tell us the session type — try everything in preference order.
		if len(cands) == 0 {
			cands = []clipboardCmd{
				{name: "wl-copy"},
				{name: "xclip", args: []string{"-selection", "clipboard"}},
				{name: "xsel", args: []string{"--clipboard", "--input"}},
			}
		}
		return cands, nil
	default:
		return nil, fmt.Errorf("unsupported platform: %s", goos)
	}
}

func clipboardUnavailableError() error {
	if runtime.GOOS == "linux" {
		return fmt.Errorf("no clipboard utility found; install one of: wl-clipboard (Wayland), xclip, or xsel")
	}
	return fmt.Errorf("no clipboard utility found")
}

func pipeStringToCommand(path string, args []string, text string) error {
	cmd := exec.Command(path, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	if _, err := io.WriteString(stdin, text); err != nil {
		_ = stdin.Close()
		_ = cmd.Wait()
		return err
	}
	if err := stdin.Close(); err != nil {
		_ = cmd.Wait()
		return err
	}
	return cmd.Wait()
}
