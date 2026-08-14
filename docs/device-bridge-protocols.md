# Device Bridge Protocols — Prior Art and Adoption Options

Research notes on the phone-scanner → agent link (the "device bridge"). The
question this answers: what already exists that we can adopt or build on,
instead of growing our own protocol further in isolation.

## 1. What we have today

The bridge is a hand-rolled JSON-over-WebSocket protocol:

| Concern | Current implementation |
|---|---|
| Transport | WebSocket on the agent port (`/ws?mode=device`), WSS by default |
| Envelope | `{type, id, payload}` (`protocol.WebSocketRequest`) |
| Handshake | First frame must be `registerDevice`; server replies with a `deviceID` |
| Events | `tagScanned`, `tagRemoved`, `deviceHeartbeat` (10s, 30s timeout) |
| Commands | `deviceWriteRequest` / `writeResponse`, correlated by `requestID` |
| Data model | NDEF records + optional `rawData`, plus UID/technology/type/ATR |
| Auth | One shared API secret for every device (query param or Bearer), loopback exempt |
| Trust | Self-signed CA, bootstrap server on :9472 serves the root cert |
| Discovery | mDNS `_nfc-device._tcp` |

Two structural limits follow from this, and they're what most of the options
below are evaluated against:

1. **No command channel to the tag.** `remotenfc.Tag` reports
   `CanWrite: false`, `CanTransceive: false`, `CanLock: false`
   (`nfc/remotenfc/tag.go`). A phone can report what it read; the agent cannot
   drive an exchange with the tag the phone is holding. Everything that needs a
   round trip — DESFire, ISO-DEP applets, real capability probing, password/auth
   flows, read-after-write verification — is hardware-reader-only.
2. **No device identity.** One secret shared by all devices, no pairing, no
   per-device revocation, no protocol version negotiation.

## 2. Candidate protocols

### 2.1 Reader/tag semantics

**PC/SC + ISO 7816-4 APDU** — the universal vocabulary for "talk to a card
through a reader": `connect` → `ATR` → `transmit(APDU) → response` →
`disconnect`. We already speak it on the hardware side (`nfc/device_pcsc.go`,
`manager_pcsc.go`), Android exposes it via `IsoDep.transceive()` /
`NfcA.transceive()`, and iOS exposes a constrained form via
`NFCISO7816Tag.sendCommand` / `NFCMiFareTag.sendMiFareCommand`. This is the
single highest-value thing to adopt: it is the missing verb, and it is the one
verb every layer beneath us already has.

**VPCD (vsmartcard "virtual PC/SC driver")** — a working, deployed instance of
exactly our problem. A driver registers a virtual reader with pcscd/Windows SCM
and relays APDUs over TCP to a remote emulator ("vpicc"), which may be an
Android app relaying to a real contactless card. Wire format is minimal: 2-byte
big-endian length prefix, then either an APDU or a one-byte control code
(`0x00` off, `0x01` on, `0x02` reset, `0x04` ATR). Default port 35963; the
`vpcd-config` tool hands the phone a `vpcd://host:port` URI as a QR code. Worth
adopting as a *compatibility mode* — it would let existing phone apps act as
davi devices, and let other PC/SC software consume a davi-bridged phone —
though it carries no auth, no discovery, and no NDEF layer.

**Web NFC (W3C CG)** — matters as a data-model reference, not a transport. Its
`NDEFRecord` shape (`recordType`, `mediaType`, `id`, `data`, `encoding`, `lang`)
is what any browser-based device already has in hand, and it is close to but not
identical with our record JSON. The spec explicitly excludes low-level I/O
("ISO-DEP, NFC-A/B, NFC-F ... are not supported") and HCE, so a WebNFC device can
never do more than our current protocol allows — it's the floor of the
capability ladder, which argues for capability negotiation rather than a single
device profile.

**NFCGate relay protocol** — TU Darmstadt research toolkit that relays raw
ISO 14443 traffic between a reader-mode phone and an HCE phone over a server.
Proves the raw-relay path works on Android and is a useful reference for framing
tag metadata alongside the byte stream. Its own caveat is ours too: network
latency breaks anything with distance-bounding or tight timeouts.

**NFC Forum NCI** — the DH↔NFCC controller interface (commands/responses/
notifications, packet segmentation). Right idea, wrong altitude: neither iOS nor
stock Android exposes NCI to apps, so we could not implement it on the device
side. Reference only.

**CCID / USB-IP** — reader-class-level redirection. Solves a different problem
(making a *USB reader* remote), needs kernel/driver work, no phone story. Skip.

### 2.2 Pairing, identity, and session security

**BSI TR-03112-6 "IFD Service" (AusweisApp "Smartphone as Card Reader")** — the
closest formal precedent that exists, and the one I'd model our v2 handshake on.
It is a phone-as-card-reader protocol with:

- JSON messages over WebSocket (RFC 6455), connection initiated by the *user
  device* (the desktop app) toward the *IFD* (the phone);
- TLS with a PSK cipher suite, where the PSK is bootstrapped from a short
  out-of-band password (4-digit code, or QR transfer), after which a persistent
  per-device pairing credential (PEM certificate) is used;
- a mandatory first message that negotiates protocol version and establishes a
  `ContextHandle` — a random pseudo-unique per-IFD identifier;
- explicit exclusivity: once connected, further connection attempts are rejected
  until the connection is released and a new password is exchanged;
- IFD-level verbs mirroring PC/SC (establish context, status, connect, transmit,
  disconnect) rather than an NDEF-only abstraction.

Every one of those five points is a gap we have. The version-negotiation-first
message and the OOB-code → durable-credential pairing are cheap to copy even if
we adopt nothing else.

**FIDO CTAP 2.2 hybrid transport (caBLE v2)** — the model for the *off-LAN*
case. Desktop shows a QR containing a CBOR handshake blob with a shared secret;
phone scans it, BLE advertisement proves physical proximity, then both sides meet
at a tunnel server over a WebSocket carrying a Noise handshake inside TLS. If we
ever want "phone anywhere, agent behind NAT", this is the blueprint — especially
the proximity check, which is the honest answer to "how do I know the phone that
paired is the one in the room".

**RFC 8628 (OAuth 2.0 Device Authorization Grant)** — only relevant if pairing
should be brokered by a Davi account rather than by physical proximity. It gives
us the familiar "enter this code" UX and real revocable tokens, at the cost of a
cloud dependency for what is currently a LAN-local product.

**SPAKE2+ / Noise** — the crypto primitives under the above. Matter uses SPAKE2+
to turn a short setup code into a strong session key; that's strictly better than
our current "shared secret in a query string" and avoids TLS-PSK's awkwardness
in browsers.

### 2.3 Envelope and transport plumbing

- **`Sec-WebSocket-Protocol` subprotocol negotiation** — the standard, free way
  to version the bridge (`davi-nfc-device.v1`, `.v2`). Costs a few lines; lets
  us change the protocol later without a flag day.
- **JSON-RPC 2.0** — a specified replacement for our ad-hoc `id`/`requestID`
  correlation, with defined error objects and notifications. Our envelope is
  already 80% of the way there.
- **CBOR** — worth it only if APDU passthrough lands and base64'd byte arrays in
  JSON become the hot path.
- **WebTransport / WebRTC data channels / MQTT 5** — relevant only for the relay
  scenario; MQTT 5's session expiry + QoS 1 is the off-the-shelf answer to
  "resume after the phone's radio drops", which plain WebSocket has no story for.
- **DNS-SD TXT records** — we already advertise `_nfc-device._tcp`; adding
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

Note also that peer-to-peer (LLCP/SNEP/Android Beam) is a dead end — deprecated
in Android 10 and removed since; it should not appear in any design.

## 3. Recommendation

Build on **PC/SC verb semantics** for capability, **TR-03112-6's handshake
shape** for session setup, and keep our own JSON/WebSocket carrier. Concretely,
in dependency order:

**T0 — protocol hygiene (small, no behavior change).**
Negotiate a subprotocol string on upgrade; make the first frame a
version/capability exchange rather than `registerDevice` alone; publish version
and capabilities in the mDNS TXT record; align our NDEF record JSON with the Web
NFC vocabulary (accepting both spellings).

**T1 — the command channel.** Add `deviceTransceiveRequest` /
`deviceTransceiveResponse` carrying base64 APDUs plus an explicit timeout, and
`deviceConnect`/`deviceDisconnect` with the tag's ATR/ATQA/SAK. Then implement
`Transceive`, `Capabilities`, `Write`, and `Lock` on `remotenfc.Tag` on top of
it, so a phone stops being a second-class device. This is the change that
unlocks everything else; the rest are conveniences.

**T2 — per-device pairing.** Replace the single shared secret with: short OOB
code (shown in the systray, or a QR carrying host/port/code) → PAKE → durable
per-device credential; a device list in the tray with revoke. Keep the shared
secret as a legacy path.

**T3 — interoperability.** A VPCD-compatible listener, in one or both
directions: accept existing "Remote Smart Card Reader"-class phone apps as davi
devices, and/or expose a bridged phone to other PC/SC software on the host.
Cheap once T1 exists, because the semantics already match.

**T4 — off-LAN relay,** only if a real requirement appears: CTAP-hybrid-shaped
QR + tunnel, with proximity evidence, rather than an open port.

Explicitly not adopting: NCI (not reachable from app code on either mobile
platform), CCID/USB-IP (wrong layer, no phone path), LLCP/SNEP (dead), Bluetooth
SAP (SIM access, unrelated).

## References

- [vsmartcard / vpcd — Virtual PC/SC Driver](https://frankmorgner.github.io/vsmartcard/virtualsmartcard/README.html) and [wire protocol notes](https://deepwiki.com/frankmorgner/vsmartcard/2.1-vpcd:-virtual-pcsc-driver)
- [PC/SC Relay (vsmartcard)](https://frankmorgner.github.io/vsmartcard/pcsc-relay/README.html)
- [BSI TR-03112-6 — eCard-API-Framework Amendment: IFD Service](https://www.bsi.bund.de/SharedDocs/Downloads/DE/BSI/Publikationen/TechnischeRichtlinien/TR03112/TR-03112-api_teil6_ergaenzung.pdf?__blob=publicationFile&v=3)
- [FIDO CTAP 2.2 — hybrid transport](https://fidoalliance.org/specs/fido-v2.2-rd-20230321/fido-client-to-authenticator-protocol-v2.2-rd-20230321.html)
- [W3C Web NFC](https://w3c-cg.github.io/web-nfc/)
- [WICG Web Smart Card API](https://github.com/WICG/web-smart-card/)
- [NFC Forum — Controller Interface (NCI)](https://nfc-forum.org/build/specifications/controller-interface-nci-technical-specification/)
- [NFCGate relay mode](https://github.com/nfcgate/nfcgate/blob/v2/doc/mode/Relay.md)
- [RFC 8628 — OAuth 2.0 Device Authorization Grant](https://www.rfc-editor.org/rfc/rfc8628.html)
