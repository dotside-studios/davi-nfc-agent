package pcsc

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// device implements nfc.Device for a card on a PC/SC reader.
type device struct {
	ctx        scardContext
	card       scardCard
	readerName string
	uid        string
	atr        []byte
	mu         sync.Mutex

	// scardCard presence tracking for reliable removal detection
	lastEventState stateFlag // Last known EventState from GetStatusChange
	lastEventCount uint16    // Event counter from upper 16 bits of EventState

	// Background monitoring for card removal
	stopMonitor chan struct{} // Signals the monitor goroutine to stop
	cardRemoved chan struct{} // Signals that card was removed (detected by monitor)

	// Tracks if unsupported tag error was already reported for current card
	unsupportedReported bool

	// feedback is the channel this reader's LED and buzzer commands travel
	// on, settled on the first signal. See feedback.go.
	feedback feedbackTransport
}

// newDevice creates a device for a card already connected on a reader.
func newDevice(ctx scardContext, card scardCard, readerName string) (*device, error) {
	// Validate protocol before any operations - the scard library panics on invalid protocol
	proto := card.ActiveProtocol()
	if proto != protocolT0 && proto != protocolT1 {
		return nil, fmt.Errorf("unsupported card protocol: %d", proto)
	}

	dev := &device{
		ctx:        ctx,
		card:       card,
		readerName: readerName,
	}

	// Get card status to retrieve ATR
	status, err := card.Status()
	if err != nil {
		return nil, fmt.Errorf("failed to get card status: %w", err)
	}
	dev.atr = status.Atr

	// Initialize card presence tracking state
	// This captures the initial event counter and state for reliable detection
	readerStates := []readerState{
		{Reader: readerName, CurrentState: stateUnaware},
	}
	if err := ctx.GetStatusChange(readerStates, 0); err == nil {
		dev.lastEventState = readerStates[0].EventState & ^stateChanged
		dev.lastEventCount = uint16(readerStates[0].EventState >> 16)
	}

	// Get UID
	uid, err := dev.getUID()
	if err != nil {
		pcscWarn.Printf("could not get UID: %v", err)
	} else {
		dev.uid = uid
	}

	// Start background card removal monitor
	dev.startCardMonitor()

	return dev, nil
}

func (d *device) Close() error {
	// Stop the background monitor first (outside the lock to avoid deadlock)
	d.stopCardMonitor()

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.card != nil {
		err := d.card.Disconnect(leaveCard)
		d.card = nil
		return err
	}
	return nil
}

// startCardMonitor starts a background goroutine that monitors for card removal.
// This provides the most reliable detection by continuously checking card state.
func (d *device) startCardMonitor() {
	d.stopMonitor = make(chan struct{})
	d.cardRemoved = make(chan struct{}, 1)

	go func() {
		// Start with current state
		d.mu.Lock()
		readerStates := []readerState{
			{Reader: d.readerName, CurrentState: d.lastEventState},
		}
		ctx := d.ctx
		d.mu.Unlock()

		if ctx == nil {
			return
		}

		for {
			select {
			case <-d.stopMonitor:
				return
			default:
			}

			// Block for up to 500ms waiting for state change
			// Timeout allows checking stopMonitor channel periodically
			err := ctx.GetStatusChange(readerStates, 500*time.Millisecond)
			if err != nil {
				// Check if context was cancelled
				if errors.Is(err, errCancelled) {
					return
				}
				// Timeout is normal - just loop again
				if errors.Is(err, errTimeout) {
					continue
				}
				// Other errors may indicate reader disconnection
				pcscWarn.Printf("cardMonitor: error %v, treating as removal", err)
				select {
				case d.cardRemoved <- struct{}{}:
				default:
				}
				return
			}

			eventState := readerStates[0].EventState

			// Check if card was removed - only use stateEmpty as the definitive indicator
			// statePresent being absent during transitions can cause false positives
			if (eventState & stateEmpty) != 0 {
				select {
				case d.cardRemoved <- struct{}{}:
				default:
				}
				return
			}

			// Update state for next iteration (clear stateChanged flag)
			readerStates[0].CurrentState = eventState & ^stateChanged
		}
	}()
}

// stopCardMonitor stops the background card removal monitor
func (d *device) stopCardMonitor() {
	if d.stopMonitor != nil {
		close(d.stopMonitor)
		d.stopMonitor = nil
	}
}

func (d *device) String() string {
	return d.readerName
}

func (d *device) Connection() string {
	return d.readerName
}

// DeviceType returns the device type identifier (implements DeviceInfoProvider)
func (d *device) DeviceType() string {
	return "pcsc"
}

// SupportedTagTypes returns the list of supported tag types (implements DeviceInfoProvider)
func (d *device) SupportedTagTypes() []string {
	return []string{"MIFARE Classic", "DESFire", "Ultralight", "NTAG", "ISO14443-4"}
}

// SupportsTransceive reports that a PC/SC reader can exchange APDUs with the
// tag (implements nfc.DeviceTransceiver). Declared rather than left to the
// default so the answer does not depend on what else this device implements.
func (d *device) SupportsTransceive() bool {
	return true
}

// IsHealthy checks if the device is still connected (implements DeviceHealthChecker)
func (d *device) IsHealthy() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.card == nil {
		return fmt.Errorf("device not connected")
	}

	// Try to get status to verify connection
	_, err := d.card.Status()
	if err != nil {
		return fmt.Errorf("device health check failed: %w", err)
	}

	return nil
}

// IsCardPresent checks if a card is still present on the reader (public, acquires lock)
func (d *device) IsCardPresent() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.checkCardPresence() == nil
}

// checkCardPresence checks if card is still present (internal, caller must hold lock)
// On macOS with ACR122U, card.Status() and GetStatusChange don't reliably detect
// card removal. Instead, we send a GET_UID command which will fail if card is gone.
func (d *device) checkCardPresence() error {
	if d.card == nil || d.ctx == nil {
		return nfc.NewCardRemovedError(fmt.Errorf("device not connected"))
	}

	// Send GET_UID command - this is a simple command that should always succeed
	// if a card is present. If it fails, the card was likely removed.
	cmd := nfc.GetUIDAPDU()
	resp, err := d.card.Transmit(cmd)
	if err != nil {
		if isCardRemovedPCSCError(err) {
			return nfc.NewCardRemovedError(err)
		}
		return nfc.NewCardRemovedError(fmt.Errorf("card presence check failed: %w", err))
	}

	// Check APDU response - GET_UID should return success (90 00)
	if len(resp) < 2 {
		return nfc.NewCardRemovedError(fmt.Errorf("invalid response length"))
	}
	sw1, sw2 := resp[len(resp)-2], resp[len(resp)-1]
	if sw1 != 0x90 || sw2 != 0x00 {
		return nfc.NewCardRemovedError(fmt.Errorf("card not responding"))
	}

	return nil
}

// Transceive sends raw data to the card and returns the response.
// scardCard removal is detected via:
// 1. Background monitor signaling card removal
// 2. Transmit errors indicating card was removed
func (d *device) Transceive(txData []byte) ([]byte, error) {
	// Quick check if background monitor detected removal (non-blocking)
	if d.cardRemoved != nil {
		select {
		case <-d.cardRemoved:
			return nil, nfc.NewCardRemovedError(fmt.Errorf("card removed (detected by monitor)"))
		default:
		}
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.card == nil {
		return nil, nfc.NewCardRemovedError(fmt.Errorf("device not connected"))
	}

	// Validate protocol before transmit - the scard library panics on invalid protocol
	proto := d.card.ActiveProtocol()
	if proto != protocolT0 && proto != protocolT1 {
		return nil, nfc.NewCardRemovedError(fmt.Errorf("invalid card protocol"))
	}

	// Transmit APDU - let transmit errors indicate card removal
	rxData, err := d.card.Transmit(txData)
	if err != nil {
		// Check if this is a card removal error
		if isCardRemovedPCSCError(err) {
			return nil, nfc.NewCardRemovedError(err)
		}
		return nil, fmt.Errorf("pcsc device transceive: %w", err)
	}

	return rxData, nil
}

// isCardRemovedPCSCError checks if a PC/SC error indicates the card was removed.
// Uses typed error checking first (most reliable), with string matching fallback.
func isCardRemovedPCSCError(err error) bool {
	if err == nil {
		return false
	}

	// Check typed errors first (most reliable)
	if errors.Is(err, errRemovedCard) {
		return true
	}
	if errors.Is(err, errResetCard) {
		return true // Often means removed on macOS
	}
	if errors.Is(err, errNoSmartcard) {
		return true
	}
	if errors.Is(err, errUnpoweredCard) {
		return true
	}

	// Fallback to string matching for edge cases and non-standard error messages
	errLower := strings.ToLower(err.Error())
	return strings.Contains(errLower, "removed") ||
		strings.Contains(errLower, "reset") ||
		strings.Contains(errLower, "unpowered") ||
		strings.Contains(errLower, "transaction") ||
		strings.Contains(errLower, "comm") ||
		strings.Contains(errLower, "no smart card") ||
		strings.Contains(errLower, "not transacted")
}

// getUID retrieves the card UID using GET UID APDU
func (d *device) getUID() (string, error) {
	// GET UID: FF CA 00 00 00
	cmd := nfc.GetUIDAPDU()
	resp, err := d.card.Transmit(cmd)
	if err != nil {
		return "", fmt.Errorf("GET UID failed: %w", err)
	}

	parsed, err := nfc.ParseAPDUResponse(resp)
	if err != nil {
		return "", err
	}

	if !parsed.IsSuccess() {
		return "", parsed.Error()
	}

	return nfc.BytesToHex(parsed.Data), nil
}

// GetTags returns the tags detected on this reader
// For PC/SC, a card is already connected, so we detect its type and return it
func (d *device) GetTags() ([]nfc.Tag, error) {
	// Quick check if background monitor detected removal (non-blocking)
	// This is critical for detecting when an unsupported tag is removed
	if d.cardRemoved != nil {
		select {
		case <-d.cardRemoved:
			return nil, nfc.NewCardRemovedError(fmt.Errorf("card removed (detected by monitor)"))
		default:
		}
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.card == nil {
		return nil, fmt.Errorf("device not connected")
	}

	// Detect tag type from ATR
	tagType := nfc.DetectTagTypeFromATR(d.atr)

	// If we couldn't get UID earlier, try again
	if d.uid == "" {
		uid, err := d.getUID()
		if err != nil {
			return nil, fmt.Errorf("failed to get UID: %w", err)
		}
		d.uid = uid
	}

	// Create appropriate tag wrapper based on detected type
	tag := nfc.NewTagForType(tagType, d, d.uid)
	if tag == nil {
		// Try to detect more precisely using commands
		tag = d.detectTagWithCommands()
	}
	if tag == nil {
		// Fall back to ISO14443-4 for unknown tags with SAK indicating ISO compliance
		if isISO14443_4Compatible(d.atr) {
			tag = nfc.NewTagForType(nfc.DetectedISO14443_4, d, d.uid)
		} else {
			// Return error only once per card session to avoid log spam
			if !d.unsupportedReported {
				d.unsupportedReported = true
				return nil, nfc.NewUnsupportedTagError(nfc.BytesToHex(d.atr))
			}
			// Already reported, return nil to indicate no tags without error
			return nil, nil
		}
	}

	if tag != nil {
		return []nfc.Tag{tag}, nil
	}

	return nil, nil
}

// detectTagWithCommands attempts to detect tag type using NFC commands
func (d *device) detectTagWithCommands() nfc.Tag {
	// Try GET_VERSION for NTAG/Ultralight EV1
	version, err := d.tryGetVersion()
	if err == nil && len(version) >= 8 {
		if tag := nfc.NewTagForType(nfc.ParseGetVersionResponse(version), d, d.uid); tag != nil {
			return tag
		}
	}

	// Try MIFARE Classic authentication probe
	if d.tryClassicAuth() {
		return nfc.NewTagForType(nfc.DetectedClassic1K, d, d.uid)
	}

	return nil
}

// tryGetVersion sends GET_VERSION command
func (d *device) tryGetVersion() ([]byte, error) {
	// Direct transmit of GET_VERSION (0x60)
	cmd := nfc.GetVersionAPDU()
	resp, err := d.card.Transmit(cmd)
	if err != nil {
		return nil, err
	}

	parsed, err := nfc.ParseAPDUResponse(resp)
	if err != nil {
		return nil, err
	}

	if !parsed.IsSuccess() {
		return nil, parsed.Error()
	}

	return parsed.Data, nil
}

// tryClassicAuth tries to authenticate to sector 0 with default keys
func (d *device) tryClassicAuth() bool {
	// Try default keys
	keys := [][]byte{
		{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}, // Transport key
		{0xD3, 0xF7, 0xD3, 0xF7, 0xD3, 0xF7}, // NFC Forum public key
		{0xA0, 0xA1, 0xA2, 0xA3, 0xA4, 0xA5}, // MAD key
	}

	for _, key := range keys {
		// Load key
		loadCmd := nfc.LoadKeyAPDU(0x00, key)
		resp, err := d.card.Transmit(loadCmd)
		if err != nil {
			continue
		}
		parsed, _ := nfc.ParseAPDUResponse(resp)
		if !parsed.IsSuccess() {
			continue
		}

		// Try auth to block 3 (sector 0 trailer)
		authCmd := nfc.MIFAREAuthAPDU(0x03, nfc.MIFAREKeyA, 0x00)
		resp, err = d.card.Transmit(authCmd)
		if err != nil {
			continue
		}
		parsed, _ = nfc.ParseAPDUResponse(resp)
		if parsed.IsSuccess() {
			return true
		}
	}

	return false
}

// isISO14443_4Compatible checks if ATR indicates ISO14443-4 support
func isISO14443_4Compatible(atr []byte) bool {
	// Look for ISO14443-4 indicator in ATR
	// Typically SAK byte with bit 5 set (0x20)
	if len(atr) < 2 {
		return false
	}

	// Check for common ISO14443-4 ATR patterns
	// This is a simplified check - real detection should parse ATR properly
	for i := 0; i < len(atr)-1; i++ {
		if atr[i] == 0x80 && (atr[i+1]&0x20) != 0 {
			return true
		}
	}

	return false
}

// readerContainsPattern checks if reader name contains common NFC reader patterns
func readerContainsPattern(name string) bool {
	patterns := []string{
		"ACR", "ACS", "NFC", "PICC", "Contactless",
		"SCL", "HID", "Identiv", "CCID", "Dual",
	}
	upperName := strings.ToUpper(name)
	for _, p := range patterns {
		if strings.Contains(upperName, strings.ToUpper(p)) {
			return true
		}
	}
	return false
}

// filterContactlessReaders filters reader list to only contactless readers
func filterContactlessReaders(readers []string) []string {
	var filtered []string
	for _, r := range readers {
		// Skip SAM slots (usually contain "SAM" or end with numbers indicating slots)
		if strings.Contains(strings.ToUpper(r), "SAM") {
			continue
		}
		// Include readers that match NFC patterns
		if readerContainsPattern(r) {
			filtered = append(filtered, r)
		} else {
			// Include all readers if none match patterns (fallback)
			filtered = append(filtered, r)
		}
	}
	return filtered
}
