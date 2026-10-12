# Davi NFC Agent

A local service that reads and writes NFC tags and serves every scan to
applications over a WebSocket. PC/SC readers, phones and WebNFC browsers feed
the same agent, so an application reads one stream whatever produced the tap.
It provides the NFC functionality of the [Davi](https://davi.social) platform.

## Features

- **Readers and phones at once.** USB readers over PC/SC, and paired phones,
  browsers or custom hardware over the device protocol
- **NDEF read and write.** Text, URI, smart poster, vCard, MIME, geo,
  tel/sms/mailto, Android Application Records, and raw records. Writes are
  checked against the tag's capacity, retried on transient faults and read
  back to confirm them
- **Tag operations.** Locking, erasing, raw APDU and framing-level exchanges,
  and NTAG 424 DNA secure dynamic messaging (SDM) with keys the agent holds
- **Capabilities with every scan.** Memory size, usable capacity, and whether
  the tag can be written or locked now
- **Secure by default.** TLS, an API secret, a per-device credential issued at
  pairing, key pinning for devices, and an origin allowlist for browsers
- **Discoverable.** Advertised over mDNS as `_nfc-device._tcp`
- **Tray and Control Center.** Reader selection and status in the system tray,
  and a web console for the log, tag inspection, writing and device management
- **A Go library.** Build your own agent from its packages and plugins, from
  the full tray application to a headless service
- **Cross-platform.** Linux, macOS and Windows

## Supported tags

| Tag | Support |
|-----|---------|
| NTAG213/215/216, MIFARE Ultralight | NDEF read and write, locking, framing-level exchange |
| MIFARE Classic 1K/4K | NDEF read and write, NDEF formatting, custom keys |
| MIFARE DESFire EV1, EV2, EV3 | NDEF, memory and capacity, files behind AES keys over an authenticated session, locking |
| NTAG 424 DNA | NDEF read and write, SDM configuration and offline tap verification, key changes |
| ISO 14443-4 Type 4 | NDEF read and write (experimental) |
| FeliCa | Identifier only |

## Quick start

Download the binary for your platform from
[releases](https://github.com/dotside-studios/davi-nfc-agent/releases), plug in
a reader and run it:

```bash
./davi-nfc-agent
```

Then connect with the API secret (tray → **Copy API Secret**):

```javascript
const ws = new WebSocket(`wss://localhost:9470/ws?secret=${encodeURIComponent(API_SECRET)}`);

ws.addEventListener("message", (event) => {
  const msg = JSON.parse(event.data);
  if (msg.type === "tagData" && msg.payload.uid) {
    console.log(msg.payload.uid, msg.payload.text);
  }
});
```

The agent's certificate is self-signed. [Getting started](docs/getting-started.md)
walks through trusting it from Node.js, reading a tag and writing to it, and
[Connect from a browser](docs/browser-access.md) covers web pages.

## Documentation

| | |
|---|---|
| [Getting started](docs/getting-started.md) | A first read and write, end to end |
| [Installation](docs/installation.md) | Platform setup and troubleshooting |
| [Connect from a browser](docs/browser-access.md) | Origins, certificates and the JavaScript client |
| [Pair a phone](docs/device-setup.md) | Making an iOS or Android app a reader |
| [Client API](docs/client-api.md), [Device API](docs/device-api.md), [Errors](docs/errors.md) | The WebSocket protocols |
| [Configuration](docs/configuration.md) | Flags, environment variables, ports and files |
| [Custom builds](docs/custom-builds.md) | Embedding the agent in Go |

The full index is in [docs/](docs/README.md).

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

[MIT License](LICENSE)

<hr />

Copyright © 2025-2026 Ned Palacios and Dotside Studios. All rights reserved.
