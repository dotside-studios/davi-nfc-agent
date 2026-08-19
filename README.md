# Davi NFC Agent

A lightweight NFC card reader agent with WebSocket broadcasting capabilities. Reads and writes NDEF formatted data from NFC tags and broadcasts to connected clients in real-time. This is for use for the NFC-related functionality integrated into the [Davi](https://davi.social) platform.

## Features

- **Multiple Device Support**: Hardware NFC readers and remote devices simultaneously
- **Remote NFC Devices**: Smartphones, browsers with WebNFC, or any device that can connect to the API
- **Rich NDEF Read/Write**: Text, URI, smart poster, vCard, MIME, geo, tel/sms/mailto, Android Application Records, and fully custom raw records
- **Reliable Writes**: Read-after-write verification, automatic retry on transient failures, and pre-flight capacity checks
- **Tag Locking & Erase**: Make tags read-only or wipe them back to an empty NDEF message
- **Tag Capabilities**: Memory size, usable capacity, and write/lock/password support reported with every scan
- **Real-time WebSocket**: Instant tag data broadcasting
- **Secure by Default**: Automatic TLS (WSS) with key pinning for devices, an origin allowlist for browsers, plus optional API-secret authentication
- **Per-device Pairing**: each device gets its own revocable credential, issued against a PIN
- **Auto-discovery**: mDNS/Bonjour advertising for zero-config device setup
- **Cross-platform**: Linux, macOS, Windows
- **System Tray UI**: Device management and status
- **Control Center**: A built-in web console for logs, tag inspection, NDEF
  writing, per-device revocation and persistent settings

## Supported Devices

**Hardware Readers**: ACR122U, ACR1252U, and other PC/SC-compatible readers

**Remote Devices**: Any NFC-capable device that connects via the [Device API](docs/api.md#device-api), including:
- Smartphones (iPhone 7+/iOS 13+, Android 4.4+)
- Browsers with WebNFC (Chrome on Android)
- Custom hardware or IoT devices

**Card Types**: MIFARE Classic (incl. NDEF formatting and custom keys), DESFire, Ultralight, NTAG21x, ISO14443-4 Type 4A (experimental)

## Quick Start

Download pre-built binaries from [releases](https://github.com/dotside-studios/davi-nfc-agent/releases), or build from source:

```bash
git clone https://github.com/dotside-studios/davi-nfc-agent.git
cd davi-nfc-agent
go build .
./davi-nfc-agent
```

See the [Installation Guide](docs/installation.md) for platform-specific setup
and troubleshooting, and [Setting up an iOS or Android device](docs/device-setup.md)
for pairing a phone.

### Control Center

Choose **Open Control Center** from the tray menu to manage the agent in a
browser: read its log, inspect and write tags, revoke a single paired device,
edit the origin allowlist, and set preferences that survive a restart.

It is reachable only over loopback, only from a page the agent served, and only
with a token minted by that tray entry — the origin allowlist plays no part in
it. See [Control Center](docs/control-center.md).

It is a self-contained package (`webui/`, frontend included) that reaches the
agent through one interface, so `go build -tags nowebui .` omits the routes, the
privileged API and the embedded console without touching anything else.

### Command-line Options

```bash
./davi-nfc-agent                       # System tray mode (default)
./davi-nfc-agent -version              # Print version information and exit
./davi-nfc-agent -device "ACS ACR122U" # Use a specific PC/SC reader by name
./davi-nfc-agent -device-port 9480     # Custom agent server port (default 9470, serves both devices and clients)
./davi-nfc-agent -api-secret mysecret  # Set the API authentication secret
./davi-nfc-agent -allowed-origins app.example.com  # Let a hosted web console connect
./davi-nfc-agent -require-paired-devices  # Admit only devices that have paired
./davi-nfc-agent -install-ca           # Trust this agent in browsers (installs a local CA)
./davi-nfc-agent -auto-tls=false       # Disable automatic TLS certificate management
./davi-nfc-agent -cert cert.pem -key key.pem  # Use your own TLS certificate
./davi-nfc-agent -config-dir ./config  # Override the config directory
```

### Connecting from a web console

The agent only accepts WebSocket upgrades whose `Origin` matches its own
host:port — otherwise any site the operator visits could drive the reader,
including permanently locking cards. A console served from anywhere else, which
is every hosted one, must be allowed.

**The Davi consoles are allowed out of the box**, so nothing needs configuring
for them. The allowlist lives in `allowed-origins.json` in the config directory
and is managed from the tray under **Allowed Origins**, which lists what is
permitted and lets you revoke any of it.

**When a page is refused, the tray offers it.** The blocked origin appears as
*"Allow example.com"* — one click admits it and persists the choice, no restart.
That is the intended way to add a console.

To preload one instead, at first run or for an unattended install:

```bash
./davi-nfc-agent -allowed-origins "console.example.com,localhost:3002"
# or
DAVI_NFC_ALLOWED_ORIGINS="console.example.com" ./davi-nfc-agent
```

Entries are matched on host:port. Full URLs are accepted and reduced, so
`https://console.example.com` and `console.example.com` are equivalent.

**Allow any origin (this session)** in the tray turns the check off until the
agent restarts. It is deliberately never persisted, and it is not a way to skip
configuring an origin — while it is on, any page the operator opens can read,
write and permanently lock cards.

> A trusted certificate is a separate requirement. The origin allowlist decides
> *who may connect*; TLS decides whether the browser will open the connection at
> all. A `wss://` connection to an untrusted certificate fails outright — unlike
> a page visit, there is no warning to click through. See
> [How devices trust the agent](#how-devices-trust-the-agent).

### How devices trust the agent

By default the agent serves a **self-signed certificate** using a key it
generates once and keeps. Nothing is added to any trust store.

Phones, readers and other native clients authenticate the agent by **pinning its
public key** rather than by trusting an authority. The pin is reported in the
registration response as `serverInfo.publicKeyPin`, logged at startup, and takes
the form `sha256/<base64>` over the SubjectPublicKeyInfo:

```
Agent public key pin: sha256/47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=
```

Record it when pairing and compare it on every later connection. **It survives
certificate reissues**, which happen whenever the host's addresses change, so a
device that pins it keeps working when the machine moves network. Pin this
value, never the certificate.

[Setting up an iOS or Android device](docs/device-setup.md) covers the pairing
flow and the trust-evaluation code for both platforms.

Browsers cannot pin, so they need a certificate they already trust. Three ways:

1. **Provide one** — point `-cert` / `-key` at a certificate for a name you
   control that resolves to the agent. Nothing is installed, and the browser
   trusts it because a public CA issued it. This is the option that scales.
2. **Trust This Agent in Browsers** — in the tray, or under *Device trust* in the
   [Control Center](docs/control-center.md). Creates a local certificate
   authority, installs it in the system trust store and reissues the agent's
   certificate under it. The operating system asks for a password, and the
   listeners restart so the new certificate is the one served. This is the same
   thing `-install-ca` does, without needing a terminal or a restart with flags.
3. **`-install-ca`** — the launch-flag equivalent of option 2, for a machine
   provisioned by a script.

> A certificate authority in a trust store can sign for **any** name, not just
> this agent. Whoever holds its key can intercept that machine's traffic, so
> option 1 is preferable wherever you can arrange it. An install that already
> has a CA keeps using it, so upgrading changes nothing for a console that
> works today.

By default the agent generates and persists a TLS certificate and an API secret
under a platform-specific config directory, so paired devices keep working
across restarts. Run `./davi-nfc-agent -help` for the full list of flags.

## Usage Examples

The agent runs a single server on one port that fills both roles, plus a bootstrap helper:
- **Agent Server** (port 9470): Serves both NFC devices (readers and smartphones, via `/ws?mode=device`) and client applications (via `/ws`) on the same port. The port is configurable via `-device-port`.
- **CA Bootstrap Server** (port 9472): Serves the root certificate for device setup, when a local CA is in use (`-install-ca`)

### JavaScript / TypeScript

Use the included [client library](docs/javascript-client.md) for browser or Node.js applications.

```javascript
const client = new NFCClient('http://localhost:9470');

client.on('tagData', (data) => {
  console.log('Card:', data.uid, data.text);
});

await client.connect();

// Write to a card
await client.write({
  records: [{ type: 'text', content: 'Hello, NFC!' }]
});
```

### Android (Kotlin)

Connect to the agent's client endpoint via WebSocket using OkHttp or similar.

```kotlin
val client = OkHttpClient()
val request = Request.Builder()
    .url("ws://192.168.1.100:9470/ws")
    .build()

val listener = object : WebSocketListener() {
    override fun onMessage(webSocket: WebSocket, text: String) {
        val msg = JSONObject(text)
        if (msg.getString("type") == "tagData") {
            val payload = msg.getJSONObject("payload")
            Log.d("NFC", "Card UID: ${payload.getString("uid")}")
        }
    }
}

client.newWebSocket(request, listener)
```

See [API Reference](docs/api.md) for the full WebSocket protocol.

### Use Your Phone as an NFC Reader

Connect your smartphone to the agent using the [NFCDeviceClient](docs/javascript-client.md#nfcdeviceclient-device-input).

```javascript
const device = new NFCDeviceClient('ws://192.168.1.100:9470');

device.on('registered', ({ deviceID }) => {
  console.log('Registered as:', deviceID);
});

await device.connect();

// Start scanning with WebNFC (Chrome on Android)
if (NFCDeviceClient.isWebNFCSupported()) {
  await device.startNFCScanning();
}
```

### Raw WebSocket

Connect directly without a client library. See [API Reference](docs/api.md) for all message types.

```javascript
const ws = new WebSocket('ws://localhost:9470/ws');

ws.onmessage = (event) => {
  const msg = JSON.parse(event.data);
  if (msg.type === 'tagData') {
    console.log('Card UID:', msg.payload.uid);
  }
};

// Write request
ws.send(JSON.stringify({
  type: 'writeRequest',
  payload: {
    records: [{ type: 'text', content: 'Hello!' }]
  }
}));
```

## Extending

**Building your own agent.** The agent is a regular Go module: import it and
`main.go` becomes yours, rather than the repository being something to fork. A
headless build without the tray or control center, or one with no hardware
backend at all, is a matter of which packages you import. See
[Go overview](docs/go-overview.md).

```bash
go get github.com/dotside-studios/davi-nfc-agent
```

**Adding hardware.** The NFC layer is interfaces the whole way down, so custom
readers and tag types sit beside the built-in PC/SC and smartphone support. See
[Extending NFC Support](docs/extending-nfc-support.md) to integrate your own
hardware or protocols.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, cross-compilation, and guidelines.

## License

[MIT License](LICENSE)

<hr />

Copyright © 2025-2026 Ned Palacios and Dotside Studios. All rights reserved.