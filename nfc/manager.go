package nfc

// Manager handles NFC device discovery.
//
// Manager provides methods to list available NFC readers and open connections
// to devices.
//
// Example:
//
//	manager := pcsc.NewManager()
//	devices, _ := manager.ListDevices()
//	device, _ := manager.OpenDevice(devices[0])
//	tags, _ := device.GetTags()
type Manager interface {
	OpenDevice(deviceStr string) (Device, error)
	ListDevices() ([]string, error)
}

// DeviceChangeNotifier is optionally implemented by Managers that support
// notifying when devices are added or removed.
type DeviceChangeNotifier interface {
	// DeviceChanges returns a channel that signals when devices are added or removed.
	DeviceChanges() <-chan struct{}
}
