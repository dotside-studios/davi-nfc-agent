# Device Bridge Protocols: Prior Art and Adoption Options

Research notes on the phone-scanner → agent link (the "device bridge"). The
question this answers: what already exists that we can adopt or build on,
instead of growing our own protocol further in isolation.

## 1. What we have today

The bridge is a hand-rolled JSON-over-WebSocket protocol:

| Concern | Current implementation |
|---|---|
| Transport | WebSocket on the agent port (`/ws?mode=device`), WSS by default |
| Envelope | `{type, id, payload}` (`protocol.WebSocketRequest`) |
| Handshake | `hello` carries a protocol version and the device's capabilities; `registerDevice` remains as the unversioned legacy path. Server replies with a `deviceID` |
| Events | `tagScanned`, `tagRemoved`, `goodbye`, `deviceHeartbeat` (30s interval, 90s timeout) |
| Commands | `deviceWriteRequest` (lock included) and `deviceTransceiveRequest`, correlated by `requestID` |
| Data model | NDEF records + optional `rawData`, plus UID/technology/type/ATR and the tag capabilities the device declares |
| Auth | A per-device credential issued at pairing, or one shared API secret (query param or Bearer); loopback exempt unless `-require-paired-devices` |
| Trust | Self-signed certificate, pinned by public key; pairing server on :9472 hands out a local CA where one is in use |
| Discovery | mDNS `_nfc-device._tcp` |

Two structural limits shaped the survey below, and most of the options in §2
are evaluated against them. Both have since been closed; they are stated here as
written, because the reasoning that follows depends on them:

1. **No command channel to the tag.** `remotenfc.Tag` reported `CanWrite`,
   `CanTransceive` and `CanLock` as false. A phone could report what it read;
   the agent could not drive an exchange with the tag the phone was holding.
   Everything that needs a round trip (DESFire, ISO-DEP applets, real capability
   probing, password/auth flows, read-after-write verification) was
   hardware-reader-only. Closed by T1.
2. **No device identity.** One secret shared by all devices, no pairing, no
   per-device revocation, no protocol version negotiation. Closed by T2.

## 2. Candidate protocols

### 2.1 Reader/tag semantics

**PC/SC + ISO 7816-4 APDU**: the universal vocabulary for "talk to a card
through a reader": `connect` → `ATR` → `transmit(APDU) → response` →
`disconnect`. We already speak it on the hardware side (`nfc/pcsc/device.go`,
`nfc/pcsc/manager.go`), Android exposes it via `IsoDep.transceive()` /
`NfcA.transceive()`, and iOS exposes a constrained form via
`NFCISO7816Tag.sendCommand` / `NFCMiFareTag.sendMiFareCommand`. This is the
single highest-value thing to adopt: it is the missing verb, and it is the one
verb every layer beneath us already has.

**VPCD (vsmartcard "virtual PC/SC driver")**: a working, deployed instance of
exactly our problem. A driver registers a virtual reader with pcscd/Windows SCM
and relays APDUs over TCP to a remote emulator ("vpicc"), which may be an
Android app relaying to a real contactless card. Wire format is minimal: 2-byte
big-endian length prefix, then either an APDU or a one-byte control code
(`0x00` off, `0x01` on, `0x02` reset, `0x04` ATR). Default port 35963; the
`vpcd-config` tool hands the phone a `vpcd://host:port` URI as a QR code. Worth
adopting as a *compatibility mode*: it would let existing phone apps act as
davi devices, and let other PC/SC software consume a davi-bridged phone,
though it carries no auth, no discovery, and no NDEF layer.

**Web NFC (W3C CG)**: matters as a data-model reference, not a transport. Its
`NDEFRecord` shape (`recordType`, `mediaType`, `id`, `data`, `encoding`, `lang`)
is what any browser-based device already has in hand, and it is close to but not
identical with our record JSON. The spec explicitly excludes low-level I/O
("ISO-DEP, NFC-A/B, NFC-F ... are not supported") and HCE, so a WebNFC device can
never do more than our current protocol allows; it's the floor of the
capability ladder, which argues for capability negotiation rather than a single
device profile.

**NFCGate relay protocol**: TU Darmstadt research toolkit that relays raw
ISO 14443 traffic between a reader-mode phone and an HCE phone over a server.
Proves the raw-relay path works on Android and is a useful reference for framing
tag metadata alongside the byte stream. Its own caveat is ours too: network
latency breaks anything with distance-bounding or tight timeouts.

**NFC Forum NCI**: the DH↔NFCC controller interface (commands/responses/
notifications, packet segmentation). Right idea, wrong altitude: neither iOS nor
stock Android exposes NCI to apps, so we could not implement it on the device
side. Reference only.

**CCID / USB-IP**: reader-class-level redirection. Solves a different problem
(making a *USB reader* remote), needs kernel/driver work, no phone story. Skip.

### 2.2 Pairing, identity, and session security

**BSI TR-03112-6 "IFD Service" (AusweisApp "Smartphone as Card Reader")**: the
closest formal precedent that exists, and the one I'd model our v2 handshake on.
It is a phone-as-card-reader protocol with:

- JSON messages over WebSocket (RFC 6455), connection initiated by the *user
  device* (the desktop app) toward the *IFD* (the phone);
- TLS with a PSK cipher suite, where the PSK is bootstrapped from a short
  out-of-band password (4-digit code, or QR transfer), after which a persistent
  per-device pairing credential (PEM certificate) is used;
- a mandatory first message that negotiates protocol version and establishes a
  `ContextHandle`, a random pseudo-unique per-IFD identifier;
- explicit exclusivity: once connected, further connection attempts are rejected
  until the connection is released and a new password is exchanged;
- IFD-level verbs mirroring PC/SC (establish context, status, connect, transmit,
  disconnect) rather than an NDEF-only abstraction.

Every one of those five points is a gap we have. The version-negotiation-first
message and the OOB-code → durable-credential pairing are cheap to copy even if
we adopt nothing else.

**FIDO CTAP 2.2 hybrid transport (caBLE v2)**: the model for the *off-LAN*
case. Desktop shows a QR containing a CBOR handshake blob with a shared secret;
phone scans it, BLE advertisement proves physical proximity, then both sides meet
at a tunnel server over a WebSocket carrying a Noise handshake inside TLS. If we
ever want "phone anywhere, agent behind NAT", this is the blueprint, especially
the proximity check, which is the honest answer to "how do I know the phone that
paired is the one in the room".

**RFC 8628 (OAuth 2.0 Device Authorization Grant)**: only relevant if pairing
should be brokered by a Davi account rather than by physical proximity. It gives
us the familiar "enter this code" UX and real revocable tokens, at the cost of a
cloud dependency for what is currently a LAN-local product.

**SPAKE2+ / Noise**: the crypto primitives under the above. Matter uses SPAKE2+
to turn a short setup code into a strong session key; that's strictly better than
our current "shared secret in a query string" and avoids TLS-PSK's awkwardness
in browsers.

### 2.3 Envelope and transport plumbing

- **`Sec-WebSocket-Protocol` subprotocol negotiation**: the standard, free way
  to version the bridge (`davi-nfc-device.v1`, `.v2`). Costs a few lines; lets
  us change the protocol later without a flag day.
- **JSON-RPC 2.0**: a specified replacement for our ad-hoc `id`/`requestID`
  correlation, with defined error objects and notifications. Our envelope is
  already 80% of the way there.
- **CBOR**: worth it only if APDU passthrough lands and base64'd byte arrays in
  JSON become the hot path.
- **WebTransport / WebRTC data channels / MQTT 5**: relevant only for the relay
  scenario; MQTT 5's session expiry + QoS 1 is the off-the-shelf answer to
  "resume after the phone's radio drops", which plain WebSocket has no story for.
- **DNS-SD TXT records**: we already advertise `_nfc-device._tcp`; adding
  `txtvers`, protocol version, and a capability list makes discovery
  self-describing instead of requiring a connect-then-find-out round trip.

### 2.4 Platform capability ceiling (the real constraint)

Any capability negotiation has to encode this, because the device tiers are
genuinely different:

| Capability | Android (reader mode) | iOS (Core NFC) | Browser (Web NFC) |
|---|---|---|---|
| NDEF read/write | yes | yes | yes (Chrome/Android) |
| ISO-DEP APDU | `IsoDep.transceive` | `NFCISO7816Tag.sendCommand`, entitlement + declared AIDs | no |
| MIFARE DESFire/Ultralight cmds | yes | `sendMiFareCommand` | no |
| MIFARE Classic crypto1 auth | yes (`MifareClassic`) | no | no |
| Raw NFC-A/B framing | `NfcA.transceive` | no | no |
| Tag lock / read-only | yes | yes (NDEF) | `makeReadOnly()` |

iOS also has tighter session timeouts and a reputation for "tag connection lost"
on longer APDU exchanges, so any command-channel design needs per-exchange
timeouts and a resumable failure mode rather than assuming a stable card session.

Note also that peer-to-peer (LLCP/SNEP/Android Beam) is a dead end, deprecated
in Android 10 and removed since; it should not appear in any design.

## 3. Recommendation

Build on **PC/SC verb semantics** for capability, **TR-03112-6's handshake
shape** for session setup, and keep our own JSON/WebSocket carrier. Concretely,
in dependency order:

**T0: protocol hygiene (small, no behavior change).** Partly done.
Negotiate a subprotocol string on upgrade (`davi-nfc-device.v1`, done); make the
first frame a version/capability exchange rather than `registerDevice` alone
(`hello`, done); publish version and capabilities in the mDNS TXT record (not
done); align our NDEF record JSON with the Web NFC vocabulary, accepting both
spellings (not done).

**T1: the command channel.** Done. Add `deviceTransceiveRequest` /
`deviceTransceiveResponse` carrying base64 APDUs plus an explicit timeout, and
`deviceConnect`/`deviceDisconnect` with the tag's ATR/ATQA/SAK. Then implement
`Transceive`, `Capabilities`, `Write`, and `Lock` on `remotenfc.Tag` on top of
it, so a phone stops being a second-class device. This is the change that
unlocks everything else; the rest are conveniences.

**T2: per-device pairing.** Done, except that the PIN exchange is not a PAKE.
Replace the single shared secret with: short OOB
code (shown in the systray, or a QR carrying host/port/code) → PAKE → durable
per-device credential; a device list in the tray with revoke. Keep the shared
secret as a legacy path.

**T3: interoperability.** A VPCD-compatible listener, in one or both
directions: accept existing "Remote Smart Card Reader"-class phone apps as davi
devices, and/or expose a bridged phone to other PC/SC software on the host.
Cheap once T1 exists, because the semantics already match.

**T4: off-LAN relay,** only if a real requirement appears: CTAP-hybrid-shaped
QR + tunnel, with proximity evidence, rather than an open port.

See §4 for how this plan lands on embedded/DIY readers; the ordering shifts
slightly there, and T2's credential choice changes.

Explicitly not adopting: NCI (not reachable from app code on either mobile
platform), CCID/USB-IP (wrong layer, no phone path), LLCP/SNEP (dead), Bluetooth
SAP (SIM access, unrelated).

## 4. Applicability to embedded / DIY readers (Arduino, ESP32)

Everything above was framed around phones, but the same bridge is the natural
home for handmade readers: an ESP32 with a PN532, a Pico W with a PN5180, an
AVR with an RC522. The design holds, with three adjustments. In one respect the
fit is *better* than phones: a PN532 can do things iOS cannot.

### 4.1 Two integration paths exist today, and they are not equal

- **In-process Go device** (`docs/extending-nfc-support.md`): implement
  `Manager`/`Device`/`Tag`, register with `MultiManager`. Full capability
  surface: `Transceive`, `WriteData`, `MakeReadOnly`, `TagCapabilities`,
  `DeviceCapabilities`, `SupportsTransceive()`, `SupportsEvents()`. The doc
  already sketches a serial PN532 device. Cost: the reader must hang off the
  agent host, and adding one means recompiling the agent.
- **Wire protocol device** (`?mode=device`): no recompile, works over the
  network, but lands in the `remotenfc.Tag` tier, i.e. `CanWrite: false`,
  `CanTransceive: false`, `CanLock: false`.

So the T1 command channel is what unifies them: it is the difference between a
DIY reader being a first-class device and being a UID-and-NDEF firehose.

### 4.2 The wire capability model is far poorer than the internal one

Internally we model capability properly: `nfc.TagCapabilities` and
`nfc.DeviceCapabilities` carry `CanTransceive`, `CanPoll`, `SupportedTagTypes`,
`MaxBaudRate`, `SupportsEvents`, `SupportsNDEF`, plus
`AssertCapabilitiesConsistent` to stop the two sources of truth from drifting.

On the wire, a device announces three fields:
`{canRead, canWrite, nfcType}` (`nfc/remotenfc/protocol.go`).

Phones nearly get away with that because they cluster into a couple of profiles.
DIY readers do not: they scatter across the space, which makes the gap
obvious. The T0 "capability-first frame" should therefore mean *project the
existing internal structs onto the wire*, not invent a new vocabulary.

### 4.3 Capability is a set, not a level

| | UID / ATQA / SAK | NDEF | ISO-DEP APDU | MIFARE Classic crypto1 | Raw framing |
|---|---|---|---|---|---|
| MFRC522 / RC522 | yes | via firmware | no (no T=CL layer in common libs) | yes, in hardware | limited |
| PN532 | yes | via firmware | yes (`InDataExchange`) | yes, in chip | yes (`InCommunicateThru`) |
| PN5180 | yes (+ ISO 15693) | via firmware | yes | yes | yes |
| iOS Core NFC | partial | yes | entitlement + declared AIDs | **no** | no |
| Android reader mode | yes | yes | yes | yes | yes |
| Web NFC | no | yes | no | no | no |

A €4 PN532 outranks an iPhone on raw capability and is beaten by it on the NDEF
abstraction. There is no ordering here that a "device level" enum could
capture, which is the argument for negotiated capability sets, and for a
separate bit for raw framing (`InCommunicateThru`-class) distinct from APDU
transceive.

### 4.4 What actually changes in the plan

**TLS-PSK moves from "one option" to the preferred one (T2).** X.509 chain
validation costs flash and RAM on an MCU; PSK ciphersuites in mbedTLS cost very
little, and TR-03112-6 already specifies exactly that. Note the interaction with
our auto-TLS: the CA in `m.caDir` is created once and reused, while leaf certs
are regenerated whenever the host's IPs change (`tls/manager.go`,
`tls/netwatch.go`). A device that pins the **CA** survives that; a device that
pins the leaf SPKI breaks the next time the agent changes network. Firmware must
be told to pin the CA.

**Pairing must work without a screen or keypad.** The phone flow (agent shows a
code, user types it into the phone) inverts for an Arduino. Needs: an
agent-side pairing window (tray button, "accept new device for 60s"), with the
device's key provisioned at flash time or through a captive-portal/serial
config step. Same primitive, opposite direction.

**A serial/USB transport should carry the same envelope.** The most common
handmade-reader topology is a board plugged into the host, not one on WiFi. Over
USB CDC the entire TLS/pairing/discovery layer collapses, since physical
attachment *is* the authentication, so the message envelope should be
framing-agnostic (COBS or SLIP over serial, WebSocket over TCP). This also
rescues the AVR tier, which cannot realistically run TLS + JSON + WebSocket in
2 KB of RAM.

**Discovery needs a floor below mDNS.** ESP32 has a real mDNS stack; smaller
targets do not. Static endpoint config, or a UDP broadcast beacon, as a fallback.

### 4.5 Protocols worth adopting specifically for this class

- **VPCD** gets considerably more attractive here than it was for phones. A
  2-byte big-endian length prefix plus one-byte control codes is roughly fifty
  lines of firmware: no JSON parser, no TLS, no WebSocket handshake. For a
  constrained board it is the single cheapest way to be a davi device, and it
  reuses the same agent-side code path as the phone apps. Consider promoting
  T3 ahead of T2 if DIY readers become a priority.
- **USB CCID** is the zero-work path: an MCU that presents as a CCID device
  shows up as an ordinary PC/SC reader, and our existing `nfc/pcsc/device.go` path
  consumes it with no davi-specific protocol at all. vsmartcard ships a CCID
  emulator to build against. Worth documenting rather than implementing.
- **MQTT** solves one thing better than we do: Last Will and Testament gives
  broker-side death detection, which is strictly better than our 10s
  heartbeat / 30s timeout, and MQTT 5 session expiry plus QoS 1 handles flaky
  WiFi. Client footprint is a few KB. Cost is a broker dependency.
- **CoAP (RFC 7252) + DTLS, with Observe (RFC 7641)** is the standards-track
  answer for constrained devices: UDP, small, and Observe is exactly our event
  stream. Heavier to implement agent-side and rarer in the field; worth knowing,
  probably not worth building.
- **ESPHome** already ships PN532 and RC522 components with its own native API
  (protobuf + Noise). A compatibility target if consumer/Home-Assistant setups
  matter, not a protocol to copy.
- **Wiegand** is the incumbent in the physical-access world and what many
  handmade badge readers emit. Not a transport for us, but it is the thing a
  DIY device would be bridging *from*, so a device profile that only ever
  reports a UID needs to remain first-class.

### 4.6 Board tiers

| Tier | Examples | Viable today |
|---|---|---|
| Full | ESP32 / ESP32-S3, Pico W, RP2040 + W5500 | Current protocol as-is: WiFi, mbedTLS, ArduinoJson, WebSocket, mDNS all available |
| Tight | ESP8266 | Works; TLS is the pinch point: PSK helps materially |
| Constrained | AVR Uno/Nano/Mega | Not over TLS+JSON+WS. Needs the serial transport, a VPCD-style binary path, or a gateway |

## 5. Verdict: keep the protocol, finish the design

The survey above could be misread as "adopt several of these." It shouldn't be.
Our protocol is at the right layer and should stay. What is wrong with it is
that it is under-specified, not that it is custom.

### 5.1 Why our layer is the right one

**Round trips.** This is the decisive argument. Every APDU-level protocol
(VPCD, CCID, remote IFD, NFCGate) relays one card command per network round
trip. Reading NDEF off a MIFARE Classic 1K is roughly 16 authentications plus
~48 block reads, call it 60+ exchanges. Locally that is single-digit
milliseconds each; over WiFi at 20–50 ms it is 1–3 seconds of tag-in-field time,
and NTAG/Ultralight are dozens of reads too. Our protocol does the tag stack at
the edge and sends one message. NFCGate's own documentation flags the same
problem from the other side: relayed traffic breaks anything with timing checks.

**The edge already has the tag stack, and sometimes you cannot bypass it.**
Android, iOS, and PN532 firmware all implement tag handling locally. An
APDU-relay design discards that and re-implements it agent-side against network
latency, effectively running `nfc/tag_*.go` over a socket. Worse, it does not
even work uniformly: iOS exposes no MIFARE Classic crypto1 at all, so for the
most common tag type on the largest phone platform there is nothing to relay.
NDEF-at-the-edge works everywhere precisely because the OS does the crypto
locally.

**Concurrency and push.** We are multi-device by construction: `MultiManager`,
a `deviceID` per connection, phones and hardware readers live simultaneously.
VPCD is one emulator per port; TR-03112-6 explicitly *rejects* additional
connections while one is active. And tag arrival is an event, not a poll: a
phone surfaces a tag when the user taps it. PC/SC models "I hold an exclusive
session and drive a transaction," which is the wrong default for our workload.

**One model, both directions.** The bridge and the client API share the same
tag/NDEF vocabulary. Swapping the bridge for an APDU protocol would not remove a
translation layer, it would add one.

### 5.2 What "better design" actually means

None of the real defects are consequences of being custom. They are the boring
things mature protocols specify and we skipped:

Four of these have since been addressed; the rest are still open.

- **Version negotiation**: was none at all. Now a subprotocol string and a
  versioned `hello`. Done.
- **Capability declaration**: was three booleans on the wire against a much
  richer internal model (§4.2). The device now declares a capability set, with
  undeclared distinguished from declared-false. Done.
- **A command channel**: the T1 omission, now filled. Note this was a *missing
  layer*, not a wrong one: an optional sub-channel for devices that can do more,
  not a replacement for NDEF-level messages.
- **Device identity**: was a shared bearer secret in a query string, with no
  per-device credential and no revocation. Pairing now issues one per device,
  revocable. Done.
- **Error taxonomy**: `protocol.ErrorCode` carries a `retryable` flag and
  separates "the tag left the field" (`TAG_REMOVED`) from "this device will
  never support that" (`NOT_SUPPORTED`). Done.
- **Liveness**: a 30s/90s heartbeat where MQTT's Last Will gives immediate
  death detection. Worth implementing the idea, not adopting the broker. Open.
- **Reconnect/resume**: undefined. A phone whose radio drops mid-write leaves
  the request in limbo; there is no idempotency key and no replay. Open.
- **Backpressure**: the device manager's tag channel is buffered to 10 and
  behavior past that is unspecified. Open.

### 5.3 The rule to follow

Borrow **design decisions**, not wire formats. From TR-03112-6: version-first
handshake, context handle, OOB code to durable credential, PSK over X.509.
From PC/SC: the verb vocabulary for the optional command sub-channel. From
JSON-RPC: correlation and error discipline. From MQTT: last-will semantics.

Foreign protocols belong at the **edge, as adapters**. VPCD and USB CCID exist
in the plan so devices too small to afford our protocol still work, and so
other PC/SC software can consume a davi-bridged reader. That is a compat shim,
not a migration.

### 5.4 The honest counterargument

Custom protocols rediscover other people's mistakes, and we already have: no
versioning, weak auth, no resume are exactly the classics. The mitigation is not
to abandon ours but to stop improvising the parts that are already specified
elsewhere; §5.2 is that list. It is worth re-testing the assumption if the
product ever centres on ISO-DEP applet transactions rather than NDEF payloads,
because that is the workload the APDU-level protocols are actually shaped for.

## References

- [vsmartcard / vpcd: Virtual PC/SC Driver](https://frankmorgner.github.io/vsmartcard/virtualsmartcard/README.html) and [wire protocol notes](https://deepwiki.com/frankmorgner/vsmartcard/2.1-vpcd:-virtual-pcsc-driver)
- [PC/SC Relay (vsmartcard)](https://frankmorgner.github.io/vsmartcard/pcsc-relay/README.html)
- [BSI TR-03112-6 eCard-API-Framework Amendment: IFD Service](https://www.bsi.bund.de/SharedDocs/Downloads/DE/BSI/Publikationen/TechnischeRichtlinien/TR03112/TR-03112-api_teil6_ergaenzung.pdf?__blob=publicationFile&v=3)
- [FIDO CTAP 2.2: hybrid transport](https://fidoalliance.org/specs/fido-v2.2-rd-20230321/fido-client-to-authenticator-protocol-v2.2-rd-20230321.html)
- [W3C Web NFC](https://w3c-cg.github.io/web-nfc/)
- [WICG Web Smart Card API](https://github.com/WICG/web-smart-card/)
- [NFC Forum: Controller Interface (NCI)](https://nfc-forum.org/build/specifications/controller-interface-nci-technical-specification/)
- [NFCGate relay mode](https://github.com/nfcgate/nfcgate/blob/v2/doc/mode/Relay.md)
- [RFC 8628: OAuth 2.0 Device Authorization Grant](https://www.rfc-editor.org/rfc/rfc8628.html)
