package nfc

import (
	"bytes"
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

// Write reliability defaults.
const (
	// DefaultMaxWriteAttempts is the number of write+verify attempts made on a
	// single write operation before giving up, when WriteOptions.MaxWriteAttempts
	// is not set.
	DefaultMaxWriteAttempts = 3
	// WriteRetryBackoff is the base delay between write retries. The delay grows
	// linearly with the attempt number (backoff, 2*backoff, ...).
	WriteRetryBackoff = 50 * time.Millisecond
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

	// SkipVerify disables read-after-write verification. By default (false), the
	// reader re-reads the card after writing and confirms the data matches what
	// was written, retrying on mismatch.
	SkipVerify bool

	// MaxWriteAttempts caps the number of write+verify attempts on transient
	// failures (write error, verification mismatch, or transient read error).
	// If <= 0, DefaultMaxWriteAttempts is used. Permanent failures such as card
	// removal, read-only tags, and capacity overflow are never retried.
	MaxWriteAttempts int

	// SkipCapacityCheck disables the pre-flight check that the encoded NDEF
	// message fits within the tag's reported NDEF capacity.
	SkipCapacityCheck bool

	// Lock, when true, makes the tag permanently read-only after a successful
	// verified write. Only tags that support locking (e.g. NTAG, Ultralight)
	// honor this; others return an error. WARNING: locking is irreversible.
	Lock bool
}

// WriteResult describes the outcome of a successful write operation. It gives
// callers (and ultimately the frontend) the same confidence for writes that the
// read path already provides: confirmation that the bytes actually landed.
type WriteResult struct {
	// UID of the tag that was written.
	UID string `json:"uid"`
	// TagType is the human-readable tag type string.
	TagType string `json:"tagType"`
	// BytesWritten is the size of the encoded NDEF message written to the tag.
	BytesWritten int `json:"bytesWritten"`
	// Verified is true when the write was confirmed by reading the data back and
	// comparing it to what was written.
	Verified bool `json:"verified"`
	// Attempts is the number of write attempts made before success.
	Attempts int `json:"attempts"`
	// Locked is true when the tag was made permanently read-only as part of the
	// write (see WriteOptions.Lock).
	Locked bool `json:"locked,omitempty"`
}

// LockResult describes the outcome of a make-read-only (lock) operation.
type LockResult struct {
	// UID of the tag that was locked.
	UID string `json:"uid"`
	// TagType is the human-readable tag type string.
	TagType string `json:"tagType"`
	// Locked is true when the tag was made permanently read-only.
	Locked bool `json:"locked"`
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

// writeMessageToCard performs the actual write operation with NDEF message
// handling, a pre-flight capacity check, bounded retries, and read-after-write
// verification. Supports overwrite mode and partial update (append/replace at
// index). It returns a WriteResult describing the outcome on success.
func (r *NFCReader) writeMessageToCard(card *Card, msg *NDEFMessage, opts WriteOptions) (*WriteResult, error) {
	log.Printf("writeMessageToCard (UID: %s, Type: %s): overwrite=%v, index=%d",
		card.UID, card.Type, opts.Overwrite, opts.Index)

	// Resolve the final NDEF message to write (overwrite vs partial merge).
	finalMsg, err := r.resolveWriteMessage(card, msg, opts)
	if err != nil {
		return nil, err
	}

	// Encode once: this is the exact NDEF payload we expect to read back.
	data, err := finalMsg.Encode()
	if err != nil {
		return nil, fmt.Errorf("writeMessageToCard (UID: %s): error encoding message: %w", card.UID, err)
	}

	// Pre-flight capacity check against the tag's reported NDEF capacity.
	if !opts.SkipCapacityCheck {
		if err := checkWriteCapacity(card.tag, len(data)); err != nil {
			return nil, err
		}
	}

	attempts := opts.MaxWriteAttempts
	if attempts <= 0 {
		attempts = DefaultMaxWriteAttempts
	}
	verify := !opts.SkipVerify

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			log.Printf("writeMessageToCard (UID: %s): retry attempt %d/%d (last error: %v)",
				card.UID, attempt, attempts, lastErr)
			r.clock.Sleep(time.Duration(attempt-1) * WriteRetryBackoff)
		}

		// Perform the write.
		if err := r.writeOnce(card, data, opts); err != nil {
			if isPermanentWriteError(err) {
				return nil, err
			}
			lastErr = err
			continue
		}

		// If verification is disabled, treat a clean write as success.
		if !verify {
			log.Printf("writeMessageToCard (UID: %s): write completed (unverified, attempt %d)", card.UID, attempt)
			return newWriteResult(card, len(data), false, attempt), nil
		}

		// Read back and confirm the data landed.
		verified, err := r.verifyWrite(card, data)
		if err != nil {
			if isPermanentWriteError(err) {
				return nil, err
			}
			lastErr = fmt.Errorf("verification read failed: %w", err)
			continue
		}
		if verified {
			log.Printf("writeMessageToCard (UID: %s): write verified successfully (attempt %d)", card.UID, attempt)
			return newWriteResult(card, len(data), true, attempt), nil
		}
		lastErr = fmt.Errorf("write verification mismatch: data read back does not match data written")
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("unknown write failure")
	}
	return nil, NewWriteError(fmt.Sprintf("writeMessageToCard (UID: %s)", card.UID),
		fmt.Errorf("failed after %d attempt(s): %w", attempts, lastErr))
}

// resolveWriteMessage determines the final NDEF message to write, applying
// overwrite vs partial-update (append/replace) semantics against the card's
// current contents. A card with no usable NDEF content is always overwritten.
func (r *NFCReader) resolveWriteMessage(card *Card, msg *NDEFMessage, opts WriteOptions) (*NDEFMessage, error) {
	cachedMsg, _ := card.ReadMessage()
	cachedNdef, isNDEF := cachedMsg.(*NDEFMessage)

	if !isNDEF || len(cachedNdef.Records()) == 0 {
		// Card is blank, non-NDEF, or unreadable: a full overwrite is the only
		// safe option.
		if !opts.Overwrite {
			log.Printf("resolveWriteMessage (UID: %s): card has no usable NDEF data, forcing overwrite", card.UID)
		}
		opts.Overwrite = true
	}

	if opts.Overwrite {
		return msg, nil
	}

	// Partial update: merge new records into the existing message.
	log.Printf("resolveWriteMessage (UID: %s): merging records for partial update", card.UID)
	cachedBuilder := cachedNdef.ToBuilder()
	newBuilder := msg.ToBuilder()

	if opts.Index <= -1 || opts.Index >= len(cachedBuilder.Records) {
		log.Printf("resolveWriteMessage (UID: %s): appending %d new record(s)", card.UID, len(newBuilder.Records))
		cachedBuilder.Records = append(cachedBuilder.Records, newBuilder.Records...)
	} else {
		log.Printf("resolveWriteMessage (UID: %s): replacing record at index %d", card.UID, opts.Index)
		if len(newBuilder.Records) > 0 {
			cachedBuilder.Records[opts.Index] = newBuilder.Records[0]
		}
	}

	updated, err := cachedBuilder.Build()
	if err != nil {
		return nil, fmt.Errorf("resolveWriteMessage (UID: %s): failed to build merged message: %w", card.UID, err)
	}
	return updated, nil
}

// writeOnce performs a single write of the encoded NDEF data to the tag. It
// writes the bytes directly (rather than through the Card's buffer) so that a
// failed attempt leaves no partial buffer behind for the next retry.
func (r *NFCReader) writeOnce(card *Card, data []byte, opts WriteOptions) error {
	// Clear any cached read state so the verification read hits the tag.
	card.Reset()

	if opts.ForceInitialize {
		if advWriter, ok := card.tag.(AdvancedWriter); ok {
			return advWriter.WriteDataWithOptions(data, TagWriteOptions{ForceInitialize: true})
		}
		log.Printf("writeOnce (UID: %s): ForceInitialize requested but tag doesn't support AdvancedWriter, using standard write", card.UID)
	}

	card.LastAccessed = time.Now()
	return card.tag.WriteData(data)
}

// verifyWrite re-reads the card and reports whether its NDEF contents exactly
// match the bytes that were written.
func (r *NFCReader) verifyWrite(card *Card, expected []byte) (bool, error) {
	card.Reset()
	readBack, err := card.tag.ReadData()
	if err != nil {
		return false, err
	}
	return bytes.Equal(readBack, expected), nil
}

// checkWriteCapacity verifies the encoded NDEF message fits within the tag's
// reported NDEF capacity. Tags that don't report a capacity (MaxNDEFSize == 0)
// are not checked here; their WriteData implementation enforces hard limits.
func checkWriteCapacity(tag Tag, ndefLen int) error {
	caps := GetTagCapabilities(tag)
	if caps.MaxNDEFSize > 0 && ndefLen > caps.MaxNDEFSize {
		return NewCapacityExceededError("WriteMessage", tag.UID(), ndefLen, caps.MaxNDEFSize)
	}
	return nil
}

// isPermanentWriteError reports whether an error should abort the write
// immediately rather than be retried.
func isPermanentWriteError(err error) bool {
	return IsCardRemovedError(err) ||
		IsReadOnlyError(err) ||
		IsCapacityExceededError(err) ||
		IsNotSupportedError(err)
}

// newWriteResult builds a WriteResult from a card and write metadata.
func newWriteResult(card *Card, bytesWritten int, verified bool, attempts int) *WriteResult {
	return &WriteResult{
		UID:          card.UID,
		TagType:      card.Type,
		BytesWritten: bytesWritten,
		Verified:     verified,
		Attempts:     attempts,
	}
}

// WriteMessageWithOptions writes an NDEF message to a detected NFC card with
// options for record manipulation. It performs a pre-flight capacity check,
// retries on transient failures, and (unless disabled) verifies the write by
// reading the data back. Use WriteMessageWithResult to obtain the WriteResult.
func (r *NFCReader) WriteMessageWithOptions(msg *NDEFMessage, opts WriteOptions) error {
	_, err := r.WriteMessageWithResult(msg, opts)
	return err
}

// WriteMessageWithResult is like WriteMessageWithOptions but returns a
// WriteResult describing the outcome (verification status, attempts, and bytes
// written) so callers can surface real write confidence to the user.
func (r *NFCReader) WriteMessageWithResult(msg *NDEFMessage, opts WriteOptions) (*WriteResult, error) {
	var result *WriteResult
	err := r.withTagOperation(func() error {
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
		res, err := r.writeMessageToCard(card, msg, opts)
		if err != nil {
			return fmt.Errorf("failed to write to card UID %s (Type: %s): %w", card.UID, card.Type, err)
		}

		result = res

		// Optionally make the tag read-only after a successful write.
		if opts.Lock {
			if _, lockErr := r.lockCard(card); lockErr != nil {
				return fmt.Errorf("write to card UID %s succeeded but lock failed: %w", card.UID, lockErr)
			}
			result.Locked = true
		}

		log.Printf("Successfully wrote NDEF message to card UID: %s (verified=%v, attempts=%d, locked=%v)",
			card.UID, res.Verified, res.Attempts, result.Locked)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// LockCard makes the currently presented tag permanently read-only. This is
// irreversible. Only tags that support locking (e.g. NTAG, Ultralight) succeed;
// others return a not-supported error.
func (r *NFCReader) LockCard() (*LockResult, error) {
	var result *LockResult
	err := r.withTagOperation(func() error {
		card, err := r.prepareCardForWrite()
		if err != nil {
			return err
		}

		defer func() {
			r.statusMux.Lock()
			r.isWriting = false
			r.statusMux.Unlock()
		}()

		res, err := r.lockCard(card)
		if err != nil {
			return fmt.Errorf("failed to lock card UID %s (Type: %s): %w", card.UID, card.Type, err)
		}
		result = res
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// lockCard makes the given card's tag read-only. Card-removal and not-supported
// errors are surfaced directly so callers can react appropriately.
func (r *NFCReader) lockCard(card *Card) (*LockResult, error) {
	locker, ok := card.tag.(TagLocker)
	if !ok {
		return nil, NewNotSupportedError("MakeReadOnly")
	}

	if err := locker.MakeReadOnly(); err != nil {
		if IsCardRemovedError(err) || IsNotSupportedError(err) {
			return nil, err
		}
		return nil, NewWriteError(fmt.Sprintf("lockCard (UID: %s)", card.UID), err)
	}

	log.Printf("lockCard (UID: %s, Type: %s): tag locked read-only", card.UID, card.Type)
	return &LockResult{UID: card.UID, TagType: card.Type, Locked: true}, nil
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
