# Davi NFC Agent

A lightweight NFC card reader agent with WebSocket broadcasting. It reads and
writes NDEF-formatted data from NFC tags and broadcasts it to connected clients
in real time, and provides the NFC functionality used by the
[Davi](https://davi.social) platform.

## Features

- **Readers and phones at once**: PC/SC readers, smartphones and WebNFC
  browsers feed the same agent, and clients read every scan from one WebSocket
- **Rich NDEF read and write**: Text, URI, smart poster, vCard, MIME, geo,
  tel/sms/mailto, Android Application Records, and raw records built by hand
- **Reliable writes**: Read-after-write verification, bounded retries on
  transient failures, and a pre-flight check that the message fits
- **Tag locking and erase**: Make a tag read-only, or wipe it back to an empty
  NDEF message
- **Capabilities with every scan**: Memory size, usable NDEF capacity, and
  whether the tag supports writing, locking and password protection
- **TLS by default**: WSS with key pinning for devices, an origin allowlist for
  browsers, and optional API-secret authentication
- **Per-device pairing**: Each device gets its own revocable credential, issued
  against a PIN
- **Zero-config discovery**: Advertised over mDNS/Bonjour, so a device finds
  the agent without being told where it is
- **Reader feedback**: ACR122 readers flash their LED and beep at what they
  read and write, once the operator turns it on
- **System tray**: Reader selection, status, and device management
- **Control Center**: A built-in web console for the log, tag inspection, NDEF
  writing, device revocation and settings that survive a restart
- **Buildable as a library**: Import the agent and write your own `main.go`, in
  any shape from the full tray application to a headless service
- **Assembled from plugins**: One method registers a background component, a
  route and a tray entry. The listener and the pairing server are plugins
  themselves, so a build is what it registers
- **Cross-platform**: Linux, macOS and Windows

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
go build ./cmd/davi-nfc-agent
./davi-nfc-agent
```

See the [Installation Guide](docs/installation.md) for platform-specific setup
and troubleshooting, and [Setting up an iOS or Android device](docs/device-setup.md)
for pairing a phone.

### Control Center

Choose **Open Control Center** from the tray to manage the agent in a browser:
read its log, inspect and write tags, revoke a paired device, edit the origin
allowlist, and change settings that survive a restart.

The console is privileged — it can rotate the API secret, revoke a device's
credential and lock a tag irreversibly — so every request to it must clear three
checks. It has to come from loopback, from a page the agent itself served, and
carry a session opened through that tray entry. There is no other way in, which
is deliberate. See [Control Center](docs/control-center.md) for the detail.

To leave it out of the binary entirely, build with `-tags nowebui`. Neither the
privileged API nor the console's frontend is compiled in, and `/control` serves
the same plain-text banner as `/`:

```bash
go build -tags nowebui ./cmd/davi-nfc-agent
```

### Reader Feedback

**Flash and Beep on Scan** in the tray menu has the reader announce its own
work: one green flash with a short beep when a tag is read or written, two red
flashes when a write or a lock fails. It is off by default, and turning it on
lasts as long as the agent runs.

The commands come from the ACS ACR122U instruction set, so ACR122 readers
answer them and other readers report the feature as unsupported and are left
alone. They are sent with `SCardControl`, falling back to a pseudo-APDU over
the card connection where the PC/SC stack will not carry escape commands. See
[Installation](docs/installation.md#reader-led-and-buzzer) for the two stacks
that need configuring.

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

## Ports

The shipped agent listens on two ports, both configurable. Each is a plugin the
program registers, so a build can move either, or leave one out:

- **9470 — agent server** (`-device-port`): One listener for both roles. NFC
  devices connect to `/ws?mode=device`, client applications to `/ws`, and the
  Control Center is served from the same port
- **9472 — pairing server** (`-bootstrap-port`, `0` disables it): Serves the
  page a phone opens to pair, and issues each device its own credential against
  the PIN printed at startup. Where a local CA is in use, it hands that out too,
  to a request carrying the PIN

## Usage Examples

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

The agent is a Go module, and the binary is an ordinary program built from the
packages it exports. A build without the tray or the control center, or one with
no hardware backend at all, is a matter of which packages you import. See
[Custom Builds](docs/custom-builds.md).

```bash
go get github.com/dotside-studios/davi-nfc-agent
```

### Plugins

What an agent is made of is what its program registers. A plugin is a value with
one method, handed everything it needs once, before the agent starts:

```go
type BackupPlugin struct {
	Every time.Duration
}

func (p *BackupPlugin) Activate(ctx agent.AgentContext) error {
	backups := &backupWorker{every: p.Every, dir: ctx.ConfigDir()}

	ctx.Systray.Add("Back Up Now", traymenu.OnClick(backups.Run))
	return ctx.Use(backups)
}
```

```go
rt.Agent.Plugins.Add(&BackupPlugin{Every: time.Hour})
```

`ctx.Use` registers something to start and stop with the agent, `ctx.Mount` adds
a route to the listener, and `ctx.Systray` is the tray's own menu, so a plugin's
entry sits beside the ones the tray declared itself. Nothing is loaded at run
time: which plugins a build has is decided by what it imports, so one left out
takes its dependencies with it.

The listener, the pairing server and the certificate are plugins too.
`agent.ServerPlugin` owns the port and everything served from it,
`agent.PairingPlugin` the pairing listener and the entries that hand out its
PIN, and `agent.TrustPlugin` the certificate the other two are configured from.
Register none and the agent drives the reader and serves no HTTP at all. See
[Plugins](docs/custom-builds.md#plugins).

### NFC backends

The agent's modular NFC layer supports adding custom readers and tag types beyond the built-in PC/SC and smartphone support. See [Extending NFC Support](docs/extending-nfc-support.md) to integrate your own hardware or protocols.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, cross-compilation, and guidelines.

## License

[MIT License](LICENSE)

<hr />

Copyright © 2025-2026 Ned Palacios and Dotside Studios. All rights reserved.