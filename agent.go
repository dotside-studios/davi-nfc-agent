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
	AllowedCardTypes map[string]bool // Card type filter using map
	APISecret        string

	// Two-server architecture
	Bridge       *server.ServerBridge
	DeviceServer *deviceserver.Server
	ClientServer *clientserver.Server
	DevicePort   int // Default: 9470
	ClientPort   int // Default: 9471

	// TLS configuration (optional, shared by both servers)
	CertFile   string       // Path to TLS certificate file
	KeyFile    string       // Path to TLS private key file
	TLSManager *tls.Manager // TLS manager for auto-TLS and network watching

	// Internal state
	devicePath        string        // Current device path
	serversMu         sync.Mutex    // Protects server restart operations
	serverRestartChan chan struct{} // Signals when servers are restarted
}

func NewAgent(nfcManager nfc.Manager) *Agent {
	return &Agent{
		Logger:            log.New(os.Stderr, "[agent] ", log.LstdFlags),
		Manager:           nfcManager,
		AllowedCardTypes:  make(map[string]bool),
		DevicePort:        9470,
		ClientPort:        9471,
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

	// If no device path specified, discover available devices
	if devicePath == "" {
		devices, err := a.Manager.ListDevices()
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

	// Cleanup Manager if it's a remotenfc.Manager
	if pm, ok := a.Manager.(*remotenfc.Manager); ok {
		pm.Close()
	}

	a.Logger.Println("Agent stopped successfully")
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

	// Get device manager
	var deviceManager *remotenfc.Manager
	if pm, ok := a.Manager.(*remotenfc.Manager); ok {
		deviceManager = pm
	} else if mm, ok := a.Manager.(interface {
		GetManager(string) (nfc.Manager, bool)
	}); ok {
		if mgr, exists := mm.GetManager(nfc.ManagerTypeSmartphone); exists {
			if pm, ok := mgr.(*remotenfc.Manager); ok {
				deviceManager = pm
			}
		}
	}

	// Create device server
	a.DeviceServer = deviceserver.New(deviceserver.Config{
		Reader:           a.Reader,
		DeviceManager:    deviceManager,
		Port:             a.DevicePort,
		APISecret:        a.APISecret,
		AllowedCardTypes: a.AllowedCardTypes,
		CertFile:         a.CertFile,
		KeyFile:          a.KeyFile,
	}, a.Bridge)

	// Create client server
	a.ClientServer = clientserver.New(clientserver.Config{
		Port:      a.ClientPort,
		APISecret: a.APISecret,
		CertFile:  a.CertFile,
		KeyFile:   a.KeyFile,
	}, a.Bridge)

	// Start both servers
	go a.DeviceServer.Start()
	go a.ClientServer.Start()

	a.Logger.Printf("Servers started: Device on port %d, Client on port %d", a.DevicePort, a.ClientPort)
	return nil
}

func (a *Agent) SetAllowCardType(cardType string, allow bool) {
	if allow {
		a.AllowCardType(cardType)
	} else {
		a.DisallowCardType(cardType)
	}
}

func (a *Agent) AllowAllCardTypes() {
	for _, cardType := range nfc.GetAllCardTypes() {
		a.AllowedCardTypes[cardType] = true
	}
}

func (a *Agent) AllowedCardTypesLength() int {
	return len(a.AllowedCardTypes)
}

func (a *Agent) AllowCardType(cardType string) {
	a.AllowedCardTypes[cardType] = true
}

func (a *Agent) DisallowCardType(cardType string) {
	delete(a.AllowedCardTypes, cardType)
}

func (a *Agent) IsCardTypeAllowed(cardType string) bool {
	return a.AllowedCardTypes[cardType]
}

// CurrentDevicePath returns the current device path from the reader.
// Returns empty string if no reader is active.
func (a *Agent) CurrentDevicePath() string {
	if a.Reader == nil {
		return a.devicePath // Return stored path if reader not started
	}
	return a.Reader.DevicePath()
}
