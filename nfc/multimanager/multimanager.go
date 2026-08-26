// Package multimanager provides a multi-manager that aggregates multiple NFC Manager implementations.
package multimanager

import (
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/dotside-studios/davi-nfc-agent/event"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// MultiManager aggregates multiple Manager implementations.
//
// The set of managers is fixed at construction. Nothing adds or removes one at
// runtime, so the map needs no lock and callers can hold a manager without
// worrying that it will be swapped underneath them.
type MultiManager struct {
	managers         map[string]nfc.Manager // managerName -> Manager instance
	managerOrder     []string               // Fallback order
	deviceChangeChan chan struct{}          // Aggregated device change channel
	stopForward      chan struct{}          // Stop channel forwarding
	closeOnce        sync.Once              // Close is idempotent

	// scans is every child's scans as one signal, so whatever consumes them
	// subscribes here rather than to each manager it happens to know about.
	// Raw, as the children report them: reading the tag is the supervisor's.
	scans event.Signal[nfc.ScannedTag]

	// listErrMu guards lastListErr, the last listing error reported per manager,
	// so a persistent one is logged once rather than on every poll. The tray,
	// the console and the device watcher all poll the list.
	listErrMu   sync.Mutex
	lastListErr map[string]string
}

// ManagerEntry represents a named manager for MultiManager initialization.
type ManagerEntry struct {
	Name    string
	Manager nfc.Manager
}

// NewMultiManager creates a new MultiManager with the given managers.
// Managers are tried in the order they are provided.
//
// Example:
//
//	mm := multimanager.NewMultiManager(
//	    multimanager.ManagerEntry{Name: "hardware", Manager: hardwareManager},
//	    multimanager.ManagerEntry{Name: "smartphone", Manager: smartphoneManager},
//	)
func NewMultiManager(entries ...ManagerEntry) *MultiManager {
	mm := &MultiManager{
		managers:         make(map[string]nfc.Manager),
		managerOrder:     []string{},
		deviceChangeChan: make(chan struct{}, 1),
		stopForward:      make(chan struct{}),
	}

	for _, entry := range entries {
		if entry.Name == "" || entry.Manager == nil {
			log.Printf("[multi] Skipping invalid manager entry: name=%s, manager=%v", entry.Name, entry.Manager)
			continue
		}

		if _, exists := mm.managers[entry.Name]; exists {
			log.Printf("[multi] Skipping duplicate manager: %s", entry.Name)
			continue
		}

		mm.managers[entry.Name] = entry.Manager
		mm.managerOrder = append(mm.managerOrder, entry.Name)
		log.Printf("[multi] Manager registered: %s", entry.Name)

		// Start forwarding device changes from child managers
		if notifier, ok := entry.Manager.(nfc.DeviceChangeNotifier); ok {
			go mm.forwardDeviceChanges(notifier.DeviceChanges())
		}

		// A child that reports scans reports them through here. No goroutine:
		// the child publishes on its own, and this passes it on.
		nfc.OnScan(entry.Manager, mm.scans.Emit)
	}

	return mm
}

// GetManager retrieves a specific manager by name.
func (mm *MultiManager) GetManager(name string) (nfc.Manager, bool) {
	manager, exists := mm.managers[name]
	return manager, exists
}

// OpenDevice opens a device using the appropriate manager.
// Device string format:
//   - "manager:deviceID" - explicit manager (e.g., "smartphone:abc123", "hardware:pn532")
//   - "deviceID" or "" - try all managers in order
func (mm *MultiManager) OpenDevice(deviceStr string) (nfc.Device, error) {
	managers := mm.managers
	order := mm.managerOrder

	if len(managers) == 0 {
		return nil, fmt.Errorf("no managers registered")
	}

	// Check if device string has manager prefix
	// Format: "managerName:deviceID" where managerName must be a registered manager
	parts := strings.SplitN(deviceStr, ":", 2)
	if len(parts) == 2 {
		managerName := parts[0]
		deviceID := parts[1]

		// Only treat as manager prefix if it's actually a registered manager
		// This handles cases where device IDs contain colons (e.g., "acr122_usb:001:003")
		if manager, exists := managers[managerName]; exists {
			device, err := manager.OpenDevice(deviceID)
			if err != nil {
				return nil, fmt.Errorf("failed to open device '%s' with manager '%s': %w", deviceID, managerName, err)
			}
			return device, nil
		}
		// Not a registered manager name - fall through to try all managers
	}

	// No prefix or empty string - try all managers in order
	var lastErr error
	for _, name := range order {
		manager := managers[name]
		device, err := manager.OpenDevice(deviceStr)
		if err == nil {
			// Success
			return device, nil
		}
		lastErr = err
	}

	// All managers failed
	if lastErr != nil {
		return nil, fmt.Errorf("all managers failed to open device '%s': %w", deviceStr, lastErr)
	}

	return nil, fmt.Errorf("no device found: %s", deviceStr)
}

// Devices aggregates what every manager offers, prefixing each path with the
// manager it came from and carrying the child's description through unchanged.
//
// A manager that cannot list is logged and left out: one unavailable backend
// must not hide the others.
func (mm *MultiManager) Devices() ([]nfc.DeviceListing, error) {
	var all []nfc.DeviceListing

	// In registration order: map order would vary between runs.
	for _, name := range mm.managerOrder {
		listings, err := mm.managers[name].Devices()
		if err != nil {
			mm.logListError(name, err)
			continue
		}
		mm.clearListError(name)

		for _, listing := range listings {
			listing.Path = qualify(name, listing.Path)
			all = append(all, listing)
		}
	}

	return all, nil
}

// qualify prefixes a path with the manager holding it, leaving an already
// qualified one alone.
func qualify(manager, path string) string {
	if strings.Contains(path, ":") {
		return path
	}
	return fmt.Sprintf("%s:%s", manager, path)
}

// Close stops forwarding and closes every manager that can be closed.
//
// The test is a bare Close method rather than an interface from the server
// package. Requiring a server-side interface meant no manager ever matched, so
// this propagated to nothing.
func (mm *MultiManager) Close() {
	mm.closeOnce.Do(func() {
		close(mm.stopForward)

		for name, manager := range mm.managers {
			if closer, ok := manager.(interface{ Close() }); ok {
				log.Printf("[multi] Closing manager: %s", name)
				closer.Close()
			}
		}
	})
}

// TagOn reports the tag a device is holding, asking the child managers whose
// devices hold their own. An empty deviceID asks each in turn for its most
// recent scan, so a build with one such manager answers exactly as it would.
func (mm *MultiManager) TagOn(deviceID string) (string, string, bool) {
	for _, holder := range mm.holders() {
		if holding, uid, ok := holder.TagOn(deviceID); ok {
			return holding, uid, true
		}
	}
	return "", "", false
}

// DevicesHoldingTags lists every device holding a tag, across the managers that
// have any.
func (mm *MultiManager) DevicesHoldingTags() []string {
	var out []string
	for _, holder := range mm.holders() {
		out = append(out, holder.DevicesHoldingTags()...)
	}
	return out
}

// WriteTag asks the manager whose device is holding the tag to encode onto it.
func (mm *MultiManager) WriteTag(deviceID, tagUID string, msg *nfc.NDEFMessage, lock bool, idempotencyKey string) (*nfc.WriteResult, error) {
	holder, err := mm.holderFor(deviceID)
	if err != nil {
		return nil, err
	}
	return holder.WriteTag(deviceID, tagUID, msg, lock, idempotencyKey)
}

// LockTag asks the manager whose device is holding the tag to make it
// permanently read-only.
func (mm *MultiManager) LockTag(deviceID, tagUID, idempotencyKey string) (*nfc.LockResult, error) {
	holder, err := mm.holderFor(deviceID)
	if err != nil {
		return nil, err
	}
	return holder.LockTag(deviceID, tagUID, idempotencyKey)
}

// TagCapabilities reports what the tag a device is holding supports.
func (mm *MultiManager) TagCapabilities(deviceID, tagUID string) (*nfc.TagCapabilities, error) {
	holder, err := mm.holderFor(deviceID)
	if err != nil {
		return nil, err
	}
	return holder.TagCapabilities(deviceID, tagUID)
}

// TransceiveTag asks the manager whose device is holding the tag to exchange
// raw bytes with it.
func (mm *MultiManager) TransceiveTag(deviceID, tagUID string, data []byte, raw bool) ([]byte, error) {
	holder, err := mm.holderFor(deviceID)
	if err != nil {
		return nil, err
	}
	return holder.TransceiveTag(deviceID, tagUID, data, raw)
}

// holders lists the child managers whose devices hold tags, in registration
// order.
func (mm *MultiManager) holders() []nfc.TagHolder {
	var out []nfc.TagHolder
	for _, name := range mm.managerOrder {
		if holder := nfc.TagsHeldBy(mm.managers[name]); holder != nil {
			out = append(out, holder)
		}
	}
	return out
}

// holderFor picks the manager an operation goes to. A single one takes every
// operation, so its own refusal is what the caller sees; with more than one the
// device holding the tag decides.
func (mm *MultiManager) holderFor(deviceID string) (nfc.TagHolder, error) {
	holders := mm.holders()
	switch len(holders) {
	case 0:
		return nil, fmt.Errorf("no manager holds tags for device %s", deviceID)
	case 1:
		return holders[0], nil
	}

	for _, holder := range holders {
		if _, _, ok := holder.TagOn(deviceID); ok {
			return holder, nil
		}
	}
	return nil, fmt.Errorf("no device %s is holding a tag", deviceID)
}

// Scans carries what every child manager's devices report, as reported.
func (mm *MultiManager) Scans() *event.Signal[nfc.ScannedTag] { return &mm.scans }

// DeviceChanges returns a channel that signals when devices are registered or unregistered
// in any of the child managers.
func (mm *MultiManager) DeviceChanges() <-chan struct{} {
	return mm.deviceChangeChan
}

// forwardDeviceChanges forwards device change events from a child manager to the aggregated channel.
func (mm *MultiManager) forwardDeviceChanges(ch <-chan struct{}) {
	for {
		select {
		case <-mm.stopForward:
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
			// Forward to aggregated channel
			select {
			case mm.deviceChangeChan <- struct{}{}:
			default:
				// Channel full, skip
			}
		}
	}
}

// logListError reports a manager's listing failure the first time it is
// seen, and again only if the reason changes. Returns whether it logged.
func (mm *MultiManager) logListError(name string, err error) bool {
	reason := err.Error()

	mm.listErrMu.Lock()
	if mm.lastListErr == nil {
		mm.lastListErr = make(map[string]string)
	}
	repeat := mm.lastListErr[name] == reason
	mm.lastListErr[name] = reason
	mm.listErrMu.Unlock()

	if repeat {
		return false
	}
	log.Printf("[multi] manager '%s' failed to list devices: %v", name, err)
	return true
}

// clearListError notes that a manager is listing again, so the next failure is
// reported afresh. A recovery is worth a line of its own: without one the log
// shows a reader failing and never coming back.
func (mm *MultiManager) clearListError(name string) {
	mm.listErrMu.Lock()
	_, wasFailing := mm.lastListErr[name]
	delete(mm.lastListErr, name)
	mm.listErrMu.Unlock()

	if wasFailing {
		log.Printf("[multi] manager '%s' is listing devices again", name)
	}
}
