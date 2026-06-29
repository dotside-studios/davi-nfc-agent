# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
