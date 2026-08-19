// Package multimanager provides a multi-manager that aggregates multiple NFC Manager implementations.
package multimanager

import (
	"fmt"
	"log"
	"strings"
	"sync"

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

	// listErrMu guards lastListErr, which holds the last ListDevices error
	// reported per manager so a persistent one is logged once rather than on
	// every poll. ListDevices is polled continuously by the tray, the console
	// and the device watcher, so an unavailable reader would otherwise be the
	// only thing in the log.
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
	}

	return mm
}

// GetManager retrieves a specific manager by name.
func (mm *MultiManager) GetManager(name string) (nfc.Manager, bool) {
	manager, exists := mm.managers[name]
	return manager, exists
}

// RemoteDevice reports whether a device path names one held by a manager whose
// devices are remote, meaning a phone rather than a reader attached to this
// machine. It answers from the path's manager prefix, so a device that is not
// currently connected is still recognized for what it is.
func (mm *MultiManager) RemoteDevice(devicePath string) bool {
	name, _, found := strings.Cut(devicePath, ":")
	if !found {
		return false
	}

	manager, exists := mm.managers[name]
	if !exists {
		return false
	}

	remote, ok := manager.(nfc.RemoteManager)
	return ok && remote.RemoteDevices()
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

// ListDevices aggregates device lists from all managers.
// Each device is prefixed with its manager name for disambiguation.
// Errors from individual managers are logged but do not fail the overall operation.
func (mm *MultiManager) ListDevices() ([]string, error) {
	return mm.list(false)
}

// ListReaders returns only the devices that can serve as this agent's reader,
// leaving out the ones held by a manager whose devices are remote. A phone
// reports its scans over the device bridge and is never opened as a reader, so
// listing one as a candidate offers a choice that can only fail.
func (mm *MultiManager) ListReaders() ([]string, error) {
	return mm.list(true)
}

func (mm *MultiManager) list(readersOnly bool) ([]string, error) {
	var allDevices []string

	// In registration order. The result picks the agent's reader when none is
	// pinned, so map order would let that vary between runs.
	for _, name := range mm.managerOrder {
		manager := mm.managers[name]

		if readersOnly {
			if remote, ok := manager.(nfc.RemoteManager); ok && remote.RemoteDevices() {
				continue
			}
		}

		devices, err := manager.ListDevices()
		if err != nil {
			mm.logListError(name, err)
			continue
		}
		mm.clearListError(name)

		// Prepend manager name to each device (if not already prefixed)
		for _, device := range devices {
			if !strings.Contains(device, ":") {
				// No prefix - add manager name
				allDevices = append(allDevices, fmt.Sprintf("%s:%s", name, device))
			} else {
				// Already prefixed (or contains colon) - keep as-is
				allDevices = append(allDevices, device)
			}
		}
	}

	return allDevices, nil
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

// logListError reports a manager's ListDevices failure the first time it is
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
