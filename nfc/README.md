# NFC Package

Package `nfc` is the agent's NFC layer: the interfaces a reader backend
implements, the drivers for each tag type, NDEF encoding and decoding, and the
`Supervisor` that operates every reader a backend offers.

It is transport-agnostic. The backends live beside it:

| Package | Backend |
|---|---|
| `nfc/pcsc` | PC/SC readers on this machine, through goscard without cgo, or `ebfe/scard` with `-tags cgopcsc` |
| `nfc/remotenfc` | Phones and WebNFC browsers acting as readers over the device WebSocket protocol |
| `nfc/multimanager` | Several backends behind one `Manager` |
| `nfc/virtualnfc` | A software reader carrying virtual tags |

The API reference is the godoc: `go doc github.com/dotside-studios/davi-nfc-agent/nfc`.
Adding a reader backend or a tag type is covered in
[Extending NFC support](../docs/extending-nfc-support.md), and using the
package from a program of your own in
[Custom Builds](../docs/custom-builds.md#driving-the-readers-directly).

## Layers

- **`Manager` and `Device`** list and open readers. A `Device` polls with
  `GetTags` and returns tags ready to use.
- **`Tag`** is one tag at the protocol level, composed of the role interfaces in
  [`tag.go`](tag.go). Type-specific operations are further interfaces, such as
  `ClassicTag` for sector and block access.
- **`Card` and `Message`** sit above a tag: `Card` is an `io.Reader` and
  `io.Writer` over its NDEF data, and `ReadMessage` decodes it.
- **`Supervisor`** opens every reader a manager offers, polls them, and
  publishes their scans on one signal, each naming the reader it came from.
  Operations name the reader they apply to.

## Supported card types

| Type | Driver |
|---|---|
| MIFARE Classic 1K and 4K | `tag_classic.go`, `tag_classic_format.go` |
| MIFARE Ultralight, Ultralight C, Ultralight EV1 | `tag_ultralight.go` |
| NTAG213, NTAG215, NTAG216 | `tag_ntag.go` |
| MIFARE DESFire, EV1, EV2, EV3 | `tag_desfire.go`, `tag_desfire_session.go`, `tag_desfire_channel.go` |
| NTAG 424 DNA | `tag_ntag424.go`, `tag_ntag424_session.go`, `tag_ntag424_ops.go` |
| ISO 14443-4 Type 4 | `tag_iso14443.go` |
| FeliCa | `tag_felica.go`, identifier only |

`NewTagForType` maps a detected type to its driver. A type detected without a
driver, such as MIFARE Plus, has no tag.

## Example

```go
card := nfc.NewCard(tag)

msg, err := card.ReadMessage()
if err != nil {
	return err
}
if ndef, ok := msg.(*nfc.NDEFMessage); ok {
	text, _ := ndef.GetText()
	log.Printf("text: %s", text)
}

card.Reset()
if err := card.WriteMessage(nfc.NewNDEFMessage().AddText("Hello", "en")); err != nil {
	return err
}

// Sector and block access on MIFARE Classic.
if classic, ok := card.GetUnderlyingTag().(nfc.ClassicTag); ok {
	key := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	block, err := classic.Read(1, 0, key, nfc.KeyTypeA)
	if err != nil {
		return err
	}
	log.Printf("sector 1 block 0: %x", block)
}
```

Tests run without hardware against the mocks (`NewMockManager`,
`NewMockDevice`, `NewMockTag` and the type-specific `NewMockClassicTag`,
`NewMockNtagTag`, `NewMockISO14443Tag`) or the emulators in `nfc/nfctest`:

```go
tag := nfc.NewMockTag("04A1B2C3D4E5F6")
tag.TagType = "MIFARE Classic 1K"
tag.Data = []byte("test data")

data, err := io.ReadAll(nfc.NewCard(tag))
```

## Design decisions

**Tag and Card are separate.** `Tag` is the protocol: identity, raw reads and
writes, transceive, locking, and type-specific access by type assertion. `Card`
is the data: standard `io` semantics over the NDEF payload, which is what most
callers want.

**A tag declares its capabilities.** `Capabilities()` is part of `Tag`, and the
built-in drivers declare theirs in `tag_profile.go`, the single source for each
type's `Type()`, `NumericType()` and capabilities. `InferTagCapabilities`
guesses from a type name only when no tag is left to ask, such as for a card
decoded from the wire.

**An empty tag is an error, not empty data.** A tag that holds no NDEF message
reports `NewNoPayloadError` rather than zero bytes, so a successful read of an
empty message and a tag with nothing on it stay distinguishable. `ReadMessage`
returns `*NDEFMessage` for NDEF data and `*TextMessage` for raw bytes that do
not decode.

**Connection is the backend's.** `GetTags` returns tags ready to use, and
`Connect` and `Disconnect` exist for the interface; the built-in drivers and
`BaseTag` implement them as no-ops.

**One operation per reader at a time.** The `Supervisor` is safe to use from any
goroutine. Each reader runs one tag operation at a time, bounded by the caller's
context and the reader's timeout, and a caller arriving while an abandoned
operation is still finishing is refused with `ErrReaderBusy`. A program driving
a `Device` and its tags directly, without a `Supervisor`, serializes its own
operations on a tag.

## Files

| File | Purpose |
|---|---|
| `manager.go`, `device.go`, `tag.go` | The `Manager`, `Device` and `Tag` interfaces and their optional extensions |
| `capabilities.go` | `TagCapabilities`, `DeviceCapabilities`, and how a device's are built |
| `tag_base.go` | `CardTransport` and the base the PC/SC drivers share |
| `tag_base_default.go` | `BaseTag`, the defaults a custom tag embeds |
| `tag_profile.go` | Each built-in type's name, numeric type and capabilities |
| `tag_*.go` | The drivers listed under [Supported card types](#supported-card-types) |
| `tagdetect.go` | Tag detection from the ATR and UID |
| `emulated_tag.go` | Production drivers over an emulated `CardTransport`, for `nfc/nfctest` |
| `supervisor.go`, `supervisor_ops.go` | The `Supervisor` and the operations it routes to a reader |
| `reader.go` | One reader: polling, reading, writing, locking and transceiving |
| `device_manager.go` | Opening one device, reconnecting it, and reporting its lifecycle |
| `scan_debounce.go` | Suppressing repeated scans of a card held on a polling reader |
| `cache.go` | The last scanned UID |
| `card.go`, `message.go`, `ndef.go`, `tlv.go` | `Card`, NDEF messages and records, TLV framing |
| `apdu.go`, `apdu_chain.go`, `apdu_explain.go` | APDU construction, chaining, and decoding a command into what it does |
| `keys.go`, `desfire_keys.go`, `ntag424_keys.go` | Card keys |
| `feedback.go` | `FeedbackDevice`, for readers with an LED or buzzer |
| `errors.go` | `NFCError` and its codes |
| `*_mock.go` | Mocks for testing |
