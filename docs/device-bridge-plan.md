# Device Bridge — Improvement Plan

Execution plan following from [device-bridge-protocols.md](device-bridge-protocols.md).
That document concluded: the protocol is at the right layer, and what is wrong
with it is under-specification. This is the sequenced work.

## Status

Phases 1–4 are implemented. Phase 5 (reach) is not started.

Work toward a successor protocol has moved to
[device-api-v2-research.md](device-api-v2-research.md), which surveys the tag,
platform and hardware semantics this bridge projects, and records the design
rules that came out of that. Most of it is undecided; the exceptions are that
**v0 and v1 are to be removed rather than deprecated** when v2 lands, on the
device protocol only, and that a device's **`platform` field gives way to a
User-Agent-shaped self-description** that nothing may branch on. See §12 and
§13 there for the conditions that attach to each.

Phase 4 landed agent-side only: the agent issues per-device credentials and
publishes its key pin, but no client verifies the pin or calls `/pair` yet, and
neither the shared secret nor the loopback bypass has been withdrawn. It is
additive — nothing is enforced that was not enforced before.

Two bugs surfaced while building and were fixed in passing: `ServerBridge.Close`
closed channels producers could still be sending on, and `handleTagScanned`
published a scan before registering the route a write would need.

Two more surfaced in review afterwards. `lockRequest` had no device route at
all: it went straight to the hardware reader, which locks whatever card it is
holding, so a lock aimed at a phone-held tag was applied to a different tag
entirely — irreversibly. And read-only mode was enforced inside the reader's own
write path, which a request routed to a device never reaches, so writes and
locks escaped the mode while transceive alone honoured it. Both are fixed: lock
routes like a write, and the mode is checked once for all three operations on
both routes, including the `remotenfc.Tag` route that reaches devices through
`TagWriter`. Capabilities follow the mode too, so a tag never advertises an
operation the agent would refuse.

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
- **the device session layer** (then `server/deviceserver/device_handler.go`,
  now `nfc/remotenfc/server.go`) — `WSTypeDeviceWriteResponse`
  handling is `log.Printf("Write response received (not yet implemented)")`.
  The constants and payload structs exist; the path does not.

So the work is: fix the projection, remove the coupling, finish the stub, then
add the one genuinely missing layer.

## Phase 1 — Make the wire honest ✅

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
dropping off. The original rationale here — avoiding a 30s heartbeat wait on an
intentional disconnect — was wrong: socket close already unregisters
immediately, and the inactivity sweeper only ever covered half-open connections
where the socket survives but heartbeats stop. The real gap was that the agent
could not tell the two apart at all, so both logged the same line and reported
the same thing upward. Delivered as a `goodbye` frame plus close-code
inspection.

*Touches:* `protocol/websocket.go`, `protocol/device.go`,
`nfc/remotenfc/protocol.go`, `nfc/errors.go`,
the device session layer, plus `docs/api.md`, `client/`.

## Phase 2 — Decouple events from transceive, finish the write path ✅

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

## Phase 3 — The command channel ✅

**3.1** `deviceTransceiveRequest` / `deviceTransceiveResponse` (base64 payload,
per-exchange timeout). Two distinct capability bits, because they are genuinely
different hardware features: APDU transceive (`InDataExchange`,
`IsoDep.transceive`) and raw framing (`InCommunicateThru`, `NfcA.transceive`).

The planned `deviceConnect` / `deviceDisconnect` pair was dropped: a tag session
is already delimited by `tagScanned` and `tagRemoved`, and on phones the OS owns
the session, so the pair would have been ceremony with no consumer.

**3.2** `remotenfc.Tag.Transceive` on top, which lets remote devices reach the
existing `nfc/tag_*.go` logic.

Keep this strictly optional. The survey's round-trip argument (§5.1) applies to
*us* the moment we use it: a chatty sequence over WiFi is seconds of
tag-in-field time, and iOS sessions time out. It exists for the operations that
genuinely need it — DESFire, ISO-DEP applets, honest capability probing — not as
the default read path.

## Phase 4 — Identity and pairing ✅

Replace the shared bearer secret with a per-device credential: agent-side
pairing window (tray button, "accept new device for 60s"), short OOB code,
durable credential, device list with revoke in the tray. Keep the shared secret
as a legacy path.

### 4.0 First: stop installing a root CA system-wide ✅

The agent currently generates a root CA and installs it into the host's system
trust store on startup (`truststore.Install()` in `tls/manager.go`), and asks
every paired phone to install it too (`tls/bootstrap.go` serves an Apple
`.mobileconfig` and an Android cert page).

**A root CA in a trust store signs anything.** It is not scoped to this agent:
whoever holds `rootCA-key.pem` can mint a valid certificate for any domain and
intercept that machine's traffic. mkcert, which this is built on, documents
itself as a development tool for exactly this reason. On iOS, enabling Full
Trust for a CA is an all-or-nothing system-wide grant; on Android a user CA is
distrusted by apps by default, so the install is simultaneously invasive and
often ineffective. The per-install CA does mean there is no single key whose
theft compromises every user — that is the only thing limiting the blast
radius.

The CA exists solely because browsers demand WebPKI-shaped trust. Native
devices never needed it. So split trust by consumer:

**Native devices (phones, MCUs, custom clients) — no CA at all.** Serve a
self-signed leaf and have devices pin its SPKI hash, learned during pairing.
Because the pairing code carries the fingerprint, this is authenticated
trust-on-first-use rather than blind TOFU. No trust store, no prompts, no
system-wide grant. This is the SSH and CTAP-hybrid model.

*Prerequisite:* the agent's keypair must be stable. `MakeCert` calls
`generateKey` on every invocation, so today the leaf key is replaced whenever
the host's IPs change and any pin would break. Generate the agent keypair once,
persist it, and reissue only the certificate when SANs change — SPKI survives
reissuance as long as the key does. This is a small change worth landing before
pairing, since pinning depends on it.

**Browsers — the CA is the wrong tool.** Two workable options:

- *Same-machine only:* serve the frontend from `http://localhost` and skip TLS
  entirely; loopback is a secure context. Note Chrome 142 fully enforces
  Private Network Access, so an `https://davi.social` page reaching a local
  agent is gated on the new Local Network Access permission rather than
  silently allowed.
- *LAN or hosted frontend:* a publicly-trusted wildcard for a domain Davi
  controls, resolving to the agent's address — the `*.plex.direct` pattern.
  Zero install, works from an HTTPS page, no trust-store changes. Costs DNS
  infrastructure, and the key ships to clients so treat it as public and
  rotatable.

**If the CA stays as a fallback,** make it opt-in rather than automatic on
startup, expose `truststore.Uninstall()` in the tray and on uninstall, and name
it identifiably. Name constraints limiting it to the agent's own SANs are worth
adding as defence in depth, but enforcement for locally-added roots is uneven
across platforms, so do not rely on them.

### 4.1 Pairing ✅

Most of the primitive already exists: `tls/bootstrap.go` has a PIN, PIN
rotation, PIN-gated routes, and a QR flow. Phase 4 largely repurposes it — hand
out `{host, port, spkiHash, deviceToken}` instead of a CA to install, which
deletes the `.mobileconfig` and Android cert paths rather than adding to them.

For the MCU tier this supersedes the earlier note preferring PSK: pinning an
SPKI hash is a 32-byte comparison, cheaper than both X.509 path building and
PSK key management, and it leaves no symmetric secret on the agent per device.

## Structure

The device protocol now lives with the driver that speaks it. `nfc/remotenfc`
owns the WebSocket endpoint (`Manager.Handler`), the sessions behind it, and the
request/response correlation for writes, locks and raw exchanges, alongside the
device registry it already held. One object owns both, so a registration and its
connection cannot outlive one another.

`server/deviceserver` keeps what is the agent's rather than the driver's:
authentication, and the choice between the hardware reader and a remote device
for each client request.

## Phase 5 — Reach (not started, demand-driven)

- **Serial/USB transport** carrying the same envelope over COBS framing. The most
  common DIY topology, and the only path for the AVR tier. Physical attachment
  is the authentication, so Phase 4 does not apply.
- **VPCD compatibility listener** — ~50 lines of firmware on the device side,
  reuses the phone-side agent path. Promote ahead of Phase 4 if DIY readers
  become a priority.
- **USB CCID** — document only. An MCU presenting as CCID already works through
  `nfc/pcsc/device.go` with no davi protocol at all.
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
