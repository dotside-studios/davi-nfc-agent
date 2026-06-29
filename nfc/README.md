# NFC Package

A Go package providing a unified, high-level abstraction for NFC operations across multiple card types. Built on top of PC/SC via ebfe/scard.

## Overview

The `nfc` package provides a modular architecture for working with NFC tags, abstracting away the complexity of different card types and protocols:

- **Unified API**: Single interface for all supported card types
- **High-level abstractions**: `io.Reader`/`io.Writer` interface for NDEF data
- **Low-level access**: Direct protocol access when needed
- **Device management**: Automatic device discovery and lifecycle management

## Architecture

```
┌─────────────────────────────────────────┐
│          Application Layer              │
│      (WebSocket Server, CLI, etc)       │
└─────────────────────────────────────────┘
                   │
                   ↓
┌─────────────────────────────────────────┐
│          High-Level API                 │
│   Card (io.Reader/Writer interface)     │
│   Message (NDEF encoding/decoding)      │
└─────────────────────────────────────────┘
                   │
                   ↓
┌─────────────────────────────────────────┐
│          Tag Abstraction Layer          │
│  Tag interface (unified operations)     │
└─────────────────────────────────────────┘
                   │
       ┌───────────┴───────────┬──────────┐
       ↓                       ↓          ↓
┌──────────────┐  ┌─────────────────┐  ┌────────────┐
│ ClassicTag   │  │   DESFireTag    │  │ ISO14443   │
│ UltralightTag│  │   (others...)   │  │   Type4    │
└──────────────┘  └─────────────────┘  └────────────┘
                   │
                   ↓
┌─────────────────────────────────────────┐
│        Device Management Layer          │
│   Manager, Device (connection mgmt)     │
└─────────────────────────────────────────┘
                   │
                   ↓
┌─────────────────────────────────────────┐
│         PC/SC API (ebfe/scard)          │
│     System: pcsclite / WinSCard         │
└─────────────────────────────────────────┘
```

## Core Components

### Manager

Device discovery and connection management.

```go
// Create a manager
manager := nfc.NewManager()

// List available NFC readers
devices, err := manager.ListDevices()

// Open a device
device, err := manager.OpenDevice(devices[0])
defer device.Close()
```

**File**: `manager.go`, `manager_pcsc.go`

### Device

Represents a connected NFC reader device.

```go
// Poll for tags
tags, err := device.GetTags()

// Get device information
connStr := device.Connection()

// Close device
device.Close()
```

**Files**: `device.go`, `device_pcsc.go`

### Tag

Low-level hardware protocol interface. Provides direct access to tag operations.

```go
type Tag interface {
    UID() string
    Type() string
    ReadData() ([]byte, error)
    WriteData(data []byte) error
    Connect() error
    Disconnect() error
    IsWritable() (bool, error)
}
```

**Files**:
- `tag.go` - Interface definition
- `tag_classic.go` - MIFARE Classic implementation
- `tag_desfire.go` - MIFARE DESFire implementation
- `tag_ultralight.go` - MIFARE Ultralight implementation
- `tag_iso14443.go` - ISO14443-4 Type 4 implementation

### Card

High-level abstraction implementing `io.Reader`, `io.Writer`, and `io.Closer`.

```go
// Create card from tag
card := nfc.NewCard(tag)

// Read NDEF data
data, err := io.ReadAll(card)

// Write NDEF data
io.WriteString(card, "Hello, NFC!")
card.Close()

// Read as structured message
msg, err := card.ReadMessage()
```

**File**: `card.go`

### Message

NDEF message encoding and decoding.

```go
// Decode NDEF message
msg, err := nfc.DecodeNDEF(data)

// Access text records
text, err := msg.GetText()

// Create new text message
msg := nfc.NewNDEFMessage()
msg.AddTextRecord("Hello!", "en")
data, _ := msg.Encode()
```

**Files**: `message.go`, `ndef.go`

## Supported Card Types

### MIFARE Classic (1K/4K)

Sector and block-based memory structure with key authentication.

```go
if classic, ok := tag.(*nfc.ClassicTag); ok {
    // Read specific sector/block
    data, err := classic.Read(sector, block, key, keyType)

    // Write to sector/block
    err = classic.Write(sector, block, data, key, keyType)
}
```

**Features**:
- Sector/block read/write
- Key A/B authentication
- NDEF formatting and read/write
- Auto key discovery for reading

**File**: `tag_classic.go`

### MIFARE DESFire (EV1/EV2/EV3)

Application and file-based structure with advanced security.

```go
if desfire, ok := tag.(*nfc.DESFireTag); ok {
    // List applications
    apps, err := desfire.ApplicationIds()

    // Select application
    err = desfire.SelectApplication(appId)

    // Read/write files
    data, err := desfire.ReadData(fileNo)
}
```

**Features**:
- Application management
- File-based data storage
- NDEF read/write support
- Secure authentication

**File**: `tag_desfire.go`

### MIFARE Ultralight (including Ultralight C)

Page-based memory with simple read/write operations.

```go
if ultralight, ok := tag.(*nfc.UltralightTag); ok {
    // Read page
    data, err := ultralight.ReadPage(page)

    // Write page
    err = ultralight.WritePage(page, data)
}
```

**Features**:
- Page-based read/write
- NDEF read/write support
- Compact memory layout

**File**: `tag_ultralight.go`

### ISO14443-4 Type 4A/B

Standard ISO14443 Type 4 tags with NDEF support.

```go
// Works automatically through Card interface
card := nfc.NewCard(tag)
data, _ := io.ReadAll(card)
```

**Features**:
- Standard NDEF operations
- Direct APDU access via `Transceive()`

**File**: `tag_iso14443.go`

## Usage Examples

### Basic Read/Write

```go
package main

import (
    "fmt"
    "io"
    "github.com/dotside-studios/davi-nfc-agent/nfc"
)

func main() {
    // Initialize manager and device
    manager := nfc.NewManager()
    devices, _ := manager.ListDevices()
    device, _ := manager.OpenDevice(devices[0])
    defer device.Close()

    // Get tags
    tags, _ := device.GetTags()
    if len(tags) == 0 {
        fmt.Println("No tags found")
        return
    }

    // Read from card
    card := nfc.NewCard(tags[0])
    data, _ := io.ReadAll(card)
    fmt.Printf("Read: %s\n", string(data))

    // Write to card
    card.Reset()
    io.WriteString(card, "Hello, NFC!")
    card.Close()
}
```

### Working with NDEF Messages

```go
// Read structured NDEF message
card := nfc.NewCard(tag)
msg, err := card.ReadMessage()

switch m := msg.(type) {
case *nfc.NDEFMessage:
    // Access text records
    text, _ := m.GetText()
    fmt.Printf("Text: %s\n", text)

    // Access URI records
    uri, _ := m.GetURI()
    fmt.Printf("URI: %s\n", uri)

case *nfc.TextMessage:
    // Raw bytes (non-NDEF)
    fmt.Printf("Raw: %x\n", m.Data)
}

// Write NDEF message
ndefMsg := nfc.NewNDEFMessage()
ndefMsg.AddTextRecord("Hello World", "en")
err = card.WriteMessage(ndefMsg)
```

### Card-Specific Operations

```go
// Get underlying tag for advanced operations
tag := card.GetUnderlyingTag()

// MIFARE Classic: sector access
if classic, ok := tag.(*nfc.ClassicTag); ok {
    key := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
    data, _ := classic.Read(1, 0, key, nfc.KeyTypeA)
}

// DESFire: application management
if desfire, ok := tag.(*nfc.DESFireTag); ok {
    apps, _ := desfire.ApplicationIds()
    for _, app := range apps {
        fmt.Printf("App: %06X\n", app)
    }
}

// Ultralight: page operations
if ultralight, ok := tag.(*nfc.UltralightTag); ok {
    page4, _ := ultralight.ReadPage(4)
    fmt.Printf("Page 4: %x\n", page4)
}
```

### Continuous Polling

```go
manager := nfc.NewManager()
devices, _ := manager.ListDevices()
device, _ := manager.OpenDevice(devices[0])
defer device.Close()

for {
    tags, err := device.GetTags()
    if err != nil {
        time.Sleep(100 * time.Millisecond)
        continue
    }

    for _, tag := range tags {
        card := nfc.NewCard(tag)
        data, _ := io.ReadAll(card)
        fmt.Printf("UID: %s, Data: %s\n", card.UID, string(data))
    }

    time.Sleep(500 * time.Millisecond)
}
```

## Testing

The package includes comprehensive mocks for testing without physical hardware:

```go
// Mock manager
manager := nfc.NewMockManager()
device := nfc.NewMockDevice()
tag := nfc.NewMockTag("04A1B2C3D4E5F6", "MIFARE Classic 1K")

// Configure mock behavior
tag.SetReadData([]byte("test data"))

// Use in tests
card := nfc.NewCard(tag)
data, _ := io.ReadAll(card)
```

**Files**: `tag_mock.go`, `device_mock.go`, `manager_mock.go`

## Key Design Decisions

### Why separate Tag and Card?

- **Tag**: Low-level hardware protocol interface
  - Direct access to card-specific features
  - Minimal abstraction over libfreefare
  - Type assertions for card-specific operations

- **Card**: High-level data interface
  - Standard Go `io.Reader`/`io.Writer` semantics
  - NDEF-focused operations
  - Simplified API for common use cases

### NDEF vs Raw Data

The package automatically handles NDEF parsing:

```go
msg, err := card.ReadMessage()

// Returns NDEFMessage if NDEF-formatted
// Returns TextMessage (raw bytes) if not NDEF
```

This allows applications to handle both formatted and unformatted tags gracefully.

### Connection Management

The package handles connection lifecycle automatically:

- `device.GetTags()` performs polling and returns ready-to-use tags
- `Tag.Connect()` / `Disconnect()` exist for interface completeness but are not
  required by the framework; tags are usable as soon as `GetTags()` returns them
  (custom tags can inherit these as no-ops from `nfc.BaseTag`)
- Clean up with `device.Close()`

## Thread Safety

The package is **not thread-safe**. If you need concurrent access:

- Use one `Manager` per goroutine, OR
- Protect `Device` and `Tag` operations with mutexes

## Dependencies

- **github.com/ebfe/scard**: Go bindings for PC/SC
- PC/SC runtime (built into macOS/Windows, `pcsclite` on Linux)

## Files Reference

| File | Purpose |
|------|---------|
| `manager.go` | Manager interface |
| `manager_pcsc.go` | PC/SC manager implementation |
| `device.go` | Device interface |
| `device_pcsc.go` | PC/SC device implementation |
| `tag.go` | Tag interface and base types |
| `tag_base.go` | Shared base tag struct |
| `tag_classic.go` | MIFARE Classic implementation |
| `tag_desfire.go` | MIFARE DESFire implementation |
| `tag_ultralight.go` | MIFARE Ultralight implementation |
| `tag_ntag.go` | NTAG implementation |
| `tag_iso14443.go` | ISO14443-4 Type 4 implementation |
| `tagdetect.go` | ATR/UID-based tag detection |
| `apdu.go` | APDU command construction |
| `tlv.go` | TLV encode/decode utilities |
| `card.go` | High-level Card abstraction |
| `message.go` | Message interface and types |
| `ndef.go` | NDEF encoding/decoding |
| `constants.go` | Card type and key constants |
| `keys.go` | Key management utilities |
| `cache.go` | Tag caching for debouncing |
| `capabilities.go` | Tag capability detection |
| `errors.go` | Error types and handling |
| `*_test.go` | Unit tests |
| `*_mock.go` | Test mocks |

## Contributing

When adding support for new card types:

1. Implement the `Tag` interface
2. Add NDEF read/write if supported
3. Include card-specific methods as needed
4. Add unit tests with mocks
5. Update this documentation

## License

MIT License - See main repository LICENSE file
