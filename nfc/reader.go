package nfc

import (
	"fmt"
	"log"
	"sync"
	"time"
)

// Polling intervals
const (
	DefaultPollingInterval      = 100 * time.Millisecond
	DeviceIdleCheckInterval     = 200 * time.Millisecond
	WriteCheckInterval          = 50 * time.Millisecond
	CardCheckTickerInterval     = 250 * time.Millisecond
	DeviceResetWaitTime         = 3 * time.Second
	DeviceErrorCooldownPeriod   = 10 * time.Second
	MaxRetriesCooldownPeriod    = 30 * time.Second
	PostErrorPauseTime          = 1 * time.Second
	UnhandledErrorRetryInterval = 1 * time.Second
)

// ReaderMode defines the access mode for the NFC reader.
type ReaderMode int

const (
	// ModeReadWrite allows both read and write operations (default).
	ModeReadWrite ReaderMode = iota
	// ModeReadOnly allows only read operations.
	ModeReadOnly
	// ModeWriteOnly allows only write operations.
	ModeWriteOnly
)

// NFCReader manages NFC device interactions and broadcasts tag data.
type NFCReader struct {
	deviceManager    *DeviceManager
	dataChan         chan NFCData      // Broadcasts successfully read NFC data
	statusChan       chan DeviceStatus // Broadcasts device status updates
	stopChan         chan struct{}     // Signals the worker to stop
	cache            *TagCache         // Caches tag data
	mode             ReaderMode        // Access mode for the reader
	clock            Clock             // Clock abstraction for time operations
	statusMux        sync.RWMutex
	cardPresent      bool           // Internal tracking of card presence
	isWriting        bool           // Tracks if a write operation is in progress
	operationMutex   sync.Mutex     // Protects tag operations (read/write)
	operationTimeout time.Duration  // Timeout for tag operations
	cardCheckTicker  Ticker         // Ticker for periodic card presence checks (based on cache)
	workerWg         sync.WaitGroup // Tracks worker goroutine completion
}

// NewNFCReader creates and initializes a new NFCReader instance with default ModeReadWrite.
func NewNFCReader(deviceStr string, manager Manager, opTimeout time.Duration) (*NFCReader, error) {
	return NewNFCReaderWithClock(deviceStr, manager, opTimeout, nil)
}

// NewNFCReaderWithClock creates and initializes a new NFCReader with a custom clock.
// If clock is nil, uses RealClock.
func NewNFCReaderWithClock(deviceStr string, manager Manager, opTimeout time.Duration, clock Clock) (*NFCReader, error) {
	if manager == nil {
		return nil, fmt.Errorf("NFCManager cannot be nil")
	}
	if opTimeout <= 0 {
		opTimeout = 5 * time.Second // Default operation timeout
	}
	if clock == nil {
		clock = NewRealClock()
	}

	deviceManager := NewDeviceManager(manager, deviceStr, clock)

	reader := &NFCReader{
		deviceManager:    deviceManager,
		dataChan:         make(chan NFCData, 1),      // Buffered to prevent blocking on send if no listener
		statusChan:       make(chan DeviceStatus, 1), // Buffered for status updates
		stopChan:         make(chan struct{}),
		cache:            NewTagCache(),
		mode:             ModeReadWrite, // Default to read/write mode
		clock:            clock,
		cardPresent:      false,
		operationTimeout: opTimeout,
	}

	// Attempt initial connection synchronously
	// If it fails, the worker will retry via device check ticker
	_ = deviceManager.EnsureConnected(make(chan struct{}))

	return reader, nil
}

// SetMode changes the reader's access mode at runtime.
func (r *NFCReader) SetMode(mode ReaderMode) {
	r.statusMux.Lock()
	defer r.statusMux.Unlock()
	r.mode = mode
	log.Printf("Reader mode changed to: %v", mode)
}

// GetMode returns the current reader mode.
func (r *NFCReader) GetMode() ReaderMode {
	r.statusMux.RLock()
	defer r.statusMux.RUnlock()
	return r.mode
}

// Close releases resources. Does not stop the worker, use Stop() for that.
func (r *NFCReader) Close() {
	log.Println("NFCReader Close called (resource cleanup).")
	r.deviceManager.Close()
	// Note: Channels dataChan, statusChan are not closed here as they might be read by other goroutines.
	// They are managed by the lifecycle of the NFCReader user.
}

// Stop gracefully shuts down the NFCReader worker and waits for it to complete.
func (r *NFCReader) Stop() {
	log.Println("Stopping NFCReader...")
	select {
	case <-r.stopChan:
		log.Println("Stop channel already closed or closing.")
		return // Already stopping or stopped
	default:
		close(r.stopChan)
		log.Println("Stop channel successfully closed, waiting for worker to finish...")
	}
	// Wait for the worker to finish
	r.workerWg.Wait()
	log.Println("NFCReader worker stopped successfully.")
	// Worker's defer will handle device closing and final status.
}

// Start begins the NFC reading process in a separate goroutine.
func (r *NFCReader) Start() {
	log.Println("NFCReader Start called, starting worker.")
	r.workerWg.Add(1)
	go r.worker()
}

// Data returns a channel that provides NFCData as tags are read.
func (r *NFCReader) Data() <-chan NFCData {
	return r.dataChan
}

// StatusUpdates returns a channel that provides DeviceStatus updates.
func (r *NFCReader) StatusUpdates() <-chan DeviceStatus {
	return r.statusChan
}

// GetDeviceStatus returns the current device status by querying live state.
func (r *NFCReader) GetDeviceStatus() DeviceStatus {
	cardPres := r.readCardPresent()
	connected := r.deviceManager.HasDevice()
	var message string
	if connected {
		dev := r.deviceManager.Device()
		if dev != nil {
			message = fmt.Sprintf("Connected to %s", dev.String())
		} else {
			message = "Connected"
		}
	} else if r.deviceManager.InCooldown() {
		message = "Device in cooldown"
	} else {
		message = "Not connected"
	}

	return DeviceStatus{
		Connected:   connected,
		Message:     message,
		CardPresent: cardPres,
	}
}

// readCardPresent safely reads the cardPresent flag.
func (r *NFCReader) readCardPresent() bool {
	r.statusMux.RLock()
	defer r.statusMux.RUnlock()
	return r.cardPresent
}

// handleDeviceEvent processes device lifecycle events from DeviceManager.
func (r *NFCReader) handleDeviceEvent(event DeviceEvent) {
	switch event.Type {
	case DeviceConnected:
		log.Printf("Device event: Connected - %s", event.Message)
		r.LogDeviceInfo()
		r.broadcastDeviceStatus() // Use default message

	case DeviceDisconnected:
		log.Printf("Device event: Disconnected - %s", event.Message)
		r.broadcastDeviceStatus("Device disconnected")

	case DeviceReconnecting:
		log.Printf("Device event: Reconnecting - %s", event.Message)
		r.broadcastDeviceStatus(fmt.Sprintf("Reconnecting: %s", event.Message))

	case DeviceReconnectFailed:
		log.Printf("Device event: Reconnect failed - %s", event.Message)
		r.broadcastDeviceStatus(fmt.Sprintf("Connection failed: %s", event.Message))

	case CooldownStarted:
		log.Printf("Device event: Cooldown started - %s", event.Message)
		r.broadcastDeviceStatus("Device in cooldown")

	case CooldownEnded:
		log.Printf("Device event: Cooldown ended - %s", event.Message)
		r.broadcastDeviceStatus("Attempting reconnection after cooldown")

	case DeviceError:
		log.Printf("Device event: Error - %s", event.Message)
		// Error already handled by DeviceManager

	default:
		log.Printf("Device event: Unknown type %d - %s", event.Type, event.Message)
	}
}

// handleCardCheck updates card presence based on cache status.
func (r *NFCReader) handleCardCheck() {
	currentCacheCardPresent := r.cache.IsCardPresent()
	cardPres := r.readCardPresent()
	if cardPres != currentCacheCardPresent {
		r.setCardPresent(currentCacheCardPresent)
		if currentCacheCardPresent {
			uid := r.cache.GetLastScanned()
			log.Printf("Card presence changed via cache: DETECTED (UID: %s)", uid)
		} else {
			log.Println("Card presence changed via cache: REMOVED/timed out")
		}
	}
}

// handleDeviceErrors processes errors from getTags and determines recovery action.
// Returns true if the error was handled and the caller should continue the loop.
// Retry logic is now managed internally by DeviceManager.
func (r *NFCReader) handleDeviceErrors(err error) bool {
	// Clear write flag on error
	r.statusMux.Lock()
	r.isWriting = false
	r.statusMux.Unlock()

	// Handle card removal specially - close device to allow reconnection
	if IsCardRemovedError(err) {
		log.Println("Card was removed, closing device for reconnection")
		r.deviceManager.Close()
		r.setCardPresent(false)
		r.broadcastDeviceStatus("Card removed, waiting for new card")
		return true
	}

	// Handle unsupported tags - don't close device, just wait for card removal
	// Closing would cause immediate reconnection to the same unsupported tag
	if IsUnsupportedTagError(err) {
		// Error is only returned once per card by the device, so just log it
		log.Printf("Unsupported tag detected: %v - waiting for card removal", err)
		r.setCardPresent(true) // Card is present, just not supported
		r.broadcastDeviceStatus("Unsupported tag, please use a different card")
		// Don't close - the card removal detection will handle when the card is removed
		return true
	}

	// Delegate error handling to DeviceManager (retry logic is now managed internally)
	needsCooldown := r.deviceManager.HandleError(err, r.stopChan)

	if needsCooldown {
		r.broadcastDeviceStatus("Device in cooldown")
		return true
	}

	// Check if device was reconnected successfully
	if r.deviceManager.HasDevice() {
		r.broadcastDeviceStatus() // Use default message from GetDeviceStatus
		return true
	}

	// For unhandled errors, send to data channel
	if !IsIOError(err) && !IsDeviceConfigError(err) && !IsTimeoutError(err) && !IsDeviceClosedError(err) {
		log.Printf("Unhandled error from getTags: %v. Sending to dataChan.", err)
		r.dataChan <- NFCData{Card: nil, Err: fmt.Errorf("get tags error: %v", err)}
		r.clock.Sleep(UnhandledErrorRetryInterval)
	}

	return true
}

// handleTagPolling processes detected tags and sends data to the channel.
func (r *NFCReader) handleTagPolling(tags []Tag) {
	// Check read permission
	r.statusMux.RLock()
	mode := r.mode
	r.statusMux.RUnlock()

	if mode == ModeWriteOnly {
		// In write-only mode, skip reading card data but still update cache for write operations
		for _, tag := range tags {
			uid := tag.UID()
			if uid != "" {
				r.cache.UpdateLastSeenTime(uid)
				// Mark as seen so writes can proceed
				r.cache.HasChanged(uid)
			}
		}
		return
	}

	for _, tag := range tags {
		uid := tag.UID()

		if uid != "" {
			r.cache.UpdateLastSeenTime(uid)
		}

		// Create Card wrapper
		card := NewCard(tag)
		if _, err := card.ReadMessage(); err != nil {
			// Check if this is a card removal error - if so, close the device
			if IsCardRemovedError(err) {
				log.Println("Card was removed during read, closing device for reconnection")
				r.deviceManager.Close()
				r.setCardPresent(false)
				r.broadcastDeviceStatus("Card removed, waiting for new card")
				return
			}
			log.Printf("Error reading data for card UID %s (Type: %s): %v", uid, card.Type, err)
			// Send card with error
			r.dataChan <- NFCData{Card: card, Err: err}
			continue
		}

		if r.cache.HasChanged(uid) {
			log.Printf("Card data changed or new card: UID %s (Type: %s)", uid, card.Type)
			r.dataChan <- NFCData{Card: card, Err: nil}
		}

		r.clock.Sleep(DefaultPollingInterval)
	}
}

func (r *NFCReader) worker() {
	log.Println("NFCReader worker started.")
	defer log.Println("NFCReader worker stopped.")

	r.cardCheckTicker = r.clock.NewTicker(CardCheckTickerInterval)
	pollTicker := r.clock.NewTicker(DefaultPollingInterval)

	defer func() {
		r.cardCheckTicker.Stop()
		pollTicker.Stop()
		r.deviceManager.Close()
		r.broadcastDeviceStatus("Worker stopped, device disconnected.")
		r.workerWg.Done()
		log.Println("Worker goroutine finished.")
	}()

	for {
		select {
		case <-r.stopChan:
			return

		case event := <-r.deviceManager.Events():
			r.handleDeviceEvent(event)

		case <-r.cardCheckTicker.C():
			r.handleCardCheck()

		case <-pollTicker.C():
			r.pollOnce()
		}
	}
}

// pollOnce performs a single polling iteration for device connection and tag reading.
// It runs the actual polling in a goroutine with a timeout to prevent blocking shutdown.
func (r *NFCReader) pollOnce() {
	// Check if we're stopping before starting any work
	select {
	case <-r.stopChan:
		return
	default:
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		r.doPoll()
	}()

	// Wait for poll to complete or stop signal
	select {
	case <-done:
		// Poll completed normally
	case <-r.stopChan:
		// Stop signal received - don't wait for poll to complete
		return
	case <-r.clock.After(5 * time.Second):
		// Poll taking too long - continue anyway to stay responsive
		log.Println("Poll operation taking longer than expected")
	}
}

// doPoll performs the actual polling work.
func (r *NFCReader) doPoll() {
	hasDev := r.deviceManager.HasDevice()
	inCool := r.deviceManager.InCooldown()

	r.statusMux.RLock()
	isWrite := r.isWriting
	r.statusMux.RUnlock()

	if inCool {
		return
	}

	// Try to connect if no device
	if !hasDev {
		// If no device path configured, try to discover one
		if r.deviceManager.DevicePath() == "" {
			devices, err := r.deviceManager.Manager().ListDevices()
			if err != nil || len(devices) == 0 {
				return // No devices available yet
			}
			// Auto-select the first available device
			log.Printf("Device discovered, auto-selecting: %s", devices[0])
			r.deviceManager.SetDevicePath(devices[0])
		}
		if err := r.deviceManager.TryConnect(); err != nil {
			// No card present is normal - just wait and retry
			if !IsNoCardError(err) {
				log.Printf("Connection attempt failed: %v", err)
			}
		}
		return
	}

	if isWrite {
		return
	}

	tags, err := r.GetTags()
	if err != nil {
		r.handleDeviceErrors(err)
		return
	}

	if len(tags) > 0 {
		r.handleTagPolling(tags)
	}
}

// broadcastDeviceStatus broadcasts a device status update.
// It queries the current live state via GetDeviceStatus().
// An optional custom message can be provided to override the default message.
func (r *NFCReader) broadcastDeviceStatus(customMessage ...string) {
	status := r.GetDeviceStatus()

	// Allow override for specific messages like "Reconnecting...", "Failed to connect", etc.
	if len(customMessage) > 0 && customMessage[0] != "" {
		status.Message = customMessage[0]
	}

	select {
	case r.statusChan <- status:
	default:
		log.Println("Warning: Device status channel full or no listener.")
	}
}

// LogDeviceInfo logs information about the connected NFC device.
func (r *NFCReader) LogDeviceInfo() {
	dev := r.deviceManager.Device()
	if dev == nil {
		return
	}
	name := dev.String()
	connString := dev.Connection()
	devicePath := r.deviceManager.DevicePath()
	log.Printf("Connected NFC device: %s (Connection: %s, Path: %s)", name, connString, devicePath)
}

// GetLastScannedData retrieves the last scanned UID from the cache.
func (r *NFCReader) GetLastScannedData() string {
	return r.cache.GetLastScanned()
}

func (r *NFCReader) setCardPresent(present bool) {
	r.statusMux.Lock()
	if r.cardPresent == present { // Avoid redundant updates
		r.statusMux.Unlock()
		return
	}
	r.cardPresent = present
	r.statusMux.Unlock()

	// Construct message based on card presence
	var message string
	if present {
		uid := r.cache.GetLastScanned()
		if uid != "" {
			message = fmt.Sprintf("Card detected (UID: %s)", uid)
		} else {
			message = "Card detected"
		}
	} else {
		message = "Card removed"
		r.cache.Clear() // Clear cache when card is definitively removed
	}

	// Broadcast status with custom message
	r.broadcastDeviceStatus(message)
}

// WriteOptions controls how data is written to NFC cards at the reader level.
type WriteOptions struct {
	// Overwrite completely replaces card data. If false, performs partial update.
	// Partial updates only work if the card already contains valid NDEF data.
	Overwrite bool

	// Index specifies which record to update (for NDEF partial updates).
	// -1 means append, >= 0 means replace at that index.
	// Ignored if Overwrite is true or card doesn't support NDEF.
	Index int

	// ForceInitialize forces reinitialization of MIFARE Classic cards even if they
	// contain existing data. WARNING: This will erase all existing data on the card.
	// Only set this to true if you explicitly want to wipe and reinitialize the card.
	ForceInitialize bool
}

// WriteCardData attempts to write data to a detected NFC card using default options (overwrite mode).
func (r *NFCReader) WriteCardData(text string) error {
	msg := &NDEFMessageBuilder{
		Records: []NDEFRecordBuilder{
			&NDEFText{Content: text, Language: "en"},
		},
	}
	ndefMsg := msg.MustBuild()
	return r.WriteMessageWithOptions(ndefMsg, WriteOptions{
		Overwrite: true,
		Index:     -1,
	})
}

// prepareCardForWrite performs common validation and card retrieval for write operations.
// It checks permissions, device availability, retrieves and validates the tag, and returns the Card.
func (r *NFCReader) prepareCardForWrite() (*Card, error) {
	// Check write permission
	r.statusMux.RLock()
	mode := r.mode
	r.statusMux.RUnlock()

	if mode == ModeReadOnly {
		return nil, fmt.Errorf("reader is in read-only mode, write operations are not allowed")
	}

	if !r.deviceManager.HasDevice() {
		return nil, fmt.Errorf("no NFC device connected")
	}

	r.statusMux.Lock()
	r.isWriting = true
	r.statusMux.Unlock()
	// Note: caller must defer the isWriting = false cleanup

	tags, err := r.GetTags()
	if err != nil {
		return nil, fmt.Errorf("failed to get tags for writing: %w", err)
	}

	if len(tags) == 0 {
		return nil, fmt.Errorf("no card detected for writing")
	}

	// Multi-card guard: require exactly one tag
	if len(tags) > 1 {
		return nil, fmt.Errorf("multiple cards detected (%d tags), please present only one card for writing", len(tags))
	}

	tag := tags[0] // Safe because we checked len(tags) == 1

	// Verify the single tag matches our cache (if cache has a card)
	currentPresentCardUID := r.cache.GetLastScanned()
	if currentPresentCardUID == "" {
		// Cache is empty (e.g., first write in write-only mode)
		log.Printf("Cache empty, using sole detected tag UID: %s", tag.UID())
		r.cache.UpdateLastSeenTime(tag.UID())
		r.cache.HasChanged(tag.UID())
	} else if currentPresentCardUID != tag.UID() {
		// Cache has a different card - unsafe to proceed
		return nil, fmt.Errorf("tag UID mismatch: cache has %s but detected tag is %s", currentPresentCardUID, tag.UID())
	}

	// Create Card wrapper for the tag
	card := NewCard(tag)
	return card, nil
}

// writeMessageToCard performs the actual write operation with NDEF message handling.
// Supports overwrite mode and partial update (append/replace at index).
func (r *NFCReader) writeMessageToCard(card *Card, msg *NDEFMessage, opts WriteOptions) error {
	log.Printf("writeMessageToCard (UID: %s, Type: %s): overwrite=%v, index=%d",
		card.UID, card.Type, opts.Overwrite, opts.Index)

	// Read current message to determine behavior
	cachedMsg, cardReadErr := card.ReadMessage()
	if cachedMsg == nil && cardReadErr == nil {
		log.Printf("writeMessageToCard (UID: %s): card does not have NDEF data, using overwrite", card.UID)
		opts.Overwrite = true
	}

	cachedNdef, isNDEF := cachedMsg.(*NDEFMessage)
	if !isNDEF || len(cachedNdef.Records()) == 0 {
		log.Printf("writeMessageToCard (UID: %s): card message is not NDEF, using overwrite", card.UID)
		opts.Overwrite = true
	}

	if opts.Overwrite {
		// Direct overwrite with provided message
		data, err := msg.Encode()
		if err != nil {
			return fmt.Errorf("writeMessageToCard (UID: %s): error encoding message: %w", card.UID, err)
		}

		// If tag supports AdvancedWriter interface and ForceInitialize is set, use it
		if opts.ForceInitialize {
			if advWriter, ok := card.tag.(AdvancedWriter); ok {
				tagOpts := TagWriteOptions{
					ForceInitialize: opts.ForceInitialize,
				}
				if err := advWriter.WriteDataWithOptions(data, tagOpts); err != nil {
					return fmt.Errorf("writeMessageToCard (UID: %s): error from WriteDataWithOptions: %w", card.UID, err)
				}
				log.Printf("writeMessageToCard (UID: %s): card write with ForceInitialize completed successfully.", card.UID)
				return nil
			}
			log.Printf("writeMessageToCard (UID: %s): ForceInitialize requested but tag doesn't support AdvancedWriter, using standard write", card.UID)
		}

		// Standard write path
		if err := card.WriteMessage(msg); err != nil {
			return fmt.Errorf("writeMessageToCard (UID: %s): error from card.WriteMessage: %w", card.UID, err)
		}

		log.Printf("writeMessageToCard (UID: %s): card write completed successfully.", card.UID)
		return nil
	}

	// Partial update mode: merge records from provided message into existing message
	log.Printf("writeMessageToCard (UID: %s): attempting NDEF partial update", card.UID)

	cachedMsgBuilder := cachedNdef.ToBuilder()
	newMsgBuilder := msg.ToBuilder()

	if opts.Index <= -1 || opts.Index >= len(cachedMsgBuilder.Records) {
		// Append mode: add all new records
		log.Printf("writeMessageToCard (UID: %s): appending %d new record(s)", card.UID, len(newMsgBuilder.Records))
		cachedMsgBuilder.Records = append(cachedMsgBuilder.Records, newMsgBuilder.Records...)
	} else {
		// Replace mode: replace record at index with first new record
		log.Printf("writeMessageToCard (UID: %s): replacing record at index %d", card.UID, opts.Index)
		if len(newMsgBuilder.Records) > 0 {
			cachedMsgBuilder.Records[opts.Index] = newMsgBuilder.Records[0]
		}
	}

	// Build and write updated message
	updatedMsg := cachedMsgBuilder.MustBuild()
	card.Reset()
	if err := card.WriteMessage(updatedMsg); err != nil {
		log.Printf("writeMessageToCard (UID: %s): NDEF partial write failed: %v", card.UID, err)
		return fmt.Errorf("writeMessageToCard (UID: %s): partial write failed: %w", card.UID, err)
	}

	log.Printf("writeMessageToCard (UID: %s): NDEF partial write succeeded", card.UID)
	return nil
}

// WriteMessageWithOptions writes an NDEF message to a detected NFC card with options for record manipulation.
func (r *NFCReader) WriteMessageWithOptions(msg *NDEFMessage, opts WriteOptions) error {
	return r.withTagOperation(func() error {
		card, err := r.prepareCardForWrite()
		if err != nil {
			return err
		}

		defer func() {
			r.statusMux.Lock()
			r.isWriting = false
			r.statusMux.Unlock()
		}()

		log.Printf("Attempting to write NDEF message to card UID: %s, Type: %s", card.UID, card.Type)
		if err := r.writeMessageToCard(card, msg, opts); err != nil {
			return fmt.Errorf("failed to write to card UID %s (Type: %s): %w", card.UID, card.Type, err)
		}

		log.Printf("Successfully wrote NDEF message to card UID: %s", card.UID)
		return nil
	})
}

// withTagOperation performs a protected tag operation with timeout.
func (r *NFCReader) withTagOperation(operation func() error) error {
	r.operationMutex.Lock()
	defer r.operationMutex.Unlock()

	done := make(chan error, 1)
	go func() {
		done <- operation()
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(r.operationTimeout):
		// Attempt to signal the operation to stop if possible (e.g. context cancellation)
		// For now, just return timeout. The operation might still be running.
		return fmt.Errorf("operation timed out after %v", r.operationTimeout)
	}
}

// GetTags retrieves available tags from the connected NFC device.
func (r *NFCReader) GetTags() ([]Tag, error) {
	dev := r.deviceManager.Device()
	if dev == nil {
		return nil, fmt.Errorf("getTags: no device connected or device is nil")
	}

	tags, err := dev.GetTags()
	if err != nil {
		return nil, fmt.Errorf("getTags: error from device.GetTags: %w", err)
	}
	return tags, nil
}

func (r *NFCReader) DevicePath() string {
	return r.deviceManager.DevicePath()
}
