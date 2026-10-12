# Extending NFC Support

This guide covers adding a reader backend or a tag type to the agent by
implementing the interfaces in package `nfc`. Using an existing backend from a
program of your own is covered in [Custom Builds](custom-builds.md).

## Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                 multimanager.MultiManager                   │
│     Aggregates several managers, routes device requests     │
├──────────────────┬──────────────────┬───────────────────────┤
│   pcsc.Manager   │ remotenfc.Manager│   YourManager         │
│  (PC/SC readers) │ (WebNFC/mobile)  │   (custom)            │
└────────┬─────────┴────────┬─────────┴──────────┬────────────┘
         │                  │                    │
         ▼                  ▼                    ▼
      Device             Device               Device
         │                  │                    │
         ▼                  ▼                    ▼
       Tag[]              Tag[]               Tag[]
```

| Interface | Purpose |
|-----------|---------|
| `nfc.Manager` | Lists devices and opens them |
| `nfc.Device` | An opened reader: polls for tags and exchanges bytes |
| `nfc.Tag` | One tag: identity, read, write, transceive, lock, capabilities |

A backend implements no authentication of its own. Devices that dial in reach it
through `pairing.Gate.Admit`, which wraps the backend's endpoint at the mount,
checks the credential and names the device it admitted; the backend reads that
name with `deviceid.Of(r)` and registers the device under it, or mints one when
nothing admitted the connection.

The gate is not in the manager tree. It wraps endpoints, so a backend with none,
such as a reader attached to this machine, is untouched by it. What it asks of
the tree is `DisconnectDevice`, so revoking a credential ends the session
holding it.

## Adding a reader backend

The samples below build one package, `myreader`, for a polled reader that
cannot exchange raw bytes with its tags.

### Step 1: The manager

```go
package myreader

import "github.com/dotside-studios/davi-nfc-agent/nfc"

type MyManager struct {
	// Your connection state: USB, serial, network.
}

func NewManager() *MyManager {
	return &MyManager{}
}

// Devices lists what this manager offers, and what is known of each before it
// is opened. CanPoll says the agent may open the device and poll it; a device
// that reports its own scans declares SupportsEvents instead and is left out of
// every reader list.
func (m *MyManager) Devices() ([]nfc.DeviceListing, error) {
	return []nfc.DeviceListing{{
		Path:         "myreader:default",
		ID:           "myreader:default",
		Capabilities: nfc.DeviceCapabilities{CanPoll: true, DeviceType: "myreader"},
	}}, nil
}

// OpenDevice returns the device ready to use: the agent performs no further
// initialization.
func (m *MyManager) OpenDevice(deviceStr string) (nfc.Device, error) {
	return &MyDevice{connection: deviceStr}, nil
}
```

### Step 2: The device

`nfc.Device` has five methods. The device below also implements
`DeviceInfoProvider`, which names it in its capabilities, and
`DeviceTransceiver`, because a device that does not implement it is reported as
`CanTransceive: true`.

```go
type MyDevice struct {
	connection string
	// Your hardware handle: serial port, USB handle, socket.
}

func (d *MyDevice) Close() error       { return nil }
func (d *MyDevice) String() string     { return "My NFC Reader" }
func (d *MyDevice) Connection() string { return d.connection }

func (d *MyDevice) Transceive(txData []byte) ([]byte, error) {
	return nil, nfc.NewNotSupportedError("Transceive")
}

// SupportsTransceive keeps the reported capabilities in step with Transceive.
func (d *MyDevice) SupportsTransceive() bool { return false }

func (d *MyDevice) DeviceType() string          { return "myreader" }
func (d *MyDevice) SupportedTagTypes() []string { return []string{"NTAG"} }

// GetTags polls the reader and returns the tags on it, empty when there are
// none.
func (d *MyDevice) GetTags() ([]nfc.Tag, error) {
	uid, present, err := d.poll()
	if err != nil || !present {
		return nil, err
	}
	return []nfc.Tag{&MyTag{uid: uid, device: d}}, nil
}

// poll, readMemory and writeMemory stand for your reader's own commands.
func (d *MyDevice) poll() (uid string, present bool, err error) { return "", false, nil }

func (d *MyDevice) readMemory(uid string) ([]byte, error) {
	return nil, nfc.NewNotSupportedError("readMemory")
}

func (d *MyDevice) writeMemory(uid string, data []byte) error {
	return nfc.NewNotSupportedError("writeMemory")
}
```

### Step 3: The tag

`nfc.Tag` combines the role interfaces in
[`nfc/tag.go`](../nfc/tag.go): identity, connection, read, write, transceive,
lock and `Capabilities()`. Embed `nfc.BaseTag` and implement the four methods it
has no default for: `UID`, `Type`, `NumericType` and `ReadData`. `BaseTag`
supplies the rest: connection is a no-op, write, transceive and lock report
not supported, and `Capabilities()` reports a readable NDEF tag and nothing
else.

```go
type MyTag struct {
	nfc.BaseTag

	uid    string
	device *MyDevice
}

func (t *MyTag) UID() string      { return t.uid }
func (t *MyTag) Type() string     { return "NTAG213" }
func (t *MyTag) NumericType() int { return 0 }

func (t *MyTag) ReadData() ([]byte, error) {
	memory, err := t.device.readMemory(t.uid)
	if err != nil {
		return nil, err
	}
	ndef, found := nfc.TLVFindNDEF(memory)
	if !found {
		return nil, nfc.NewNoPayloadError("ReadData", t.uid, nil)
	}
	return ndef, nil
}
```

`ReadData` must not report an absent NDEF message by returning zero bytes and a
nil error, which is indistinguishable from a successful read of an empty tag.
Return `nfc.NewNoPayloadError`, and the reader publishes the tag as a scan
carrying its identity alone. A tag that never carries NDEF reports
`SupportsNDEF: false`, which skips the read.

A tag that supports more overrides the operation and `Capabilities()` together,
so what it advertises and what it does cannot drift apart:

```go
func (t *MyTag) WriteData(data []byte) error {
	return t.device.writeMemory(t.uid, data)
}

func (t *MyTag) Capabilities() nfc.TagCapabilities {
	return nfc.TagCapabilities{
		CanRead:      true,
		CanWrite:     true,
		SupportsNDEF: true,
		TagFamily:    "NTAG",
		Technology:   "ISO14443A",
	}
}
```

A tag whose content arrives from elsewhere rather than being read live, as a
phone's does, sets `ReadsAreSnapshot`, so a write is not confirmed by reading it
back.

### Step 4: Register the manager

```go
package main

import (
	"example.com/myreader"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/multimanager"
	"github.com/dotside-studios/davi-nfc-agent/nfc/pcsc"
)

func main() {
	manager := multimanager.NewMultiManager(
		multimanager.ManagerEntry{Name: nfc.ManagerTypeHardware, Manager: pcsc.NewManager()},
		multimanager.ManagerEntry{Name: "myreader", Manager: myreader.NewManager()},
	)
	defer manager.Close()

	// Hand manager to agent.Setup or nfc.NewSupervisor; see custom-builds.md.
}
```

## Optional interfaces

| Implemented by | Interface | Method | Effect |
|---|---|---|---|
| Device | `DeviceInfoProvider` | `DeviceType()`, `SupportedTagTypes()` | Fills `DeviceType` and `SupportedTagTypes` in the capabilities |
| Device | `DeviceEventEmitter` | `SupportsEvents()` | True reports `SupportsEvents` and clears `CanPoll` |
| Device | `DeviceTransceiver` | `SupportsTransceive()` | Sets `CanTransceive`, which is true without it |
| Device | `DeviceRawTransceiver` | `SupportsTransceiveRaw()` | Sets `CanTransceiveRaw`, which is false without it |
| Device | `DeviceHealthChecker` | `IsHealthy()` | Checked before reusing a device that is already open |
| Tag | `TagRawTransceiver` | `TransceiveRaw(frame)` | Framing-level exchange, with no ISO 7816 envelope |
| Tag | `AdvancedWriter` | `WriteDataWithOptions(data, opts)` | Used instead of `WriteData` when a write asks for `ForceInitialize` |
| Manager | `DeviceChangeNotifier` | `DeviceChanges()` | Signals devices added or removed |
| Manager | `TagReporter` | `Scans()` | Publishes scans from devices that report their own |
| Manager | `DeviceDisconnector` | `DisconnectDevice(id, reason)` | Ends a live session, for revocation |
| Manager | (bare method) | `Close()` | Called by `MultiManager.Close` |

### Device capabilities

`nfc.GetDeviceCapabilities(device)` builds a `DeviceCapabilities` from the
interfaces above. It starts from `CanPoll: true`, `CanTransceive: true` and
`DeviceType: "unknown"`, then:

- `DeviceInfoProvider` sets `DeviceType` (`"pcsc"` for the PC/SC backend,
  `"smartphone"` for a phone) and `SupportedTagTypes`.
- `DeviceEventEmitter` returning true sets `SupportsEvents` and clears
  `CanPoll`. It leaves `CanTransceive` alone: how a device reports scans says
  nothing about whether it can exchange bytes with the tag.
- `DeviceTransceiver` decides `CanTransceive`.
- `DeviceRawTransceiver` decides `CanTransceiveRaw`. Its method must answer from
  what is already known, since it is called while building capabilities and
  sends nothing to the card.

### Raw frames

`TransceiveRaw` reaches commands that are not APDUs, such as an NTAG21x
`READ_SIG`, `READ_CNT` or `PWD_AUTH`. The reader sends a raw exchange only to a
tag implementing `TagRawTransceiver`, and refuses it as not supported
otherwise. A device that can carry one, as a PN533-based reader does with
`InCommunicateThru`, implements `DeviceRawTransceiver` to report
`CanTransceiveRaw`. A tag driver built on an `nfc.CardTransport` checks for
`nfc.RawCardTransport`, and refuses the exchange when the transport is not one.

### Write options

```go
func (t *MyTag) WriteDataWithOptions(data []byte, opts nfc.TagWriteOptions) error {
	if opts.ForceInitialize {
		// Wipe and reinitialize the tag; this erases everything on it.
	}
	return t.WriteData(data)
}
```

### Device discovery

```go
type MyManager struct {
	changes chan struct{}
}

// DeviceChanges implements nfc.DeviceChangeNotifier.
func (m *MyManager) DeviceChanges() <-chan struct{} {
	return m.changes
}

// notifyDeviceChange is called when a device is added or removed. A send that
// would block is dropped, since one pending signal already says "look again".
func (m *MyManager) notifyDeviceChange() {
	select {
	case m.changes <- struct{}{}:
	default:
	}
}
```

`MultiManager` listens to every child that implements `DeviceChangeNotifier`
and forwards its signals on its own `DeviceChanges`.

### Health checks

```go
func (d *MyDevice) IsHealthy() error {
	if _, _, err := d.poll(); err != nil {
		return fmt.Errorf("myreader not responding: %w", err)
	}
	return nil
}
```

When asked to connect to a device it already holds, the device manager reuses
it if `IsHealthy` returns nil and reopens it otherwise. A device without the
method is assumed healthy.

### Serving your own connections

A manager whose devices connect over the network, rather than being attached to
this machine, owns that transport. `remotenfc` is the worked example: it serves
the WebSocket endpoint phones connect to through `Manager.Handler`. Expose an
`http.Handler`, keep authentication out of it, and take anything else the
program configures as options. The program mounts it on the server plugin under
`server.ModeDevice` (see [Custom Builds](custom-builds.md)).

Implement `Close()`, with no arguments and no return value, to stop goroutines
and drop connections. `MultiManager.Close` calls it on every child that has it;
a `Close() error` does not match and is not called.

## Errors

Return the structured errors in package `nfc`, so the agent and its clients can
tell the cases apart (see [Errors](errors.md) for how they reach a client):

| Constructor | Use |
|---|---|
| `nfc.NewNotSupportedError(op)` | An operation this tag or device cannot perform |
| `nfc.NewNoPayloadError(op, uid, cause)` | A tag that was read and holds no NDEF message |
| `nfc.NewAuthError(op, uid, cause)` | Authentication to the tag failed |
| `nfc.NewReadError(op, cause)`, `nfc.NewWriteError(op, cause)` | A read or write failed |
| `nfc.WrapError(code, op, message, cause)` | Any other `nfc.ErrCode*` with context |

## Testing

`nfc.AssertCapabilitiesConsistent` checks a tag's `Capabilities()` against its
query methods: `CanLock` against `CanMakeReadOnly()`, `IsReadOnly` against
`IsWritable()`, and `SupportsNDEF: false` against what `ReadData` returns. It
never writes, transceives or locks, so it is safe on hardware; cover those
operations in tests of your own.

```go
func TestMyDevice(t *testing.T) {
	device := &MyDevice{connection: "test"}

	caps := nfc.GetDeviceCapabilities(device)
	if !caps.CanPoll || caps.CanTransceive {
		t.Errorf("capabilities = %+v", caps)
	}

	tag := &MyTag{uid: "04A1B2C3D4E5F6", device: device}
	if err := nfc.AssertCapabilitiesConsistent(tag); err != nil {
		t.Fatal(err)
	}
}
```

`nfc/nfctest` emulates tags in memory behind an `nfc.CardTransport`, for testing
a tag driver's I/O without hardware.

## Example: a serial PN532 reader

An outline of a PN532 device's polling. `sendCommand` and `parseTargets` stand
for your serial framing and response parsing; the remaining `nfc.Device` methods
follow Step 2.

```go
type PN532Device struct {
	port io.ReadWriteCloser
	conn string
}

func (d *PN532Device) DeviceType() string          { return "pn532-serial" }
func (d *PN532Device) SupportedTagTypes() []string { return []string{"MIFARE Classic", "NTAG"} }

func (d *PN532Device) GetTags() ([]nfc.Tag, error) {
	// InListPassiveTarget: one target, 106 kbps type A.
	resp, err := d.sendCommand([]byte{0xD4, 0x4A, 0x01, 0x00})
	if err != nil {
		return nil, err
	}
	return d.parseTargets(resp), nil
}
```

The PN532 carries raw frames with `InCommunicateThru`, so such a device can also
implement `DeviceRawTransceiver` and give its NTAG tags `TransceiveRaw`.

## Interface reference

| Type | Must implement | Supplied by `nfc.BaseTag` |
|---|---|---|
| `nfc.Manager` | `OpenDevice`, `Devices` | |
| `nfc.Device` | `Close`, `String`, `Connection`, `Transceive`, `GetTags` | |
| `nfc.Tag` | `UID`, `Type`, `NumericType`, `ReadData` | `Connect`, `Disconnect` (no-op); `WriteData`, `Transceive`, `MakeReadOnly` (not supported); `IsWritable`, `CanMakeReadOnly` (false); `Capabilities` (`CanRead`, `SupportsNDEF`) |
