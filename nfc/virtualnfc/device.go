package virtualnfc

import (
	"sync"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// Mode selects how a Device presents its field to an nfc.Supervisor. It is a
// read-out adapter choice, not two device implementations: the field is
// event-driven either way (Present/Remove are the events), and Mode only decides
// whether the Supervisor learns of a change by polling GetTags or by receiving a
// reported scan.
type Mode int

const (
	// PollMode advertises CanPoll: the Supervisor opens the device and polls
	// GetTags, which returns the current field snapshot. Stands in for a PC/SC
	// reader.
	PollMode Mode = iota

	// EventMode advertises SupportsEvents: the Supervisor never opens the device
	// and instead receives a ScannedTag through its Manager's TagReporter on
	// every field change. Stands in for a phone / push device.
	EventMode
)

// Device is an in-memory nfc.Device holding a field of Cards. Present and Remove
// change the field; in EventMode each change is reported through the Manager the
// device is plugged into. It implements the optional nfc capability interfaces so
// nfc.BuildDeviceCapabilities describes it correctly.
type Device struct {
	mu      sync.RWMutex
	id      string
	devType string
	mode    Mode
	cards   map[string]*Card
	order   []string // presentation order, for a stable GetTags
	closed  bool
	report  func(nfc.ScannedTag) // set by a Manager on Plug; nil when unplugged
}

// NewDevice builds a virtual device in the given mode. deviceType is what it
// reports as its DeviceType (e.g. "virtual-reader"); empty defaults to "virtual".
func NewDevice(id string, mode Mode, deviceType string) *Device {
	if deviceType == "" {
		deviceType = "virtual"
	}
	return &Device{
		id:      id,
		devType: deviceType,
		mode:    mode,
		cards:   make(map[string]*Card),
	}
}

// ID returns the device identifier.
func (d *Device) ID() string { return d.id }

// Mode returns the device's read-out mode.
func (d *Device) Mode() Mode { return d.mode }

// Present taps cards onto the device's field. A card whose UID is already
// present replaces the earlier one in place. In EventMode a plugged device
// reports a ScannedTag for each arrival.
func (d *Device) Present(cards ...*Card) {
	d.mu.Lock()
	arrived := make([]*Card, 0, len(cards))
	for _, c := range cards {
		if c == nil {
			continue
		}
		if _, exists := d.cards[c.uid]; !exists {
			d.order = append(d.order, c.uid)
		}
		d.cards[c.uid] = c
		arrived = append(arrived, c)
	}
	mode, report := d.mode, d.report
	d.mu.Unlock()

	if mode == EventMode && report != nil {
		for _, c := range arrived {
			report(nfc.ScannedTag{Tag: c.tag})
		}
	}
}

// Remove takes the card with the given UID off the field. In EventMode a plugged
// device reports a removal.
func (d *Device) Remove(uid string) {
	d.mu.Lock()
	_, existed := d.cards[uid]
	if existed {
		delete(d.cards, uid)
		for i, u := range d.order {
			if u == uid {
				d.order = append(d.order[:i], d.order[i+1:]...)
				break
			}
		}
	}
	mode, report := d.mode, d.report
	d.mu.Unlock()

	if existed && mode == EventMode && report != nil {
		report(nfc.ScannedTag{RemovedUID: uid})
	}
}

// setReport installs (or clears, with nil) the sink a Manager routes this
// device's EventMode scans to.
func (d *Device) setReport(fn func(nfc.ScannedTag)) {
	d.mu.Lock()
	d.report = fn
	d.mu.Unlock()
}

// --- nfc.Device ---

// Close marks the device closed; subsequent GetTags report it.
func (d *Device) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	return nil
}

// String returns the device identifier.
func (d *Device) String() string { return d.id }

// Connection returns the device's connection string.
func (d *Device) Connection() string { return "virtual:" + d.id }

// Transceive is unsupported at the device level: a virtual reader has no tag to
// address without one named. Raw exchange goes through the tag (RoutedTag).
func (d *Device) Transceive(txData []byte) ([]byte, error) {
	return nil, nfc.NewNotSupportedError("Transceive")
}

// GetTags returns the current field snapshot, in presentation order. This is the
// poll read-out; the Supervisor calls it for a PollMode device.
func (d *Device) GetTags() ([]nfc.Tag, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()
	if d.closed {
		return nil, nfc.ErrDeviceClosed
	}
	tags := make([]nfc.Tag, 0, len(d.order))
	for _, u := range d.order {
		tags = append(tags, d.cards[u].tag)
	}
	return tags, nil
}

// --- optional nfc capability interfaces ---

// DeviceType reports the device type (implements nfc.DeviceInfoProvider).
func (d *Device) DeviceType() string { return d.devType }

// SupportedTagTypes reports nothing specific (implements nfc.DeviceInfoProvider).
func (d *Device) SupportedTagTypes() []string { return nil }

// SupportsEvents reports whether the device is event-driven (implements
// nfc.DeviceEventEmitter). True gates it out of polling in nfc.ListReaders.
func (d *Device) SupportsEvents() bool { return d.mode == EventMode }

// SupportsTransceive reports device-level transceive, which a virtual reader
// does not do (implements nfc.DeviceTransceiver).
func (d *Device) SupportsTransceive() bool { return false }

var (
	_ nfc.Device             = (*Device)(nil)
	_ nfc.DeviceInfoProvider = (*Device)(nil)
	_ nfc.DeviceEventEmitter = (*Device)(nil)
	_ nfc.DeviceTransceiver  = (*Device)(nil)
)
