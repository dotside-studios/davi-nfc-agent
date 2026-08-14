# Device Bridge — Improvement Plan

Execution plan following from [device-bridge-protocols.md](device-bridge-protocols.md).
That document concluded: the protocol is at the right layer, and what is wrong
with it is under-specification. This is the sequenced work.

## Diagnosis

Reading the code, the defects are not scattered — they are four instances of one
pattern plus one stub. **The wire is a lossy projection of a good internal
model.**

| Internal model | What reaches the wire |
|---|---|
| `nfc.TagCapabilities` (12 fields), `nfc.DeviceCapabilities` (6 fields), with `AssertCapabilitiesConsistent` to prevent drift | `{canRead, canWrite, nfcType}` — 3 fields (`nfc/remotenfc/protocol.go`) |
| `nfc.ErrorCode` — 10 typed codes (`ErrCodeTagRemoved`, `ErrCodeAuthFailed`, `ErrCodeCapacityExceeded`, …) | 9 ad-hoc strings that map to none of them: `PARSE_ERROR`, `TAG_SEND_FAILED`, `UNKNOWN_TYPE`, … |
| `nfc.Tag` — full read/write/transceive/lock surface | `remotenfc.Tag` hard-codes `CanWrite/CanTransceive/CanLock: false` |
| `nfc.DeviceTransceiver` — an interface specifically for declaring transceive support | Unreachable for remote devices, see below |

Plus one wrong coupling and one stub:

- **`nfc/capabilities.go:115-124`** — `SupportsEvents() == true` defaults
  `CanPoll` *and* `CanTransceive` to false. The `DeviceTransceiver` interface
  does override the latter, so this is a default rather than a rule; the gap is
  that `remotenfc.Device` does not implement it, and a capability declared over
  the wire never reaches this code. A PN532 and an iPhone therefore land in the
  same tier as a Web NFC browser.
- **`server/deviceserver/device_handler.go`** — `WSTypeDeviceWriteResponse`
  handling is `log.Printf("Write response received (not yet implemented)")`.
  The constants and payload structs exist; the path does not.

So the work is: fix the projection, remove the coupling, finish the stub, then
add the one genuinely missing layer.

## Phase 1 — Make the wire honest

No new capability, no behavior change, fully backward-compatible. This is the
foundation everything else needs, and it is the cheapest phase.

**1.1 Version negotiation.** Offer `Sec-WebSocket-Protocol: davi-nfc-device.v1`
on upgrade; a device that offers nothing is treated as `v0` (current behavior)
forever. Then make the first frame a `hello` carrying protocol version and
capabilities, with `registerDevice` folded into it for v1.
*Why first:* every later change needs a way to be introduced without a flag day.

**1.2 Project the capability structs onto the wire.** Replace the three-field
`remotenfc.DeviceCapabilities` with the existing `nfc.DeviceCapabilities` +
`nfc.TagCapabilities` shapes — they already carry JSON tags. Keep accepting the
three old fields when the peer is v0.
*Unblocks:* everything capability-conditional; §4.3 of the survey (capability is
a set, not a level).

**1.3 Project the error taxonomy onto the wire.** Map `nfc.ErrorCode` to wire
error codes, add a `retryable` flag, and keep the current strings as deprecated
aliases. Distinguish "retry this", "the tag left the field", and "this device
will never support that" — today they are all opaque strings.

**1.4 Clean-close semantics.** Distinguish a device saying goodbye from a device
dropping off, so we are not waiting 30s on a heartbeat timeout for an intentional
disconnect. This is MQTT's last-will idea implemented locally, not MQTT.

*Touches:* `protocol/websocket.go`, `protocol/device.go`,
`nfc/remotenfc/protocol.go`, `nfc/errors.go`,
`server/deviceserver/device_handler.go`, plus `docs/api.md`, `client/`.

## Phase 2 — Decouple events from transceive, finish the write path

**2.1 Break the coupling** — *folded into Phase 3.1.* The intent was to let a
remote device declare transceive. But `remotenfc.Device.Transceive` returns
`NotSupported` until the command channel exists, so declaring the capability
first would only produce capability drift — the exact fault
`AssertCapabilitiesConsistent` exists to catch. The declaration and the routing
have to land together, so `remotenfc.Device` gains `DeviceTransceiver` in 3.1.

The event-based default in `BuildDeviceCapabilities` is left alone: it is
documented behavior that external implementers rely on
(`docs/extending-nfc-support.md`), and `DeviceTransceiver` already overrides it
for any device that wants to.

**2.2 Finish `deviceWriteRequest`/`deviceWriteResponse`.** Route
`bridge.WriteRequest` to the owning device session, correlate the response by
`requestID`, enforce a timeout, and surface a typed error on drop. Add an
idempotency key so a write replayed after a reconnect is not applied twice —
cheapest to introduce now, with the first real request/response pair, rather
than retrofitted in Phase 3.

**2.3 Make `remotenfc.Tag` tell the truth.** Implement `WriteData` and
`MakeReadOnly` over 2.2, and derive `Capabilities()` from what the device
declared in 1.2 instead of returning hard-coded `false`.

*Value:* this is the first phase a user notices — writing to a tag held by a
phone starts working — and it exercises the request/response machinery Phase 3
depends on.

## Phase 3 — The command channel

**3.1** `deviceConnect` / `deviceDisconnect` (carrying ATR/ATQA/SAK) and
`deviceTransceiveRequest` / `deviceTransceiveResponse` (base64 payload,
per-exchange timeout). Two distinct capability bits, because they are genuinely
different hardware features: APDU transceive (`InDataExchange`,
`IsoDep.transceive`) and raw framing (`InCommunicateThru`, `NfcA.transceive`).

**3.2** `remotenfc.Tag.Transceive` on top, which lets remote devices reach the
existing `nfc/tag_*.go` logic.

Keep this strictly optional. The survey's round-trip argument (§5.1) applies to
*us* the moment we use it: a chatty sequence over WiFi is seconds of
tag-in-field time, and iOS sessions time out. It exists for the operations that
genuinely need it — DESFire, ISO-DEP applets, honest capability probing — not as
the default read path.

## Phase 4 — Identity and pairing

Replace the shared bearer secret with a per-device credential: agent-side
pairing window (tray button, "accept new device for 60s"), short OOB code, PAKE,
durable credential. Prefer PSK over X.509 for the MCU tier, and document that
firmware pins the **CA** — created once in `m.caDir` and reused — never the leaf,
which regenerates on every host IP change (`tls/manager.go`, `tls/netwatch.go`).
Device list with revoke in the tray. Keep the shared secret as a legacy path.

## Phase 5 — Reach (demand-driven, not scheduled)

- **Serial/USB transport** carrying the same envelope over COBS framing. The most
  common DIY topology, and the only path for the AVR tier. Physical attachment
  is the authentication, so Phase 4 does not apply.
- **VPCD compatibility listener** — ~50 lines of firmware on the device side,
  reuses the phone-side agent path. Promote ahead of Phase 4 if DIY readers
  become a priority.
- **USB CCID** — document only. An MCU presenting as CCID already works through
  `nfc/device_pcsc.go` with no davi protocol at all.
- **Relay for off-LAN** — only if a real requirement appears.

## Ordering rationale

Phases 1→3 are strictly ordered: 1.1 makes change possible, 1.2 makes capability
expressible, 2.1 makes it *truthful*, and only then does 3.1 have anything to
negotiate against. Phase 4 is independent of 1–3 and can move earlier if
security review demands it. Phase 5 is demand-driven throughout.

Each phase updates `docs/api.md`, `docs/javascript-client.md`, and `client/`
alongside the Go changes — the JS device client is the reference implementation
of this protocol, so a phase that does not update it is not finished.

## Explicitly not doing

Replacing the protocol with VPCD/CCID/remote-IFD; adopting MQTT or CoAP as
transport; NCI; LLCP/SNEP. Reasoning in §5 of the survey.
