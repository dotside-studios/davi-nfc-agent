# Extending NFC Support

This guide explains how to add support for new NFC readers or tag types to the davi-nfc-agent.

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│                      MultiManager                           │
│     Aggregates multiple managers, routes device requests    │
├──────────────────┬──────────────────┬───────────────────────┤
│  HardwareManager │ remotenfc.Manager│   YourManager         │
│  (PC/SC readers) │ (WebNFC/mobile)  │   (custom)            │
└────────┬─────────┴────────┬─────────┴──────────┬────────────┘
         │                  │                    │
         ▼                  ▼                    ▼
      Device             Device               Device
         │                  │                    │
         ▼                  ▼                    ▼
       Tag[]              Tag[]               Tag[]
```

### Core Interfaces

| Interface | Purpose |
|-----------|---------|
| `Manager` | Device discovery and connection |
| `Device` | Hardware communication |
| `Tag` | Tag operations (read/write/transceive) |

## Adding a New Device Type

### Step 1: Implement the Manager Interface

```go
package myreader

import "github.com/dotside-studios/davi-nfc-agent/nfc"

type MyManager struct {
    // Your connection state (USB, serial, network, etc.)
}

func NewManager() *MyManager {
    return &MyManager{}
}

// ListDevices returns available device identifiers
func (m *MyManager) ListDevices() ([]string, error) {
    // Enumerate connected devices
    // Return identifiers like "myreader:usb:001" or "myreader:192.168.1.100"
    return []string{"myreader:default"}, nil
}

// OpenDevice opens a device by its identifier
// The device should be fully initialized and ready to use when returned
func (m *MyManager) OpenDevice(deviceStr string) (nfc.Device, error) {
    // Parse deviceStr and connect to the hardware
    device := &MyDevice{
        connection: deviceStr,
    }

    // Perform any device-specific initialization here
    // The returned device should be ready to use immediately

    return device, nil
}
```

### Step 2: Implement the Device Interface

```go
type MyDevice struct {
    connection string
    // Your hardware handle (serial port, USB handle, socket, etc.)
}

func (d *MyDevice) Close() error {
    // Clean up resources
    return nil
}

func (d *MyDevice) String() string {
    return "My NFC Reader"
}

func (d *MyDevice) Connection() string {
    return d.connection
}

// DeviceType returns the device type identifier (implements DeviceInfoProvider)
func (d *MyDevice) DeviceType() string {
    return "myreader"
}

// SupportedTagTypes returns supported tag types (implements DeviceInfoProvider)
func (d *MyDevice) SupportedTagTypes() []string {
    return []string{"MIFARE Classic", "NTAG"}
}

func (d *MyDevice) Transceive(txData []byte) ([]byte, error) {
    // Send raw bytes to the reader and return response
    // This is for device-level commands, not tag communication
    return nil, nfc.NewNotSupportedError("Transceive")
}

func (d *MyDevice) GetTags() ([]nfc.Tag, error) {
    // Poll for tags on the reader
    // Return detected tags

    // Example: detect a tag and wrap it
    tagUID := "04A1B2C3D4E5F6"
    tagType := "MIFARE Classic 1K"

    tag := &MyTag{
        uid:     tagUID,
        tagType: tagType,
        device:  d,
    }

    return []nfc.Tag{tag}, nil
}
```

### Step 3: Implement the Tag Interface

The `Tag` interface bundles several role interfaces (identity, connection, read,
write, transceive, lock). Rather than implement all of them, **embed
`nfc.BaseTag`** and override only the methods your tag actually supports. The
base provides safe defaults: connection is a no-op, and write/transceive/lock
operations report "not supported".

You only ever need to implement the four methods that have no sensible default:
`UID()`, `Type()`, `NumericType()`, and `ReadData()`.

```go
type MyTag struct {
    nfc.BaseTag // supplies Connect/Disconnect/WriteData/Transceive/IsWritable/CanMakeReadOnly/MakeReadOnly

    uid     string
    tagType string
    device  *MyDevice
}

// --- TagIdentifier (required) ---

func (t *MyTag) UID() string {
    return t.uid
}

func (t *MyTag) Type() string {
    return t.tagType
}

func (t *MyTag) NumericType() int {
    return 0 // Your type code
}

// --- TagReader (required) ---

func (t *MyTag) ReadData() ([]byte, error) {
    // Read NDEF data from the tag and return raw NDEF bytes.
    return nil, nil
}

// --- TagCapabilityProvider (optional but recommended) ---

func (t *MyTag) Capabilities() nfc.TagCapabilities {
    return nfc.TagCapabilities{
        CanRead:       true,
        CanWrite:      true,
        CanTransceive: false,
        CanLock:       false,
        TagFamily:     "MIFARE Classic",
        Technology:    "ISO14443A",
        MemorySize:    1024,
        SupportsNDEF:  true,
    }
}

// --- Override only what you support ---

// If your tag is writable, override WriteData (otherwise it inherits the
// BaseTag default that returns a NotSupported error):
func (t *MyTag) WriteData(data []byte) error {
    // Write NDEF data to the tag.
    return nil
}

// Connect/Disconnect/Transceive/IsWritable/CanMakeReadOnly/MakeReadOnly are all
// inherited from nfc.BaseTag. Override any of them the same way if your tag
// supports them.
```

> Keep `Capabilities()` in sync with what you actually override: if you advertise
> `CanWrite: true`, make sure you override `WriteData`. See
> [Capability-Based Implementation](#capability-based-implementation) for a test
> helper that catches drift.

### Step 4: Register with MultiManager

In your main.go or initialization code:

```go
import (
    "github.com/dotside-studios/davi-nfc-agent/nfc"
    "github.com/dotside-studios/davi-nfc-agent/nfc/multimanager"
    "myproject/myreader"
)

func main() {
    manager := multimanager.NewMultiManager(
        multimanager.ManagerEntry{Name: nfc.ManagerTypeHardware, Manager: nfc.NewManager()},
        multimanager.ManagerEntry{Name: "myreader", Manager: myreader.NewManager()},
    )

    // Use the manager...
}
```

## Dynamic Device Discovery

Implement `DeviceChangeNotifier` to notify the system when devices are added or removed:

```go
type MyManager struct {
    devices     map[string]*MyDevice
    devicesChan chan struct{}
    mu          sync.RWMutex
}

// DeviceChanges returns a channel that signals when devices change.
// Implements nfc.DeviceChangeNotifier.
func (m *MyManager) DeviceChanges() <-chan struct{} {
    return m.devicesChan
}

// Call this when a device is added or removed
func (m *MyManager) notifyDeviceChange() {
    select {
    case m.devicesChan <- struct{}{}:
    default:
        // Channel full, skip notification
    }
}
```

The `MultiManager` automatically listens to managers that implement `DeviceChangeNotifier` and forwards change notifications.

## Capability-Based Implementation

You don't need to implement all methods if your device doesn't support them.
Embed `nfc.BaseTag` so unsupported operations already return the right
"not supported" error, then advertise what *is* supported via `Capabilities()`:

```go
type MyTag struct {
    nfc.BaseTag // WriteData/Transceive/MakeReadOnly default to "not supported"
    // ...
}

func (t *MyTag) Capabilities() nfc.TagCapabilities {
    return nfc.TagCapabilities{
        CanRead:       true,
        CanWrite:      false, // Read-only device — no need to override WriteData
        CanTransceive: false,
        CanLock:       false,
    }
}
```

Because the defaults come from `BaseTag`, a read-only tag literally just needs
`UID`, `Type`, `NumericType`, and `ReadData` — there is no `WriteData` boilerplate
to write.

### Keeping capabilities honest

`Capabilities()` and actual method behavior are two separate sources of truth, so
they can drift. Drop `nfc.AssertCapabilitiesConsistent` into your tests to catch
the common cases (it performs only non-mutating checks and never writes or locks
the tag):

```go
func TestMyTagCapabilities(t *testing.T) {
    tag := &MyTag{ /* ... */ }
    if err := nfc.AssertCapabilitiesConsistent(tag); err != nil {
        t.Fatal(err)
    }
}
```

Callers can check capabilities before calling methods:

```go
caps := nfc.GetTagCapabilities(tag)
if caps.CanWrite {
    tag.WriteData(data)
} else {
    log.Println("Tag does not support writing")
}
```

### Capability Helper Functions

Use these convenience functions for common capability checks:

```go
// Check tag capabilities
if nfc.CanTagRead(tag) {
    data, _ := tag.ReadData()
}

if nfc.CanTagWrite(tag) {
    tag.WriteData(data)
}

if nfc.CanTagTransceive(tag) {
    resp, _ := tag.Transceive(apdu)
}

if nfc.CanTagLock(tag) {
    tag.MakeReadOnly()
}
```

### DeviceCapabilities

Use `GetDeviceCapabilities()` to inspect device capabilities:

```go
caps := nfc.GetDeviceCapabilities(device)

// DeviceCapabilities struct:
// - CanTransceive: bool      // Device supports raw transceive
// - CanPoll: bool            // Device supports polling for tags
// - SupportedTagTypes: []string  // e.g., ["MIFARE Classic", "NTAG"]
// - DeviceType: string       // e.g., "libnfc", "smartphone"
// - MaxBaudRate: int         // Max baud rate in bps
// - SupportsEvents: bool     // Tag arrival/removal events

if caps.SupportsEvents {
    // Event-driven device (e.g., smartphone)
} else if caps.CanPoll {
    // Polling device (e.g., hardware reader)
}
```

Capabilities are automatically built from optional interfaces the device implements (`DeviceInfoProvider`, `DeviceEventEmitter`).

## Error Handling

Use the structured error types for consistent error handling:

```go
import "github.com/dotside-studios/davi-nfc-agent/nfc"

// For unsupported operations
return nfc.NewNotSupportedError("Transceive")

// For authentication failures
return nfc.NewAuthError("ReadData", tag.UID(), err)

// For read/write failures
return nfc.NewReadError("ReadData", err)
return nfc.NewWriteError("WriteData", err)

// For generic errors with context
return nfc.WrapError(nfc.ErrCodeReadFailed, "ReadSector", "failed to read sector 1", err)
```

Callers can handle errors programmatically:

```go
if nfc.IsNotSupportedError(err) {
    // Operation not supported, try alternative
}

if nfc.IsAuthError(err) {
    // Authentication failed, maybe try different key
}

code := nfc.GetErrorCode(err)
switch code {
case nfc.ErrCodeTagRemoved:
    // Tag was removed, retry
case nfc.ErrCodeReadFailed:
    // Read failed, handle error
}
```

## Advanced Write Operations

For tags that need special write handling, implement the `AdvancedWriter` interface:

```go
// TagWriteOptions controls write behavior
type TagWriteOptions struct {
    // ForceInitialize wipes and reinitializes the tag before writing.
    // WARNING: This erases all existing data.
    ForceInitialize bool
}

// AdvancedWriter is an optional interface for tags supporting write options.
// Implement WriteDataWithOptions to handle special write cases.
func (t *MyTag) WriteDataWithOptions(data []byte, opts nfc.TagWriteOptions) error {
    if opts.ForceInitialize {
        // Wipe and reinitialize the tag
        if err := t.format(); err != nil {
            return err
        }
    }
    return t.WriteData(data)
}
```

The `NFCReader` automatically uses `WriteDataWithOptions` when available:

```go
// Writing with force initialization
opts := nfc.TagWriteOptions{ForceInitialize: true}
if writer, ok := tag.(nfc.AdvancedWriter); ok {
    err := writer.WriteDataWithOptions(data, opts)
} else {
    // Fallback to standard write
    err := tag.WriteData(data)
}
```

## Optional: Server Integration

If your device needs WebSocket handlers (like smartphone NFC):

```go
import "github.com/dotside-studios/davi-nfc-agent/server"

// Implement server.ServerHandler
func (m *MyManager) Register(s server.HandlerServer) {
    s.HandleMessage("myreader:scan", m.handleScan)
}

// Implement server.ServerHandlerCloser for cleanup
func (m *MyManager) Close() {
    // Cleanup resources
}
```

## Testing Your Implementation

Create mock implementations for testing:

```go
func TestMyDevice(t *testing.T) {
    device := &MyDevice{connection: "test"}

    // Test capabilities
    caps := device.Capabilities()
    if !caps.CanPoll {
        t.Error("Expected CanPoll to be true")
    }

    // Test GetTags
    tags, err := device.GetTags()
    if err != nil {
        t.Errorf("GetTags failed: %v", err)
    }

    // Test tag capabilities
    for _, tag := range tags {
        tagCaps := nfc.GetTagCapabilities(tag)
        if !tagCaps.CanRead {
            t.Error("Expected tag to support reading")
        }
    }
}
```

## Examples

### Read-Only Network Device

A device that receives tag data over the network (read-only):

```go
type NetworkTag struct {
    nfc.BaseTag // write/transceive/lock default to "not supported"

    uid     string
    tagType string
    data    []byte // Pre-loaded data
}

func (t *NetworkTag) UID() string      { return t.uid }
func (t *NetworkTag) Type() string     { return t.tagType }
func (t *NetworkTag) NumericType() int { return 0 }

func (t *NetworkTag) Capabilities() nfc.TagCapabilities {
    return nfc.TagCapabilities{
        CanRead:       true,
        CanWrite:      false,
        CanTransceive: false,
        CanLock:       false,
        TagFamily:     t.tagType,
    }
}

func (t *NetworkTag) ReadData() ([]byte, error) {
    return t.data, nil
}

// No WriteData needed — inherited from nfc.BaseTag as "not supported".
```

### Serial PN532 Reader

A device connected via serial port:

```go
type PN532Device struct {
    port   io.ReadWriteCloser
    conn   string
}

func (d *PN532Device) DeviceType() string {
    return "pn532-serial"
}

func (d *PN532Device) SupportedTagTypes() []string {
    return []string{"MIFARE Classic", "NTAG", "ISO14443-4"}
}

func (d *PN532Device) GetTags() ([]nfc.Tag, error) {
    // Send InListPassiveTarget command
    cmd := []byte{0xD4, 0x4A, 0x01, 0x00}
    resp, err := d.sendCommand(cmd)
    if err != nil {
        return nil, err
    }

    // Parse response and create tags
    // ...
}
```

## Interface Reference

### Methods you must implement

These four have no sensible default and must be implemented on every tag:

| Method | Interface |
|--------|-----------|
| `UID()` | TagIdentifier |
| `Type()` | TagIdentifier |
| `NumericType()` | TagIdentifier |
| `ReadData()` | TagReader |

### Methods provided by `nfc.BaseTag`

Embedding `nfc.BaseTag` supplies these with safe defaults. Override only the ones
your tag supports:

| Method | Interface | BaseTag default |
|--------|-----------|-----------------|
| `Connect()` | TagConnection | no-op (returns nil) |
| `Disconnect()` | TagConnection | no-op (returns nil) |
| `WriteData()` | TagWriter | returns NotSupported |
| `Transceive()` | TagTransceiver | returns NotSupported |
| `IsWritable()` | TagLocker | returns false |
| `CanMakeReadOnly()` | TagLocker | returns false |
| `MakeReadOnly()` | TagLocker | returns NotSupported |

### Optional Methods

| Method | Interface | Purpose |
|--------|-----------|---------|
| `Capabilities()` | TagCapabilityProvider | Runtime tag capability discovery |
| `WriteDataWithOptions()` | AdvancedWriter | Write with initialization options |
| `DeviceChanges()` | DeviceChangeNotifier | Device add/remove notifications |
| `DeviceType()` | DeviceInfoProvider | Device type identifier ("libnfc", "smartphone") |
| `SupportedTagTypes()` | DeviceInfoProvider | List of supported tag types |
| `SupportsEvents()` | DeviceEventEmitter | Whether device emits tag events |
| `SupportsTransceive()` | DeviceTransceiver | Whether device supports raw transceive |
| `IsHealthy()` | DeviceHealthChecker | Connection health validation |
| `Register()` | server.ServerHandler | WebSocket integration |
| `Close()` | server.ServerHandlerCloser | Cleanup on shutdown |

### DeviceInfoProvider Interface

Implement this interface to provide device metadata. Capabilities are built automatically from this:

```go
func (d *MyDevice) DeviceType() string {
    return "myreader"
}

func (d *MyDevice) SupportedTagTypes() []string {
    return []string{"MIFARE Classic", "NTAG"}
}
```

### DeviceEventEmitter Interface

For event-based devices (like smartphones) that receive tags via events rather than polling:

```go
func (d *MyDevice) SupportsEvents() bool {
    return true  // Tags arrive as events, not via polling
}
```

When `SupportsEvents()` returns true, `BuildDeviceCapabilities()` will automatically set:
- `CanPoll: false`
- `CanTransceive: false`
- `SupportsEvents: true`

### DeviceTransceiver Interface

Polling devices default to `CanTransceive: true`. If your polling device's
`Transceive` actually returns a `NotSupported` error, implement
`DeviceTransceiver` so the reported capabilities match reality:

```go
func (d *MyDevice) SupportsTransceive() bool {
    return false // Device cannot do raw transceive
}
```

When present, `SupportsTransceive()` is authoritative for `CanTransceive` in the
capabilities built by `BuildDeviceCapabilities()`.

### DeviceHealthChecker Interface

For devices that support connection health checking:

```go
func (d *MyDevice) IsHealthy() error {
    if !d.isConnected {
        return fmt.Errorf("device not connected")
    }
    return nil
}
```

The `DeviceManager` uses this interface to check device health before operations.
