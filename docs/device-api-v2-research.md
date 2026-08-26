# Device API v2 — Research Notes

Research toward freezing the current device protocol (v1) and designing its
successor. Almost nothing here is decided: this is the evidence and the model
that came out of it, written down so the design conversation starts from a
fixed text rather than from memory. The decisions taken so far are §12 to
§14.

It followed two documents that no longer exist: `device-bridge-protocols.md`,
a survey of prior art, and `device-bridge-plan.md`, which sequenced the phased
work that produced v1. Both were deleted from master once their work had
landed, so this file is now the only surviving one in that chain and is written
to stand alone. References below to "the survey" and to "Phase N" are
historical and resolve against the git history and the changelog rather than
against a file. Where those two were about *our* protocol, this one is about
the semantics underneath it — what the tags, the platforms and the
hardware actually offer, and therefore what a wire protocol can honestly carry.

## 0. Scope, and the test a design has to pass

Every protocol surveyed assumes a **homogeneous** population of holders. PC/SC
assumes readers attached to this machine. Web NFC assumes one browser. OSDP
assumes badge readers on an RS-485 bus. VPCD assumes one emulator per port.
ESPHome assumes it owns the device. None of them lets a phone, a USB reader, a
DIY ESP32 board and a badge reader be present at the same time, speaking one
vocabulary, addressed by a client that does not care which of them is holding
the tag.

That heterogeneity is the thing this agent is for, and it is the constraint the
design should be judged against:

> The same vocabulary must work unchanged for a badge reader, a phone and a
> PC/SC reader — and a client must not be able to tell which one is holding the
> tag.

**Which of those this document is about.** The agent reaches tags by three
separate paths, and only one of them is the device API:

| Path | What it serves | In scope here |
|---|---|---|
| **Device API** | holders that open a connection to the device server over the network — phones, browsers, ESP32-class boards, a bridge speaking for several readers | **yes** |
| PC/SC backend | readers attached to the machine the agent runs on | no |
| Serial | a board on a wire, with its own framing and its own answer to trust | no |

So the test above applies at the **client** boundary, across all three paths.
This document designs one of them: the network one. A card reader plugged into
the agent's host is not a device-API question, and neither is a board on a USB
cable — they are served by paths that already exist or will get their own.

## 1. What v1 did to v0

v1 is additive almost everywhere: the first frame's type selects the dialect
(`nfc/remotenfc/server.go`), `registerDevice` is still served, and every
capability field is `omitempty`. Three things were genuinely narrowed.

**The write options struct.** v0's wire `DeviceWriteRequest` carried
`Options nfc.WriteOptions` — the whole struct: `Overwrite`, `Index`,
`ForceInitialize`, `ExpectUID`, `SkipVerify`, `MaxWriteAttempts`,
`SkipCapacityCheck`, `Lock`. v1 replaced it with `Lock bool` plus
`NDEFBytes` / `IdempotencyKey` / `TagUID` (`nfc/remotenfc/wire.go:115`), and the
call sites hard-code the rest: `nfc/remotenfc/tag.go:207` and `:227` pass
`{Overwrite: true, Index: -1}`, and `writeTag` forwards only `opts.Lock`
(`nfc/remotenfc/requests.go:357`). Append-at-index, force-initialising a
Classic card, and retry/verify policy are now unreachable over the bridge.

The honest qualifier: v0 never implemented the write path at all, and the
client API does not expose those options on the reader route either
(`server/handlers.go` also hard-codes `{Overwrite: true, Index: -1}`). This was
headroom, not a working feature — but the shape was right, and the wire is now
narrower than the internal model it projects.

**The write outcome.** Neither version's `deviceWriteResponse` carries a
result, so the agent manufactures one (`nfc/remotenfc/deviceops.go:46`):
`BytesWritten` is what we sent rather than what landed, `Attempts: 1` is an
assumption, and `Verified` comes from a `confirmable()` helper that is just
`!ReadsAreSnapshot`.

`Verified` itself comes out right, and it is worth being precise about why:
`remotenfc.Tag` declares `ReadsAreSnapshot = true` unconditionally
(`nfc/remotenfc/tag.go:112`), so `confirmable()` is always false on this route
and a device write is never reported as verified. The defect is narrower than
"it lies" — it is that the device never says what it did, and the agent fills
the gap with values it did not observe. A device that verified its own write
has no way to say so, and one that needed three attempts is recorded as having
needed one.

**The `platform` allowlist** (v0 required `ios`/`android`/`web`) was removed in
`#28`, correctly — it was the smartphone assumption encoded as an admission
test, and it refused the bundled client's own `"node"` example.

The CA install path is a different story than the docs suggest: Phase 4.0
landed (self-signed by default, CA opt-in behind `-install-ca`,
`tls/manager.go`), and `docs/api.md` dropped the install instructions. What has
no replacement is the browser case — a browser cannot pair and cannot pin, so
it is still CA-or-hosted-cert, gated only by the origin allowlist.

### 1.1 Pairing, as it actually shipped

Per-device revocation is the right requirement and a shared secret cannot serve
it. The mechanism has five gaps worth recording before v2 touches any of it:

1. **`/pair` is plain HTTP.** `tls/bootstrap.go` starts the bootstrap server
   with `ListenAndServe` on 9472. The PIN travels in the query string, and the
   response carries both the device token and the `publicKeyPin`. The pin is
   the anti-MITM measure and it is delivered over the channel it exists to
   protect. The QR — a genuine out-of-band channel, since it is shown on the
   kiosk — currently encodes `http://host/install?pin=…` and no key hash.
2. **The credential is a bearer token, not a key.** Nothing binds it to a
   device. The survey recommended TR-03112-6's OOB-code → PAKE → per-device
   credential; what shipped went straight to a shared-secret-per-device.
3. **Paired identity is discarded at the door.** `CheckAuth` and
   `CheckPairedDevice` threw away the returned device ID and `RegisterDevice`
   minted a fresh UUID per connection, so the tray's paired list and the
   connected-device list were separate identity spaces. **Fixed on master in
   `#40`**; see §14 item 3.
4. **Revocation is not immediate.** Auth is checked once at upgrade; `Revoke`
   does not close live sessions.
5. **Headless devices cannot pair** without an operator relaying a PIN. See
   §7.4 — OSDP's install mode is the standard's answer to this.

## 2. The agent runs three tag models

| | Local reader | Device bridge | Client API |
|---|---|---|---|
| Arrival | poll ~100 ms, `HasChanged(uid)` — "UID differs from last" | explicit `tagScanned` | `tagData` broadcast |
| Removal | **inferred**: last seen >1 s ago (`nfc/cache.go:57`) | explicit `tagRemoved`, **with UID** | a nil tag — names the *holder*, not the tag |
| Concurrency | `soleTag` refuses >1 tag (`nfc/reader.go:1144`) | one tag per device; `setActiveTag` replaces | n/a |
| Presence | `deviceStatus.cardPresent` | none — device-level only | the reader's only |

The bridge has the better lifecycle — it is the only source that knows a tag
left, and which one — and the agent narrows it on the way out:
`SendTagRemoved` receives `data.UID` and broadcasts a nil tag
(`nfc/remotenfc/manager.go:259`). Master's `ScannedTag` now carries a `Device`,
so a removal says which holder it came from — but still not which tag left, so
a client watching a phone cannot tell whether the departed tag was its own.

### 2.1 Where this is going: edges at the device, state at the agent

The reader's column of that table is the agent reconstructing something its
backend already knew. `nfc/pcsc/device.go` runs a `GetStatusChange` monitor
with an event state and an event counter and a `cardRemoved` channel (`:24`,
`:99`), and uses it only for removal detection inside the device while
`NFCReader` polls and re-derives presence above it. Driving the reader from
those edges and deleting `TagCache` collapses the first two columns into one
shape: every holder becomes an event source.

**Presence is three layers, and `TagCache` smears all of them.**

| Layer | Owner | What it is |
|---|---|---|
| Field | the device | edge detection, and whatever debounce it applied |
| State | the agent | what each holder is holding now, derived from those events |
| Delivery | the wire | event identity, so a replayed delivery dedupes exactly |

The agent keeps the middle layer — `ActiveTag`, `GetTags` and `resolveRoute`
all need it — and gives up the first entirely.

**Mechanism belongs to the device; the policy it applied must be declared.**
Edge detection is physical and only the device can observe it. Debounce is
policy, and a holder that picks its own silently makes the event stream mean
different things depending on which holder produced it — exactly what §0 says a
client must not be able to detect. So a device reports its events *and* the
policy behind them: a debounce window, or a presence model (edge-triggered,
polled at interval N, unknown).

**No second debouncer at the agent.** Two filters in series, with different
windows and phases, produce behaviour neither of them designed, and the failure
is silent event loss. That is unacceptable wherever the same credential is
presented repeatedly and each presentation counts — apartment time-in and
time-out, event check-in and check-out. A duplicate can be recognised by its
event id; a tap that was never delivered cannot be recovered at all.

**Presence is not hold mode.** Both are declared by the device and neither is
determined by the agent, but they answer different questions: hold mode says
what the agent may ask for (§9), presence says what the event stream means.
Hold mode stays advisory and never a gate.

### 2.2 Three cautions for that change

Recorded because they are cheap to trip over:

- **`SupportsEvents` carries a coupling.** A PC/SC device that starts declaring
  events makes `BuildDeviceCapabilities` (`nfc/capabilities.go:127`) set
  `CanPoll = false` *and* `CanTransceive = false`, and the PC/SC device
  implements neither `DeviceEventEmitter` nor `DeviceTransceiver` today.
  `CanPoll` has no readers, so that half is inert; the other half would have
  hardware readers declaring they cannot transceive. `BuildDeviceCapabilities`
  has no internal consumer at present, so it is wrong on paper immediately and
  wrong in practice as soon as something reads it — and it is a documented
  extension surface. A one-line `SupportsTransceive()` in the same change
  closes it.
- **Order matters.** `HasChanged` is what gates the broadcast
  (`nfc/reader.go:456`). Removing `TagCache` before the trigger is edge-driven
  leaves the reader broadcasting on every poll.
- **Arrival and read separate.** `handleTagPolling` detects and reads in one
  step, so a tag that arrives but cannot be read surfaces as an error carrying
  a Card. Splitting them is more honest, and matches what the wire already does.

## 3. The device protocol is announce-only; every native API is query-based

This is the structural finding. Our wire has three commands (`write`, `lock`,
`transceive`) and no questions. A device volunteers capabilities once at
`hello` and once per `tagScanned`, and is never asked anything again.

- **CoreNFC**: `queryNDEFStatus()` → `(notSupported | readWrite | readOnly,
  capacity: UInt32)`, then `readNDEF` / `writeNDEF` / `writeLock`.
- **Android**: `getType()` (`org.nfcforum.ndef.type1..4`, or
  `com.nxp.ndef.mifareclassic`), `getMaxSize()`, `isWritable()`,
  `canMakeReadOnly()`, `getNdefMessage()` vs `getCachedNdefMessage()`,
  `writeNdefMessage()`, `makeReadOnly()`.
- **Web NFC** — the floor: `scan` / `write(overwrite)` / `makeReadOnly`, and
  nothing else. Explicitly out of scope: low-level ISO-DEP and NFC-A/B/F I/O,
  HCE, **tag removal events**, tag type and capacity queries.
- **OSDP**: `CMD_ID` → `REPLY_PDID`, `CMD_CAP` → `REPLY_PDCAP`.

So the client-facing `capabilitiesRequest` has no device-side counterpart: for
a phone-held tag it is answered from what the device volunteered at scan time,
not by asking the tag. No serious reader protocol is announce-only.

## 4. Capability is three questions flattened into one struct

`tagProfile` (`nfc/tag_profile.go`) is the best idea in the codebase —
capabilities declared *next to the driver that must honour them*, with
`AssertCapabilitiesConsistent` to stop drift. But three distinct questions get
flattened:

1. **What the bridge can carry** — `canWrite` / `canTransceive` / `canLock` /
   `maxHoldMs`. A property of the device.
2. **What this kind of tag supports** — memory, max NDEF, family, technology,
   crypto, password. A property of the *type*, and agent-side it is still
   recovered by substring-matching a display name in `InferTagCapabilities`
   (`"mifare classic"`, `"ntag2"`, …).
3. **What this tag is right now** — `isReadOnly`, real free capacity,
   formatted-or-not. A property of the *instance*; only the holder can answer.

The three-valued logic `#28` had to retrofit (`Device.DeclaredCapabilities`
returning `(caps, declared)`) exists because the wire had no way to say "I do
not know". That correction should become general: **every capability answer is
yes / no / unknown, and is attributed to a level.**

`ReadsAreSnapshot` is well chosen and validated by Android — `getCachedNdefMessage()`
returns what was captured at discovery, `getNdefMessage()` "always reads the
current NDEF Message… may cause RF activity". But Android offers *both*, so
snapshot-ness is a property of **a read**, not of a tag. Modelling it as a
static per-tag boolean is why the write path cannot confirm anything.

## 5. Sessions and holds are real, and unmodelled

Verified: an iOS session is capped at **60 s** from `begin`, and a connected tag
drops at roughly **20 s** — a hard limit that cannot be extended. iOS reports
these as two different errors: `Code=100 "Tag connection lost"` and
`Code=201 "Session timeout"`.

Three nested lifetimes exist — device connection, scan session, tag hold — and
the wire models the first (heartbeat 30 s / timeout 90 s) and part of the third
(`maxHoldMs`, advisory). There is no error code for "my session ended", which
is neither `TAG_REMOVED` nor `DEVICE_GONE`. And `retryableCodes` marks
`TAG_REMOVED` retryable when it is only retryable *after the user taps again* —
a different class from "retry now".

## 6. Identity: UID is load-bearing and unsound

`resolveRoute` (`server/clientserver/tagresolve.go:33`) is good design — it resolves
*which holder has this tag* at request time rather than preferring a source.
But it addresses by UID, and UID is not an identity: MIFARE Classic and DESFire
ship random IDs, Web NFC's `serialNumber` may be the empty string, and a
Wiegand reader has no UID at all. Meanwhile every platform hands out a handle —
Android's `Tag`, CoreNFC's tag-in-session, PC/SC's card handle — valid exactly
while the tag is in the field. Our wire discards it and re-derives from a hex
string.

### 6.1 Addressing and data are different questions

Handles-versus-UIDs is about **addressing**: how a request names the tag it
applies to. It is not about whether the UID is available. A scan event carries
the credential either way, so an application that wants the UID as a *value* —
to key a record, or to compose a URL — is unaffected by the choice.

```jsonc
// UID addressing — "whoever is holding this UID"
{"type":"writeRequest","payload":{"uid":"04:A2:24:52:9F:5C:80","records":[…]}}

// handle addressing — this tag, this holder, this presentation
{"type":"writeRequest","payload":{"tagRef":"t_8f21c…","records":[…]}}
// the scan event still carries {"credential":{"kind":"uid","value":"04a224529f5c80"}}
```

Two failure modes motivate the handle. **Two holders, one UID** — a cloned
card, a non-unique 4-byte UID, or the same card moved between readers — and
`deviceHoldingUID` takes the first match in iteration order. And **lift and
re-tap**: between the scan and the write the card leaves and returns, the UID
still matches, but it is a different presentation, so a late write lands on the
second tap. A handle goes stale with the presentation, so the operation fails
loudly instead of applying somewhere else.

### 6.2 A UID as an application identifier

Distinct from addressing, and worth writing down because applications do this —
writing `example.com/c/$uid` onto a card is a real use case here. Three
hazards:

- **Spelling.** Our canonical form is colon-separated uppercase
  (`ParseUID`, `nfc/remotenfc/convert.go:12` → `04:A2:24:52:9F:5C:80`), Android
  hands out a `byte[]`, ESPHome prints `74-10-37-94`, PC/SC gives raw bytes. A
  mismatch between whoever writes the value and whoever looks it up is silent
  and surfaces much later as "not found". Pick one canonical spelling for
  application use — lowercase hex, no separators — and normalise at the edge.
- **A UID is not an authenticator.** 4-byte UIDs are explicitly non-unique
  under ISO/IEC 14443-3, and UID-writable cards will present whatever is asked
  of them. Adequate as a lookup key, never as proof the card is genuine; that
  needs the card to demonstrate something (a password, a signature).
- **Random UIDs.** DESFire and Plus can be configured to emit a fresh
  identifier per activation, so a value captured once stops matching the card.

### 6.3 A write path that never needs the UID

NTAG213/215/216 can mirror their own UID into the NDEF message at read time.
With `MIRROR_CONF = 01b`, `MIRROR_PAGE > 03h` and `MIRROR_BYTE` selecting the
offset, the tag substitutes its UID as exactly **14 ASCII bytes** at that
position on every read (the NFC counter mirror adds 6 more, 21 with both — 14
plus an `x` separator plus 6), and the mirror must not cross the user-memory
boundary.

The consequence is a production flow with no personalisation step: write one
identical NDEF to every card and each serves its own URL, with the writing
device never learning any UID. It is NTAG21x only, it is a configuration-page
write — the same class this codebase currently gates off pending hardware
validation (`nfc/password.go`) — and the mirrored value's hex case should be
confirmed on real hardware before anything depends on it.

This converges with §7.2 from the other direction: "write this NDEF to the next
card presented" is the same armed-intent primitive the badge-reader tier needs,
and it is what bulk card production wants too. The IoT constraint is not a
special case being accommodated; it is a primitive worth having anyway.

## 7. The IoT tier

### 7.1 It is not a smaller phone

| | Phone | PC/SC reader | ESP32 + PN532 | Badge reader (Wiegand/OSDP) |
|---|---|---|---|---|
| Tag hold | bounded (~20 s) | until removed | until removed, while it polls | **none** — a tap is ~200 ms |
| Identifier | UID | UID + ATR | UID + ATQA/SAK | facility code + card number, **no UID** |
| NDEF | OS-provided | agent-side drivers | **firmware-dependent** | absent |
| Who initiates | device pushes | agent polls | either | panel polls |
| Outputs | alert text, vibrate | LED/buzzer (ACR122) | LED/buzzer/relay | LED/buzzer/text/relay, standardised |

### 7.2 The tag is not held, and writes are a latched mode

ESPHome's PN532 component is the reference implementation of this tier.
`on_tag` fires only when the tag "is changed or goes away for one cycle of
`update_interval`" (default 1 s), with `on_tag_removed` on departure — that is
presence-by-timeout and dedup-by-change, **the same model as our own
`TagCache`**, arrived at independently.

But writing is not a request against a held tag. It is `write_mode(message)`:
the board is *armed*, and writes the next tag it sees. There is no addressable
tag to target, because by the time a request crosses the network the badge is
gone.

This is the largest finding. v1's command model — `{requestID, tagUID}` →
response, correlated against a tag the device is currently holding — is
structurally unusable on this tier. The primitive that works is a **standing
intent**: "the next credential you see within N seconds, do this", answered
later by an event. Phones and readers can express holds; badge readers can only
express intents.

### 7.3 What OSDP models that we do not

- **The panel polls; the reader replies.** Card reads arrive as `REPLY_RAW` in
  answer to `CMD_POLL`. Events are pull-delivered, which is what a constrained
  or half-duplex link can support.
- **Identity and capability are query verbs** (`CMD_ID`/`CMD_CAP`).
- **Outputs are first-class**: `CMD_LED`, `CMD_BUZ`, `CMD_TEXT`, `CMD_OUT`
  (relay/strike). The reader is an actuator, not only a sensor.
- **`REPLY_BUSY` and `REPLY_NAK`** — explicit backpressure and negative ack.
  We have a 10-deep channel (`nfc/remotenfc/manager.go:66`) and then
  unspecified behaviour.
- **`CMD_MFG` / `REPLY_MFGREP`** — a sanctioned vendor-extension channel, so
  proprietary features do not fork the protocol.
- Card data is a **raw bit array of declared length** — not a UID, not NDEF.

### 7.4 Screenless pairing has a standard answer

OSDP's **install mode**: a PD with no key set accepts a secure channel using
**SCBK-D**, a publicly known default key, purely so the panel can `CMD_KEYSET`
a unique per-device key — after which the device auto-disables install mode and
accepts only the real key. The documented caveats are exactly the ones we would
inherit: communications are *not* secure during install, it must happen in a
controlled environment, and the key must be unique per device.

That is the agent-side "accept new device for 60 s" window from Phase 4.1,
validated by a standard, including the honest security statement. Alongside it,
**Improv Wi-Fi** (BLE + serial) is the de-facto way an ESP joins a network at
all, and **ATECC608 / ESP32-WROOM-32SE** is how a device holds a key it cannot
leak. Identity gets provisioned over the wire the device was flashed on — never
typed into a screen.

### 7.5 Resource numbers that constrain the wire

- A TLS handshake needs **40–50 KB of free heap**; `mbedtls_ssl_setup`
  allocates **~23 KB per connection**; mbedTLS defaults to 16 KB record
  buffers, **32 KB in RAM** for RX+TX. Optimised builds land ~40 KB steady with
  ~64 KB peaks.
- ESP32 (~320 KB usable) copes. ESP8266 is marginal — TLS is *the* pinch point,
  and max-fragment-length negotiation is the lever. AVR is out entirely.

Consequences: frames must fit a 2–4 KB max fragment (base64 raw-data dumps are
hostile), and the device endpoint currently sets **no read limit at all** —
only the web UI does (`agent/console/server.go:250`, 4 KB). `-auto-tls=false` already
gives a plain-`ws://` path, which is the honest escape hatch for a constrained
board on a trusted network.

### 7.6 Capability is a firmware property here

The PN532 lists at most **two targets** at once (`MaxTarget` = One | Two), does
APDU exchange via `InDataExchange` and framing-level exchange via
`InCommunicateThru`. Note that `InListPassiveTarget` does not report what kind
of tag it found — the .NET binding's own guidance is "you can't determine which
target you've read… prefer the AutoPoll function, as the type identified is
returned".

More important: **NDEF lives in the Arduino/ESPHome library, not the chip.**
Whether a PN532 board can read NDEF, write NDEF or lock a tag is a property of
the firmware build, and it changes with an OTA update. Capability on this tier
is not a static hardware fact declared once at `hello`.

## 8. OSDP: take the answers, not the wire

Every structural feature of OSDP exists because it runs on RS-485 multidrop:
half-duplex, shared bus, no collision detection, so a peripheral may not speak
unless spoken to. That is where the polling, the 7-bit bus address and the
SOM/LEN/CTRL/CRC framing come from. Over TCP none of it buys anything, and the
polling actively hurts — a CP polls every 50–200 ms, which over Wi-Fi is a
constant packet stream at a device that could have stayed asleep.

The data model does not fit either: `REPLY_RAW` is a bit array plus a format
code; there is no NDEF, no tag capability, no write, no lock. Writing an NDEF
message to an NTAG has no OSDP command, so every davi operation would travel
through `CMD_MFG` — inheriting the framing and none of the semantics. Three
smaller reasons: the secure channel is a symmetric per-device key the agent
must *store* (backwards from Phase 4, which keeps only hashes); "OSDP" without
SIA conformance is a claim to defend; and nothing in the device population
speaks it — not phones, not browsers, not ESPHome, not any Arduino NFC library.

This is the same conclusion the survey reached about VPCD and CCID, and it
generalises: **foreign protocols belong at the edge, as adapters.** If OSDP
matters later it belongs in that slot — an adapter, or davi presenting itself
as a PD — not as a migration.

What to take: capability-as-a-query, output verbs, BUSY, install-mode pairing.

## 9. The model

Four levels, each with its own identity, lifetime, capability set and error
class:

1. **Bridge** — the connection. Identity: the *paired* device, not a
   per-connection UUID. Carries what this device can do at all, the protocol
   version, and the output channel. Capability must be re-announceable
   mid-session (§7.6) and survive a gateway speaking for several readers.
2. **Hold mode** — declared by the device, replacing the advisory `maxHoldMs`:
   `until-removed` (reader, polling board) | `bounded` (iOS, ~20 s) | `brief`
   (Web NFC) | `none` (badge reader). **This is the axis everything else hangs
   off**, because it decides whether operations are commands or intents.
3. **Hold** — one tag in the field, where the device has holds at all.
   Identity: an opaque holder-issued `tagRef`; the credential is an attribute,
   not the address. Ends with a *named* departure.
   Where hold mode is `none`, this level is replaced by **armed intents** with
   a scope and an expiry, answered by an event.
4. **Operation** — read / status / write / lock / exchange / signal, against a
   `tagRef` or an intent. Correlated, idempotent, bounded, and answered with a
   real result — including an explicit BUSY.

Cutting across all four: **the credential is not necessarily a UID.** It has a
declared kind — `uid` | `wiegand(bits)` | `opaque` — where UID is one case
rather than the assumed one. Today the wire rejects a device that cannot supply
both `uid` and `technology` (`nfc/remotenfc/protocol.go:51`), which a Wiegand
bridge or a Home Assistant tag source (an opaque `tag_id` and nothing else)
can never do honestly.

## 10. Design rules

Checkable rules, each traceable to something above.

**Capability and negotiation**

1. Every capability answer is three-valued: yes / no / **unknown**. A missing
   boolean must never read as a refusal (§4).
2. Attribute each capability to a level — bridge / kind / instance (§4).
3. Capability is mutable: re-announceable and re-queryable mid-session (§7.6).
4. Declaring is opting in — **the agent never sends a device a message type it
   did not declare**. This is what makes a small floor real: a badge reader
   implements two outbound messages and zero inbound ones.
5. Version negotiation and capability negotiation are different mechanisms.
   v1 built the first and still lacks the second (§3).

**Identity and addressing**

6. Address tags by a holder-issued handle, not by an identifier we do not
   control (§6).
7. The identifier is a typed credential, not a UID (§9).
8. Device identity is not connection identity — required for revocation that
   bites mid-session, and for gateways (§1.1, §9).

**Lifecycle and time**

9. Model the holder's time budget explicitly, as a mode rather than a number
   (§5, §9).
10. Where hold mode is `none`, operations are armed intents, not commands
    (§7.2).
11. Events name their subject: a departure says which tag left (§2).

**Operations and failure**

12. Report outcomes; never synthesise them (§1).
13. Freshness is a property of the read, not of the tag (§4).
14. Errors carry a class, not just a code: permanent / retry-now /
    retry-after-re-present / session-ended (§5).
15. Backpressure is a protocol element (§7.3).
16. Outputs are peers of inputs — LED, buzzer, text/alert, vibrate. This is
    what makes a phone a *good* device rather than a degraded reader, and it is
    available on every tier.
17. Bind the envelope to more than one *network* transport, and bound it in
    both directions. An HTTP POST of the same `tagScanned` JSON is ~15 lines on
    any board and survives deep sleep (§7.5). Serial is a separate path with
    its own framing, not a binding of this API (§0).
18. Give extensions a sanctioned channel; ignore unknown fields; answer unknown
    message types with a typed error (§7.3).

**Self-description**

19. The device's account of itself is descriptive and inert: User-Agent-shaped,
    bounded in length and character set, never branched on, and kept out of the
    types the deciding code can see (§13).

**Presence**

20. Edge detection belongs to the device, and the debounce policy it applied is
    declared rather than assumed. The agent holds state derived from those
    events and never filters them a second time (§2.1).

## 11. What to resist

- **A second, "simple" protocol.** Two implementations, two test matrices, and
  a cliff the device falls off the moment it needs one more feature. Profiles
  by declaration, one protocol.
- **Friendliness through looseness.** v1's problems came from
  under-specification, not from being custom. A small mandatory core with
  *strict* optional extensions is friendly; tolerating anything is not.
- **A wire richer than any device can honestly answer.** Every field needs a
  holder that can fill it truthfully, or it becomes another synthesised
  `Verified`.
- **Policy in the protocol.** Pairing, read-only mode and origin rules are
  agent-edge concerns; keeping them there is why pairing-as-an-HTTP-endpoint
  was the right call even though its mechanism needs work (§1.1).
- **Designing for the phone and treating everything else as degraded.** That
  assumption produced `smartphone:{id}`, the platform allowlist, and a
  manager-wide `RemoteDevices() → true` that locked a real PN532 reader out of
  ever being a reader. The last of those has since been fixed on master, in
  exactly the direction rules 1 and 2 argue for: `Manager.Devices` returns a
  `DeviceListing` per device carrying what the driver knows before it is
  opened, and `CanPoll` gates opening, so a device that reports its own scans
  is excluded by saying what it is rather than by the agent inferring it from
  the kind of manager it came from (`nfc/manager.go:25`).

The last one is the summary: v1 is a phone protocol that other devices are
allowed to use. v2 should be a holder-agnostic protocol that phones happen to
be one instance of.

## 12. Decided: v0 and v1 are removed, not deprecated

**Scope: the device protocol only.** The client protocol served on the same
port is untouched by this. It has consumers — the Control Center,
`client/test-client.html`, and any frontend talking to the agent — and a
different risk profile. `protocol/` holds both and the boundary is not obvious
from the package layout, so it needs stating before any deletion sweep.

### The premise, as checked

- `@davi/nfc-agent-client` is **not published to npm** (the registry returns 404), so
  the bundled JS client has no package consumers.
- Agent binaries have shipped publicly since **v1.0.0 (January 2026)**.
  `v1.0.0`–`v1.0.3` are **v0-only**; the v1 dialect first ships in `v1.1.0`
  (August 2026). `docs/api.md` has published the wire throughout.

So "no consumers" is a claim about the world rather than about this repository.
It is the product owner's call, and this section records it as taken.

### Why removal rather than a deprecation window

Compatibility with zero users is pure cost, and it has already distorted the
design: §1 is largely a list of things v1 got wrong *because* it had to be
additive — the write options collapsed to a single `Lock bool`, and the
outcome synthesised agent-side because the response shape could not change. A
v2 designed under the same constraint inherits the same shape.

The surface is not two constants either. It is the dual-dialect
`awaitRegistration` and `handleRegisterDevice`, `DeviceProtocolV0` and
`NegotiateDeviceVersion`, `nfc/remotenfc/v0capabilities_test.go`, the device
client's `protocolVersion >= 1 ? 'hello' : 'registerDevice'` branching, and a
section of `docs/api.md`. The client library was rewritten in TypeScript in
`#32`, but `client/nfc-device-client.js` was left as it was and still carries
that branching.

And the freeze only means something if the old dialect actually goes. v1 is
described here, preserved in git history, and shipped in tagged releases;
nothing is lost that cannot be recovered.

### Conditions

**1. Delete the versions, keep the versioning.** Phase 1.1 existed so that a
protocol change would not need a flag day, and it would be self-defeating to
spend that mechanism on its first use and then remove it. Keep
`Sec-WebSocket-Protocol`, move the token to `davi-nfc-device.v2`, and change
the default: today an absent or unrecognised subprotocol falls through to v0
forever (`VersionFromSubprotocol` returns 0, and the first frame's type
re-selects the dialect regardless). **That silent fall-through is the thing to
remove**, not the mechanism around it. Absent or unknown becomes a typed
refusal.

**2. Do not delete what v0 forced us to discover.** Four things read like
legacy scaffolding and are in fact the design the rules in §10 now require:

- **Three-valued capability** (`DeclaredCapabilities` returning
  `(caps, declared)`) exists because a v0 device says nothing about itself —
  but it is rule 1. Keep it and generalise it.
- **`InferTagCapabilities`** serves Cards decoded from the wire and tags that
  are gone, not only v0 devices. Keep it; make it unreachable from live device
  paths.
- **`platform` is descriptive, not an admission test** (`#28`). Keep.
- **`maxHoldMs` is advisory and never a gate.** That is the seed of hold mode
  (§9).

**3. One commit, landing with v2 — not before it, not after.** Before leaves
the agent with no device protocol; after leaves two live paths and a standing
temptation to keep them. It should be a real deletion: constants, structs,
tests, the `docs/api.md` section, and the JS client rewritten as v2-only rather
than gaining branches — its fallback logic teaches implementers the wrong
shape.

### Two things that follow

**Make the refusal informative.** This is the cheap insurance in place of
keeping v0: a device offering nothing, or offering `davi-nfc-device.v1`, gets a
close frame naming what the agent speaks and where to read about it, rather
than a connection that hangs or half-registers. It costs about ten lines and
turns a mystery into a support answer for anything built against a January
binary. It pairs with finally publishing the **device protocol version in the
mDNS TXT record** — currently `version=1.0`, which is the agent's version, not
the protocol's (the survey's T0 asked for this and it was never done) — so a
headless board can discover the mismatch before it connects.

**Spend the whole break budget.** If this is the one hard break, it should be
the complete one: credential kinds, tag handles, hold mode, query verbs,
outputs, error classes — everything in §10. A partial break needing a second
break in six months is the worst outcome, and it is the tempting one, because
each item looks individually deferrable.

## 13. Decided: self-description is a User-Agent string, not a platform enum

v0 required `platform` to be `ios`, `android` or `web` and refused registration
otherwise; `#28` removed the allowlist, leaving a free-form string that nothing
branches on. v2 replaces the field outright with a **User-Agent-shaped**
self-description, folding `appVersion` into it.

The shape is RFC 9110's: an ordered list of `product/version` tokens with
parenthesised comments.

```
DaviDevice/2.0 (esp32; pn532; fw 1.4.2) ArduinoJson/7.0
DaviDevice/2.0 (ios 18.2; iPhone15,2) CoreNFC/1.0
DaviDevice/2.0 (web; Chrome/131) WebNFC/1.0
DaviDevice/2.0 (linux/amd64; acr1252u) libdavi/0.3
```

It extends without a registry, reads well in a log and a device list, and
carries the version of the *implementation* beside the platform — which
`platform` and `appVersion` did separately and incompletely. Firmware version
in particular matters on the IoT tier, where capability follows the build
(§7.6).

**And it must be inert.** The User-Agent is the web's own cautionary tale:
sites branched on it, so every browser learned to lie, and the platform ended
up at capability detection instead. This protocol already made the small
version of that mistake, so the rule attaches to the field permanently:

> Nothing in the agent may branch on the self-description. Behaviour follows
> declared capabilities (rule 4), never the string.

Enforce that structurally rather than by comment: keep the string out of any
type the routing and capability code can see. It belongs in the descriptor the
console and the logs read, not beside `Capabilities`. A field the deciding code
cannot reach cannot be sniffed.

**Bound it.** This is device-controlled text that reaches the logs, the tray
and the Control Center's device list (`agent/console/state.go:48`), so: a maximum
length — real User-Agent strings grow without limit — printable ASCII only per
RFC 9110's `token` and comment rules, no control characters and no line breaks.
Truncate rather than refuse: a malformed description is not a reason to turn
away a device that otherwise speaks the protocol.

**It does not negotiate.** Protocol version stays in the subprotocol token and
the handshake (§12); `DaviDevice/2.0` inside the string is informational, and
treating it as a version would rebuild the sniffing problem the field exists to
avoid. What it is genuinely good for is knowing what is deployed — the question
that would have made §12's removal decision a measurement rather than a
judgement.

## 14. Decided: pairing, for an attended deployment

**Assumption: a person is present when a device is added.** They can see the
kiosk screen and they are holding the device. Unattended provisioning — a board
shipped to a site and plugged in by someone who never sees the agent — is
deferred rather than rejected, and the note at the end of this section says
where that has to be recorded in the code.

### The two problems being solved

Pairing today conflates them, which is why it sits awkwardly:

- **Agent authenticity** — "is this the agent I think it is?" Answered by the
  SPKI pin, learned out of band. Half-built already (`PublicKeyPin` in
  `ServerInfo`).
- **Device authorization** — "may this device connect, and can it be revoked on
  its own?" Answered by a per-device credential.

Two provisioning channels serve the population this API carries: an
**on-screen QR** for a phone, and an **operator-gated window** for a headless
network device. Browsers have neither, and are gated by the origin allowlist
instead. Physical attachment authenticates itself, but that is the serial
path's answer rather than one of this API's (§0).

### What gets built

**1. The QR is the root of trust.** It carries `{host, port, spkiHash, code}`,
and the device pairs **over TLS pinned to that hash**. This closes the
cleartext hand-off (§1.1, gap 1) with no new cryptography, because the channel
is authenticated by something that never crossed the network — the SSH and
CTAP-hybrid model.

The condition that makes it true: the QR must be **rendered on the kiosk** —
tray or Control Center — and read off the screen. It is currently fetched from
`/qr.png` over HTTP, and a phone fetching it over the network destroys the
out-of-band property the whole design rests on.

**2. `/pair` moves to the agent port.** 9470 already carries TLS with the
certificate the pin covers. The bootstrap server then serves only the
human-facing setup page, and could eventually become a Control Center route.
Fewer ports, one certificate, and the pairing endpoint sits where the device is
about to connect anyway.

**3. Session identity is paired identity — landed.** The agent resolves the
presented credential to the paired device ID and registers the device under it
rather than minting a fresh UUID per connection, so a returning device meets
its own previous session and the console stops showing paired devices offline
while they are connected. Implemented on master in `#40`: `CheckPairedDevice`
now returns the identity it admitted (`server/auth.go:92`). Gap 3 is closed.

**4. Revocation closes live sessions.** The registry's `OnChange` already
exists; wiring it to drop sessions whose device was revoked closes gap 4.
Without it, "revoked" means "revoked at next connect".

**5. Credential type is declared, not assumed.** A bearer token is the floor; a
proven-possession keypair is preferred where the platform has somewhere safe to
keep it — Secure Enclave, Android Keystore, ATECC608. The distinction is worth
stating because a keypair is *not* unconditionally better: against interception
the pinned TLS already closes the hole, and a key sitting in plain flash is no
better than a token in plain flash. It wins where there is a keystore, which is
the tiers that matter.

**6. Browsers do not pair**, and this is documented rather than left implicit,
so nobody builds half of one. The origin allowlist is their gate.

### What is not being built

**PAKE, at least not first** — a departure from Phase 4.1 and from the survey's
recommendation, on the grounds that the QR changes the calculus. PAKE earns its
complexity by turning a *short, low-entropy* secret into a strong key over an
*unauthenticated* channel. Once the QR carries the SPKI hash the channel is
authenticated and the code can simply be high-entropy, so the benefit largely
evaporates. It remains the right answer for a typed-code fallback if that path
turns out to matter; the existing rate limit and five-attempt lockout covers
most of that risk for a LAN product in the meantime.

**Credential expiry.** A device that stops working after ninety days is a
support call, not security. Explicit revocation plus `lastSeen` for spotting
stale entries, which is the shape already there.

### Sequencing

Most of this does not need the protocol rewrite, and holding security fixes for
one would be the wrong trade:

- **Now, independent of v2:** the QR carries the SPKI hash and pairing moves to
  pinned TLS on the agent port, and revocation closes live sessions. Session
  identity has already landed (item 3).
- **With v2:** `pairing=required` in the mDNS TXT record, the informative
  refusal (§12), and the device declaring its credential type in the handshake.
- **Demand-driven:** keypair possession proof, and the operator-gated install
  window.

### Deferred: unattended provisioning

Not designed now, by decision. If devices are ever added by someone who cannot
see the kiosk, the QR stops being available and the primary channel changes:
provisioning moves to flash time (a per-device key burned in, or an enrolment
secret) or to an install window a remote operator can open — OSDP's install
mode (§7.4) is the model, with its caveats intact.

**This must reach the code, not only this document.** A TODO at `handlePair`
(`tls/pairing.go`) — the function that decides how a device proves it may pair
— naming this section, so that whoever next changes the pairing path sees that
the attended assumption was a choice rather than an oversight.

## 15. Open questions

1. **Handles or UIDs?** Handles change every client-facing request shape too.
2. **Which query verbs earn their round trip?** A *status* query looks worth
   it; a *read* query should stay optional. The survey's round-trip argument
   applies to us the moment we use one: reading NDEF off a MIFARE Classic 1K is
   sixty-odd card exchanges — single-digit milliseconds locally, one to three
   seconds of tag-in-field time over WiFi. Our protocol does the tag stack at
   the edge and sends one message; a query verb spends that saving.
3. **One tag per holder, or a declared `maxSimultaneousTags`?** A PN532 holds
   two; `soleTag` refuses two.
4. **Does the device report write results**, and does `Verified` then split
   into "confirmed" and "unconfirmable"?
5. **Gateways.** One connection speaking for N readers breaks "one device = one
   connection = one identity".
6. **Poll-delivered events** for links that cannot hold a socket open, or
   push-only with an adapter for the constrained tier?
7. **The client-API boundary.** §12 keeps the client protocol out of the
   break, but if tags gain handles and credentials that are not UIDs, that
   protocol has to express them somehow. Three ways out: the agent synthesises
   a UID for clients (rule 12 violated again), the client protocol grows
   additively (allowed — additive is not breaking), or some devices cannot be
   surfaced to clients at all. Writing `example.com/c/$uid` from a holder that
   reports no UID sits exactly on this line.
8. **Deferred:** whether physical access control is a product direction. If it
   ever is, OSDP compatibility becomes a market requirement rather than a
   technical choice, and §8 is re-opened. Explicitly not being decided now —
   the agent should serve the use cases others do not, so the rules above are
   written to be vertical-neutral.

## References

Primary sources checked for this document. The deleted survey carried its own
list, covering VPCD, TR-03112-6, CTAP hybrid, Web NFC, NCI and NFCGate; that is
in its final revision in the git history.

- [Android `android.nfc.tech.Ndef`](https://www.iut-fbleau.fr/docs/android/reference/android/nfc/tech/Ndef.html)
  — constants, methods, exceptions
- [Apple CoreNFC `NFCNDEFTag`](https://developer.apple.com/documentation/corenfc/nfcndeftag)
  — `queryNDEFStatus`, `NFCNDEFStatus`, `writeLock`
- [CoreNFC session limits](https://developer.apple.com/forums/thread/802895)
  — 60 s session, ~20 s tag connection, error codes 100 and 201
- [W3C Web NFC](https://w3c-cg.github.io/web-nfc/) — surface and exclusions
- [LibOSDP command and reply codes](https://doc.osdp.dev/protocol/commands-and-replies)
- [LibOSDP secure channel](https://libosdp.sidcha.dev/libosdp/secure-channel.html)
  — SCBK-D and install mode
- [ESPHome PN532 component](https://esphome.io/components/binary_sensor/pn532/)
  — `on_tag` / `on_tag_removed`, `write_mode`
- [Home Assistant Tags](https://www.home-assistant.io/integrations/tag/)
  — opaque `tag_id`
- [Improv Wi-Fi](https://www.improv-wifi.com/) — BLE and serial provisioning
- [ESP32 secure element (ATECC608)](https://docs.espressif.com/projects/esp-idf/en/v5.2/esp32/api-reference/peripherals/secure_element.html)
- [ESP-IDF mbedTLS memory guidance](https://docs.espressif.com/projects/esp-faq/en/latest/software-framework/protocols/mbedtls.html)
- [PN532 `MaxTarget`](https://learn.microsoft.com/en-us/dotnet/api/iot.device.pn532.listpassive.maxtarget?view=iot-dotnet-latest)
  — one or two targets
- [NTAG213/215/216 data sheet (NXP)](https://www.nxp.com/docs/en/data-sheet/NTAG213_215_216.pdf)
  — UID and NFC counter ASCII mirror, `MIRROR_CONF` / `MIRROR_BYTE` /
  `MIRROR_PAGE`, and the 14/6/21-byte mirror sizes
- [RFC 9110 §10.1.5 — User-Agent](https://www.rfc-editor.org/rfc/rfc9110#field.user-agent)
  — the `product/version` and comment grammar §13 borrows
