# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.1.2] - 2026-08-15

### Added

- **A tag broadcast names the reader that read it.** `tagData` described the tag
  and not its source, so a client holding one had no way to tell whether a
  `deviceStatus` applied to it. The payload now carries `deviceID` when the tag
  came from a paired device, taken from the source the tag already records.
  Absent means the agent's own hardware, which is what every tag was before
  devices existed. See [Tag Data](docs/api.md#tag-data)

### Changed

- The tag inspector names the reader a tag came from, since with a phone paired
  that is no longer a question with one answer

### Fixed

- **A smartphone disconnecting no longer floods the log.** `GetTags` reported a
  closed device as `fmt.Errorf("device is closed")` — its own phrase, matching
  no sentinel. `IsDeviceClosedError` looks for the typed `ErrDeviceClosed` or,
  failing that, the string `device closed`, so it declined every one of them:
  two spellings that read identically to a person and not at all to
  `strings.Contains`. Nothing downstream handled a phone disconnecting as a
  result — the reader still had a device, so it polled straight back into the
  closed one, a loop with no delay and a log line every turn, and a reconnection
  never once attempted. The three returns are now `nfc.ErrDeviceClosed`, which
  survives the `%w` wrapping `getTags` adds; the string fallback accepts both
  spellings, since it exists for text this package does not control
- **A poll that hit an unrecognized error is paced.** `handleDeviceErrors`
  treats "the device manager still has a device" as a sign the device recovered,
  and polls again immediately. That holds for the branches that close and reopen
  one, but an error no branch recognized left the device exactly as it was, so
  the test passed trivially and the loop spun at full speed. The delay for an
  unhandled error existed already, one branch further down, unreachable in the
  case that needed it most. A poll that recognizes nothing now waits the same
  interval, so an error past the classifiers costs a line a second rather than a
  line a millisecond
- **A standing device fault is reported once, not on every poll.** `HandleError`
  is reached from the poll loop, so an error that persisted was logged as often
  as the device was polled — and the reason a line is worth reading is that the
  condition changed. It now reports a fault the first time, again when the
  reason changes, and again when one returns after the device has worked,
  matching what `ListDevices` was given in 1.1.1 against the same log buffer
- **The control center no longer discards tags scanned by a phone.** A tag
  tapped on a paired phone reached the frontend and never appeared in the
  console, which reads the same broadcast on the same endpoint. The console
  cleared its tag whenever a `deviceStatus` reported no card — and that status
  describes the agent's own reader, whose `cardPresent` is false for the entire
  life of every phone scan. The tag was received, displayed, and wiped by the
  next status message. No other consumer reads tag presence out of reader
  status, which is why only this one lost them. Reader status now clears only a
  tag the reader itself produced; a tag from a device is cleared by the device
  saying so
- **A tag leaving a phone's field is recorded as a removal.** A device reports it
  as a `tagData` with no UID, which the console treated as a scan — rendering a
  blank tag over the real one and logging a scan of nothing

## [1.1.1] - 2026-08-15

### Added

- **Trust this agent in browsers, without a terminal.** A browser cannot open a
  `wss://` connection to an untrusted certificate, and unlike a page visit there
  is no warning to click through — the page simply never connects, and nothing
  on either side says why. The only remedy was `-install-ca`, a launch flag, so
  a non-technical operator could not get a web page talking to the reader at
  all. The tray gains **Trust This Agent in Browsers** and the Control Center
  gains the same action under *Device trust*, both stating the tradeoff before
  they run: a CA in a trust store can sign for any name, so a certificate for a
  name you control is still preferable. The listeners restart afterwards, so the
  reissued certificate is the one served. The entry hides itself once there is a
  CA, and the flag stays for scripted provisioning

### Fixed

- **The public key pin now describes the key the agent actually serves.** The
  pin is computed from `agent.key`, the long-lived key the self-signed route
  signs, and that is what `/pair` hands a device. The CA route did not use it:
  truststore's `MakeCert` generates a key of its own, which was then installed
  as the server key. So an agent with a CA advertised a pin its handshake could
  never present, and a device that paired with one was locked out permanently —
  re-pairing returned the same unusable pin. The CA route now signs the same
  persistent key, so the pin holds across adopting a CA and devices paired
  before **Trust This Agent in Browsers** keep working after it. An install
  already serving the wrong key reissues on the next start
- **Reissuing a certificate no longer installs a certificate authority.**
  `RegenerateCertificates` called the CA routine directly instead of going
  through the routing that picks self-signed or CA, so the Control Center's
  **regenerate** action — labelled as nothing more than reissuing a certificate
  — created a local CA, installed it in the system trust store and prompted for
  a password, on an agent that had deliberately never had one. That defeated
  1.1.0's own change to stop installing a CA by default. Reissuing now keeps
  whichever route the install already uses, and putting a CA in the trust store
  happens only when explicitly asked for
- **A missing reader no longer floods the log.** `ListDevices` is polled
  continuously by the tray, the console and the device watcher, and every failed
  poll logged. With no reader attached that was 85 of 108 lines — one repeating
  message, drowning anything else that happened, including the certificate
  errors the log was added to surface. A failure is now reported once, again if
  the reason changes, and a recovery gets a line of its own. Same conditions
  measured after: 1 line in 21

## [1.1.0] - 2026-08-15

### Added

- **Control Center.** A web console served by the agent itself at its own root,
  covering what neither the tray nor the flags could do. Four tabs: Overview,
  Tag, Activity and Security. The reader controls — start/stop, device,
  mode, card filter, port — sit on Overview rather than behind a settings tab,
  so the page an operator lands on is the page they work from. Opened from the
  tray's
  **Open Control Center**, which mints a single-use token and hands it to the
  browser. Being same-origin, it is also the one browser client that works on a
  fresh install without `-install-ca` — a page visit can be accepted manually,
  where a bare `wss://` to an untrusted certificate fails outright. Built with
  Vite and React; `webui/frontend/dist` is committed and embedded, so
  `go build .` still needs nothing but Go.
  See [Control Center](docs/control-center.md)
- **The agent's log is readable.** Log output went to stderr and nowhere else,
  so an agent started from a desktop launcher discarded every certificate
  warning, refused origin and reader failure as it produced it. The last 5000
  lines are now kept in memory, streamed live to the console, filterable by
  level and text, and downloadable for a bug report. Nothing is written to disk
- **NDEF composer and tag inspector.** Writing a tag by hand previously meant
  being a client application: the agent's support for smart posters, vCards,
  MIME, geo, Android Application Records and fully raw records was unreachable
  without writing code against the WebSocket protocol. The console composes any
  of them, sizes the message against the tag's reported capacity as it is
  typed, and can erase or permanently lock. It writes over the ordinary client
  endpoint, so there remains one implementation of the write path
- **Live event feed.** Scans, writes, locks and errors as they happen,
  filterable, pausable and exportable as NDJSON. The tray only ever showed the
  card currently on the reader, so a tag presented and taken away left no trace
- **The control center is a removable module.** Everything it needs lives in
  `webui/` — the gate, the routes, the state snapshot, the dispatcher and the
  frontend it embeds — and none of it imports the agent. It declares what it
  needs of the agent as `webui.Host`, implemented by a single adapter in
  `package main`, which both keeps the console's whole reach into the agent
  readable in one file and lets its tests run against a fake host with no
  hardware. `go build -tags nowebui .` then drops it: no `/control` routes, no
  privileged API, no tray entry and no embedded console — about 820 KB smaller,
  with none of the console's strings present. Only three files in `package main`
  carry the constraint; the call sites tolerate a nil console, so no shared file
  needs a tag of its own. The agent's own protocol is unaffected: raw tag
  exchanges, settings persistence and the log ring remain in both builds, each
  being reachable without the console
- **Raw exchanges with a tag, from a client and from the console.** The agent
  could already transceive with a tag, but only agent-to-device: no client
  could ask for one, so DESFire, ISO-DEP applets and capability probing meant
  writing a program. `transceiveRequest`/`transceiveResponse` open that channel
  to clients, routed like a write — to the device holding a tag, otherwise to
  the hardware reader. The console gains an APDU panel with hex in, hex and
  ASCII out, decoded ISO 7816 status words, per-exchange timing, and presets
  built from the commands `nfc/apdu.go` already constructs.
  **Refused in read-only mode**: a raw command can write to a configuration
  page, burn OTP bits or lock a tag permanently, and the agent can neither tell
  that apart from a `SELECT` nor undo it
- **Connected clients are visible, and can be disconnected.** The client server
  tracked its connections but exposed only a count, so "something is writing to
  my tags" had no answer. Each connection now reports its origin, address, user
  agent, how long it has been connected, and how many writes and locks it has
  issued — counted per connection, so a client that is only listening is
  distinguishable from one changing tags. Any one of them can be disconnected;
  it is free to reconnect, so revoking its origin remains what bars it
- **Per-device revocation.** Paired devices are listed with platform, pairing
  time, last seen and whether they are connected, and each can be revoked on
  its own. The tray's only option was to revoke every device, which made
  removing one lost phone cost every other phone its pairing
- **Settings persist.** Reader mode, card-type filter, reader selection, port
  and the paired-device requirement are written to `settings.json` in the
  config directory. Mode and filter previously lived only in the tray's menu
  state and were lost on every launch; device and port could only be set with a
  flag, and so had to be repeated in whatever started the agent. An explicit
  flag still wins, and no file is written until something is deliberately saved
- **Certificate and origin diagnostics.** The console reports certificate
  expiry, issuer and the names it actually covers, and warns when the agent is
  reachable on an address the certificate omits — previously indistinguishable
  from the agent being down, since a browser reports both as a failed
  connection. Origins refused since startup accumulate with a one-click
  **allow**, where the tray offered a blocked origin only while its menu was open
- **Device protocol versioning.** Devices offer the `davi-nfc-device.v1`
  WebSocket subprotocol and send a `hello` first frame carrying the protocol
  version alongside registration, so setup costs one round trip. The response
  reports the version both sides will speak, clamped to what the agent
  implements rather than refused when a device asks for something newer.
  Devices that offer nothing still send `registerDevice` and get a
  byte-identical exchange back
- **Full capability declaration on the wire.** Devices can declare APDU
  transceive, raw framing, lock support, device type, supported tag families
  and baud rate; tag scans can carry the capabilities the device determined for
  that specific tag. All additions are `omitempty`, so a device declaring
  nothing extra sends exactly the previous object
- **Classified errors.** Error payloads carry `retryable`, plus the failing
  operation and tag. Combined with `code` this distinguishes "retry with
  backoff" from "ask the user to present the tag again" from "stop". Covers
  both the device and client endpoints; existing code strings are unchanged
- **`goodbye` frame.** A device can announce a deliberate departure, which the
  agent acknowledges with a close handshake. Silent disconnects are classified
  from the WebSocket close code, so a clean shutdown is no longer
  indistinguishable from a dead radio
- **Writing to tags held by remote devices.** A write is routed to the device
  reporting the most recent scan whenever no hardware reader has a card
  present, correlated by request ID and bounded by a 20s timeout. A device that
  disconnects mid-write releases its waiters immediately rather than making
  each wait out the timeout. Carries an idempotency key so a device can
  recognize a write it already applied after a lost response
- **Transceive command channel.** `deviceTransceiveRequest`/`Response` let the
  agent exchange raw data with a tag a device is holding, with separate
  declarations for APDU-level and framing-level exchange. Costs one network
  round trip per command, so it is for what genuinely needs it — DESFire,
  ISO-DEP applets, capability probing — not as a general read path
- **Origin allowlist with first-party defaults**, persisted to
  `allowed-origins.json` and managed from the tray. A refused origin is offered
  as a one-click *"Allow …"*, which persists without a restart. Preload with
  `-allowed-origins` or `DAVI_NFC_ALLOWED_ORIGINS`
- **Public key pinning.** The agent reports `serverInfo.publicKeyPin` and logs
  it at startup. Devices record it when pairing and compare it on later
  connections, recognizing the agent without any certificate authority. It
  survives certificate reissues, which happen whenever the host's addresses
  change
- **Per-device pairing.** `POST /pair` on the bootstrap server exchanges the
  kiosk PIN for a credential belonging to one device, returned alongside the
  agent's key pin. Tokens are stored hashed and shown once. The tray lists
  paired devices and revokes them individually, or all at once
- **`-require-paired-devices`** (also `DAVI_NFC_REQUIRE_PAIRED_DEVICES`, and a
  tray toggle that applies immediately): admits only devices holding a paired
  credential, withdrawing both the shared secret and the loopback bypass for
  device connections. Browser consoles are unaffected

### Changed

- **Self-signed certificates by default; the local CA is now opt-in.** The
  agent previously created a certificate authority and installed it into the
  system trust store on startup. It now serves a self-signed certificate from a
  key it generates once and keeps, touching no trust store. Use `-install-ca`
  for browsers that cannot pin and have no externally provisioned certificate,
  or point `-cert`/`-key` at one you provide. **An install that already has a
  CA keeps using it**, so a browser console working today is unaffected
- Tags held by remote devices now report `canWrite`, `canLock` and
  `canTransceive` honestly, based on what the device declared *and* whether it
  is still connected, instead of always reporting read-only
- **Single-port architecture.** The device server (previously port 9470) and
  client server (previously port 9471) are now served from one listener on a
  single port (default 9470). NFC devices connect to `/ws?mode=device` (or with
  the `X-Device-Mode: true` header); web clients connect to plain `/ws`. Both
  `/health` and `/api/v1/health` are served on the one port and report
  `"type":"agent"`. A new `server/unifiedserver` package fronts the existing
  device/client handlers and routes each connection; the in-process bridge
  between them is unchanged.

### Removed

- The `-client-port` flag. The client endpoint now shares the agent port; set
  the single port via `-device-port` (default 9470).

### Fixed

- **A hosted web console could not connect at all.** The WebSocket origin guard
  rejects any `Origin` that is not the agent's own `host:port`, and
  `AllowedOrigins` — the escape hatch built alongside it — was never populated:
  no flag, no environment variable, and `agent.go` left it nil on both servers.
  Every browser console was affected, since a page is by construction served
  from somewhere other than the agent's port. The REST endpoints meanwhile
  answered `Access-Control-Allow-Origin: *`, so the two halves of the same API
  disagreed about who may call them
- Pairing required auto-TLS. The endpoint lived on a server that only ran when
  the agent managed its own certificates, so the deployment using an externally
  provisioned certificate — the one that most needs per-device credentials —
  had no way to pair
- `ServerBridge.Close` closed channels that producers could still be sending
  on, which panics the losing goroutine. Only the done channel is closed now;
  consumers already exit on their own context
- A tag scan was published to clients before the route a write needs was
  registered, so a client reacting to `tagData` with an immediate write could
  be told no device held a tag it had just been told about
- Tag scans that fail to parse — a malformed UID, an undecodable NDEF message —
  were reported as the transient `TAG_SEND_FAILED`, inviting a device to resend
  a payload that can never be accepted. They are now `INVALID_DATA` and marked
  permanent
- A failed write reported `WRITE_FAILED` whatever went wrong, because the
  reader's typed error was flattened to a string at the bridge before the
  client server saw it

- Certificate generation silently ignored a failure to set `CAROOT`, and the
  hosts cache ignored write errors, so a truncated cache could be read back as
  a complete host list on the next start
- Handlers in the TLS bootstrap server logged a certificate or profile as
  "served" even when writing the response body had failed

### Security

- The Control Center's API is gated independently of the client and device
  endpoints. Every request must arrive over loopback, declare the agent's own
  origin, and carry a session cookie minted by the tray; the routes are served
  without the permissive CORS headers the client endpoints carry. The origin
  allowlist is deliberately not consulted — an entry there authorises a console
  to read tags and must never confer the ability to revoke a device or rotate
  the secret. Loopback is determined from `RemoteAddr`, never from a forwarding
  header
- `golang.org/x/text` and `golang.org/x/net` are updated past two
  vulnerabilities reachable from certificate generation: an infinite loop on
  invalid input in `x/text` (GO-2026-5970) and a failure to reject ASCII-only
  Punycode labels in `x/net/idna` (GO-2026-5026)
- Builds move to Go 1.25.13, clearing four standard-library vulnerabilities
  the agent reaches through its own TLS and HTTP servers: unbounded
  post-handshake messages (GO-2026-6090) and an Encrypted Client Hello privacy
  leak (GO-2026-5856) in `crypto/tls`, a missing `ReadHeaderTimeout` on the
  unencrypted HTTP/2 check in `net/http` (GO-2026-6089), and unbounded
  recursion in `encoding/asn1` (GO-2026-5972) reached when parsing the CA
  certificate
- The agent no longer installs a certificate authority into the system trust
  store by default. A CA in a trust store can sign for **any** name, not just
  this agent, so whoever holds its key can intercept that machine's traffic.
  See *Changed* above for the replacement and the compatibility path
- Per-device credentials replace a single shared secret as the recommended way
  to authenticate a device, so removing one device no longer means rotating a
  secret that logs out every other device at the same time

## [1.0.3] - 2026-06-29

### Fixed

- NTAG locking now locks the **entire** user area. `MakeReadOnly` previously set
  only the static lock bytes (pages 3-15), leaving the bulk of an NTAG215/216
  writable while reporting a successful lock; it now also sets the model's
  dynamic lock bytes. Validated end-to-end against in-memory tag emulators
- DESFire read/write now interpret the DESFire native status word (wrapped
  `91 00` = OK) instead of requiring ISO `90 00`. The old generic check would
  have rejected every real DESFire response
- DESFire read/write now follow the additional-frame (`91 AF`) chain, so NDEF
  payloads larger than a single ~59-byte native frame work. Validated against
  the in-memory DESFire emulator; the per-frame size is datasheet-modeled and
  wants a hardware cross-check
- TLS network watcher no longer infinite-loops regenerating certificates when
  the hosts-cache write fails. The watcher now compares against in-memory
  `lastHosts` (updated only after a fully successful regeneration) instead of
  the possibly-stale disk cache, so a partial failure retries cleanly on the
  next tick instead of re-running truststore install + cert generation forever
- TLS `Manager` network-watcher state (`networkChangeChan`, `stopWatchChan`,
  `lastHosts`) is now mutex-guarded, fixing a data race under `-race` and a
  double-`StopWatching` close-of-closed-channel panic
- Network-change watchers now shut down reliably on quiescent sockets. The
  close-to-interrupt trick (which Linux/Darwin don't guarantee will wake a
  thread already blocked in `recvfrom`) is replaced with a short receive
  timeout so the watcher loop observes stop and returns within ~200ms

### Added

- Tag capabilities exposed over the wire: every `tagData` broadcast now carries
  a `capabilities` object (memory, max-NDEF size, `canWrite`, `canLock`,
  `isReadOnly`, `supportsPassword`), and clients can fetch the present tag's
  capabilities on demand via a `capabilitiesRequest`/`capabilitiesResponse`
  message. Backed by `NFCReader.GetCapabilities` and `Card.Capabilities`
- Erase/format support: `NFCReader.EraseCard` and an `empty` write record type
  overwrite a tag with an empty NDEF message (verified like any write, and
  composable with `lock`). Reversible — the tag can be rewritten afterward
- Password-protection capability reporting (`TagCapabilities.SupportsPassword`,
  true for NTAG21x) and the reader API contract (`SetCardPassword`,
  `RemoveCardPassword`, `PasswordOptions`). The destructive NTAG config writes
  (PWD/PACK/AUTH0/ACCESS) are intentionally gated off and return a clear
  not-supported error pending validation on real hardware, since a wrong
  configuration can permanently lock a tag
- Tag locking (make read-only) exposed through the API: write-and-lock in one
  step via `"lock": true` on a write request, or lock an already-written tag
  with a standalone `lockRequest`. Supported on lockable tags (NTAG,
  Ultralight); others return a clear error. New `NFCReader.LockCard` and
  `WriteOptions.Lock`
- Expanded write record types beyond text/uri: `url`, `mailto`/`email`, `tel`,
  `sms`, `geo`, `smartposter` (URI + title), `mime`, `vcard`, `external`, `aar`
  (Android Application Record / app launch), and fully custom `raw` records
  (TNF + type + payload). New `NDEFSmartPoster` and `NDEFRaw` builders
- URI records are now written with the longest matching NFC Forum abbreviation
  prefix (e.g. `https://`, `tel:`, `mailto:`), saving bytes on small tags; the
  decoder understands the full prefix table for tags written by other tools
- Read-after-write verification: writes are now confirmed by reading the data
  back and comparing it to what was written, bringing write reliability to parity
  with the read path
- Automatic write retry with linear backoff on transient failures (configurable
  via `WriteOptions.MaxWriteAttempts`); permanent failures (card removed,
  read-only, capacity exceeded) are never retried
- Pre-flight capacity check that rejects NDEF messages larger than the tag's
  usable capacity before any write is attempted
- Structured write results (`WriteResult` / `WriteMessageWithResult`) surfaced in
  the `writeResponse` payload: `uid`, `tagType`, `bytesWritten`, `verified`, and
  `attempts`
- MIFARE Classic NDEF formatting and custom-key support: blank Classic 1K cards
  can be formatted as NFC Forum tags (MAD written to sector 0, sector trailers
  switched to NFC Forum config, with validation that keeps trailers rewritable
  with Key B to prevent bricking), and cards provisioned with non-default keys
  can be read/written via `NFCReader.SetClassicKeys` / `pcscClassicTag`
  candidate keys (#3)
- PNP phone pairing: a QR-first flow for using a phone as an NFC reader. The
  agent generates a 6-digit PIN (`crypto/rand`) shown in the systray; `/qr.png`
  encodes a PIN-gated `/install` URL, and `/install` user-agent-routes to an
  unsigned iOS `.mobileconfig` or an Android DER `.crt` that the OS installs
  directly. `/ca.pem` remains (PIN-gated) for legacy clients, and five wrong
  PIN attempts lock pairing until restart. New systray "Pair Phone" and
  "Pairing PIN" items. Adds `github.com/skip2/go-qrcode`
- Native OS network-change watcher for certificate regeneration: subscribes to
  the platform address-change source (Linux `AF_NETLINK`, macOS `PF_ROUTE`,
  Windows `NotifyAddrChange`) so a network roam regenerates TLS certs in
  milliseconds instead of waiting on the old 5s poll, now demoted to a 30s
  safety net
- IPv6 support: `GetLANIPs` / `getLocalIPs` now return both IPv4 and IPv6
  globals (IPv4 preferred for `ips[0]` callers), all host:port composition goes
  through `net.JoinHostPort` so IPv6 literals are bracketed, and `::1` is
  accepted as a valid host
- Clipboard fallback on Linux: the systray copy buttons now pick the clipboard
  utility by display server (`wl-copy` on Wayland, then `xclip`/`xsel` on X11)
  instead of assuming `xclip`, with a clear install hint naming the packages
  when none is present. macOS (`pbcopy`) and Windows (`clip`) are unchanged

### Security

- Tier-1 hardening across the WebSocket servers. `CheckOrigin` now rejects
  cross-site WebSocket hijacking (both Upgraders previously returned `true`,
  letting any visited website read live NFC events from localhost); the device
  server (phone-as-reader) and client server now require an API secret
  (loopback-bypassed, constant-time compared, supplied via `?secret=` or
  `Authorization: Bearer`); the secret is auto-generated (32-byte URL-safe
  base64) and persisted under the config dir with mode 0600 + Windows DACL on
  first run; and both the pairing PIN and the API secret are rotatable from the
  systray ("Regenerate Pairing PIN" / "Regenerate API Secret")
- Windows TLS file permissions: the TLS and CA directories, `server.key`, and
  `hosts.txt` now receive an explicit DACL granting only the current user,
  Administrators, and SYSTEM. Unix 0600/0700 mode bits are advisory on Windows,
  so private keys were previously world-readable

### Changed

- A `writeResponse` with `success: true` now guarantees the data was verified on
  the tag; unconfirmed writes return an error instead
- Streamlined the custom Manager/Device/Tag extension surface for library
  consumers: custom tag implementations can embed `BaseTag` to inherit sensible
  defaults instead of implementing the full interface by hand

## [1.0.2] - 2026-01-19

### Fixed

- Critical segmentation fault (SIGSEGV) caused by race condition in PC/SC context management where context could be released while another goroutine was using it

## [1.0.1] - 2026-01-18

### Fixed

- Concurrent WebSocket write panic caused by multiple goroutines writing to the same connection
- Excessive "no NFC devices found" log spam when no device is connected

### Changed

- Device discovery moved to agent level for cleaner separation of concerns
- Agent now starts without a device and waits for device connection
- Hot plug-n-play support: devices are auto-discovered when plugged in and paths are cleared on disconnect
- Systray now reads device state from agent (agent is source of truth)
- "Refresh Devices" menu item now auto-selects first available device if none connected

### Removed

- Last scanned card is no longer sent to newly connected WebSocket clients

## [1.0.0] - 2026-01-11

### Added

- Two-server architecture: Device Server (port 9470) for NFC readers and Client Server (port 9471) for applications
- Hardware NFC reader support via PC/SC (ACR122U and other PC/SC-compatible readers)
- Remote device support: smartphones, browsers with WebNFC, and custom hardware can connect as NFC readers
- NDEF read/write support for Text and URI record types
- MIFARE Classic, DESFire, and Ultralight tag support
- ISO14443-4 Type 4A tag support (experimental)
- JavaScript client libraries: NFCClient (consumer) and NFCDeviceClient (universal device input with configurable WebSocket client)
- Auto-TLS certificate management with CA bootstrap server (port 9472)
- mDNS/Bonjour service discovery for automatic device detection
- System tray UI for device management and status monitoring
- Cross-platform builds: Linux (amd64, arm64), macOS (amd64, arm64), Windows (amd64)
- Build versioning with embedded commit hash and build time
- Network change detection for automatic certificate regeneration
- Protocol validation for PC/SC device operations
- Support for handling unsupported NFC tags with error reporting
