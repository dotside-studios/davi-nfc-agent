package virtualnfc

import (
	"fmt"
	"sort"
	"sync"

	"github.com/dotside-studios/davi-nfc-agent/event"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// Manager is an nfc.Manager over a set of virtual Devices keyed by path. It
// notifies on Plug/Unplug (nfc.DeviceChangeNotifier) and publishes EventMode
// scans on one signal (nfc.TagReporter), so a single Manager can host both
// polled and event devices: the Supervisor opens the polled ones and subscribes
// to the signal for the rest.
type Manager struct {
	mu      sync.RWMutex
	devices map[string]*Device
	changes chan struct{}
	scans   event.Signal[nfc.ScannedTag]
}

// NewManager builds an empty manager.
func NewManager() *Manager {
	return &Manager{
		devices: make(map[string]*Device),
		changes: make(chan struct{}, 1),
	}
}

// Plug registers a device at path and, for an EventMode device, wires its scans
// onto the manager's signal tagged with that path. Plugging a path that already
// holds a device replaces it.
func (m *Manager) Plug(path string, d *Device) {
	m.mu.Lock()
	if prev, ok := m.devices[path]; ok {
		prev.setReport(nil)
	}
	m.devices[path] = d
	m.mu.Unlock()

	d.setReport(func(s nfc.ScannedTag) {
		s.Device = path
		m.scans.Emit(s)
	})
	m.notify()
}

// Unplug removes the device at path, if any, and stops its reporting.
func (m *Manager) Unplug(path string) {
	m.mu.Lock()
	d, ok := m.devices[path]
	if ok {
		delete(m.devices, path)
	}
	m.mu.Unlock()

	if ok {
		d.setReport(nil)
		m.notify()
	}
}

// Present taps cards onto the device at path.
func (m *Manager) Present(path string, cards ...*Card) error {
	d, ok := m.device(path)
	if !ok {
		return fmt.Errorf("virtualnfc: no device %q", path)
	}
	d.Present(cards...)
	return nil
}

// Remove takes the card with the given UID off the device at path.
func (m *Manager) Remove(path, uid string) error {
	d, ok := m.device(path)
	if !ok {
		return fmt.Errorf("virtualnfc: no device %q", path)
	}
	d.Remove(uid)
	return nil
}

func (m *Manager) device(path string) (*Device, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	d, ok := m.devices[path]
	return d, ok
}

// --- nfc.Manager ---

// OpenDevice returns the device registered at path.
func (m *Manager) OpenDevice(path string) (nfc.Device, error) {
	d, ok := m.device(path)
	if !ok {
		return nil, fmt.Errorf("virtualnfc: no device %q", path)
	}
	return d, nil
}

// Devices lists every plugged device in a stable order, each with the
// capabilities its mode implies.
func (m *Manager) Devices() ([]nfc.DeviceListing, error) {
	m.mu.RLock()
	paths := make([]string, 0, len(m.devices))
	for path := range m.devices {
		paths = append(paths, path)
	}
	byPath := make(map[string]*Device, len(m.devices))
	for path, d := range m.devices {
		byPath[path] = d
	}
	m.mu.RUnlock()

	sort.Strings(paths)
	out := make([]nfc.DeviceListing, 0, len(paths))
	for _, path := range paths {
		out = append(out, nfc.DeviceListing{
			Path:         path,
			ID:           path,
			Capabilities: nfc.BuildDeviceCapabilities(byPath[path]),
		})
	}
	return out, nil
}

// DeviceChanges signals when a device is plugged or unplugged (implements
// nfc.DeviceChangeNotifier).
func (m *Manager) DeviceChanges() <-chan struct{} { return m.changes }

// Scans carries every EventMode device's reported tags (implements
// nfc.TagReporter).
func (m *Manager) Scans() *event.Signal[nfc.ScannedTag] { return &m.scans }

func (m *Manager) notify() {
	select {
	case m.changes <- struct{}{}:
	default:
	}
}

var (
	_ nfc.Manager              = (*Manager)(nil)
	_ nfc.DeviceChangeNotifier = (*Manager)(nil)
	_ nfc.TagReporter          = (*Manager)(nil)
)
