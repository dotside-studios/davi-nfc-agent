package nfc

import "github.com/dotside-studios/davi-nfc-agent/event"

// Manager handles NFC device discovery.
//
// Manager provides methods to list available NFC readers and open connections
// to devices.
//
// Example:
//
//	manager := pcsc.NewManager()
//	devices, _ := manager.Devices()
//	device, _ := manager.OpenDevice(devices[0])
//	tags, _ := device.GetTags()
type Manager interface {
	OpenDevice(deviceStr string) (Device, error)

	// Devices lists what this manager knows about, with what is known of each
	// before anything opens it.
	Devices() ([]DeviceListing, error)
}

// DeviceListing is a device a manager offers, described before it is opened.
//
// A driver's transport settles most of what a device can do, so a listing
// carries what the driver knows without touching it. A device that declares
// more when it connects refines it; what an opened device reports is its own,
// through [GetDeviceCapabilities].
type DeviceListing struct {
	// Path names the device to OpenDevice.
	Path string

	// Capabilities is what the driver knows the device can do. CanPoll is the
	// one a consumer must respect: a device that reports its own scans is not
	// opened and read from here, and opening one produces a connection that
	// can never succeed.
	Capabilities DeviceCapabilities
}

// DevicePaths lists what a manager offers, by path.
func DevicePaths(m Manager) ([]string, error) {
	if m == nil {
		return nil, nil
	}

	listings, err := m.Devices()
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(listings))
	for _, listing := range listings {
		paths = append(paths, listing.Path)
	}
	return paths, nil
}

// DeviceChangeNotifier is optionally implemented by Managers that support
// notifying when devices are added or removed.
type DeviceChangeNotifier interface {
	// DeviceChanges returns a channel that signals when devices are added or removed.
	DeviceChanges() <-chan struct{}
}

// TagReporter is optionally implemented by a Manager whose devices report their
// own scans, rather than being polled through a reader this machine opened.
//
// What it publishes is raw: the tag as the device reported it, before anything
// has been read off it. Making that into an [NFCData] is the supervisor's, so
// every scan is processed in one place however it arrived.
//
// The manager owns the signal, so it reports whether or not anything is
// listening, and more than one thing can listen.
type TagReporter interface {
	// Scans carries every tag the manager's devices report, as reported.
	Scans() *event.Signal[ScannedTag]
}

// OnScan registers fn for the tags a manager's devices report, and returns the
// handle that removes it again. Nil for a manager that reports none, which is
// every manager whose devices are polled through a reader.
func OnScan(m Manager, fn func(ScannedTag)) *event.Connection {
	reporter, ok := m.(TagReporter)
	if !ok {
		return nil
	}
	return reporter.Scans().Connect(fn)
}
