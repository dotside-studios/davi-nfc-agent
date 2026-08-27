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

	// Devices lists what this manager offers.
	Devices() ([]DeviceListing, error)
}

// DeviceListing describes a device before it is opened. An opened device
// reports its own capabilities through [GetDeviceCapabilities].
type DeviceListing struct {
	// Path names the device to OpenDevice. An aggregate qualifies it with the
	// manager holding it.
	Path string

	// ID is the identity the device holds with its driver: for a paired device,
	// the identity it paired with. Drivers with no identity of their own repeat
	// the path.
	ID string

	// Capabilities is what the driver knows without opening the device. CanPoll
	// gates opening it: a device that reports its own scans is never opened
	// here, and connecting to one cannot succeed.
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

// DeviceDisconnector is optionally implemented by a Manager holding live
// sessions it can end by identity.
//
// A credential is checked once, when a device connects, so revoking one does
// nothing to a device already connected: whatever owns the credentials ends the
// session here. A manager whose devices are polled through a reader holds no
// session and does not implement this.
type DeviceDisconnector interface {
	// DisconnectDevice ends the session held by deviceID, reporting whether
	// there was one. reason is what the device is told.
	DisconnectDevice(deviceID, reason string) bool
}

// Disconnect ends the session m holds for deviceID, reporting whether there was
// one. False for a manager that holds no sessions.
func Disconnect(m Manager, deviceID, reason string) bool {
	disconnector, ok := m.(DeviceDisconnector)
	if !ok {
		return false
	}
	return disconnector.DisconnectDevice(deviceID, reason)
}
