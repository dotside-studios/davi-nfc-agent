# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

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

### Changed

- A `writeResponse` with `success: true` now guarantees the data was verified on
  the tag; unconfirmed writes return an error instead

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
