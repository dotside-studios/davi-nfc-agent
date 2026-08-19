package nfc

// Device represents an NFC reader/writer hardware device.
//
// A Device is obtained from a Manager and provides low-level access
// to NFC communication capabilities. Devices are returned ready-to-use
// from Manager.OpenDevice() - no additional initialization is required.
//
// Example:
//
//	manager := pcsc.NewManager()
//	device, err := manager.OpenDevice("")
//	defer device.Close()
type Device interface {
	Close() error
	String() string
	Connection() string
	Transceive(txData []byte) ([]byte, error)
	GetTags() ([]Tag, error)
}

// DeviceHealthChecker is an optional interface for devices that support
// health/connectivity checks. Use type assertion to check if a device
// implements this interface.
//
// Example:
//
//	if checker, ok := device.(DeviceHealthChecker); ok {
//	    if err := checker.IsHealthy(); err != nil {
//	        // Device is not responding, handle reconnection
//	    }
//	}
type DeviceHealthChecker interface {
	IsHealthy() error
}
