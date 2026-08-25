# Davi NFC Agent

An NFC card reader agent with WebSocket broadcasting. It reads and writes
NDEF-formatted data from NFC tags and broadcasts it to connected clients in real
time, and provides the NFC functionality used by the [Davi](https://davi.social)
platform.

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
  writing, device revocation and the agent's settings
- **Buildable as a library**: Import the agent and write your own `main.go`, in
  any shape from the full tray application to a headless service
- **Assembled from plugins**: One method registers a background component, a
  route and a tray entry. The listener and the pairing server are plugins
  themselves
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
allowlist, and change the agent's settings. It is reachable from loopback only,
through a session the tray opens. Build with `-tags nowebui` to leave it out of
the binary. See [Control Center](docs/control-center.md).

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
host:port. Otherwise any site the operator visits could drive the reader,
including permanently locking cards. A console served from anywhere else, which
includes every hosted one, must be allowed. The Davi consoles already are.

When a page is refused, the tray offers it as *"Allow example.com"*, and one
click admits it. To preload one instead, at first run or for an unattended
install:

```bash
./davi-nfc-agent -allowed-origins "console.example.com,localhost:3002"
# or
DAVI_NFC_ALLOWED_ORIGINS="console.example.com" ./davi-nfc-agent
```

The allowlist lives in `allowed-origins.json` in the config directory and is
managed from the tray under **Allowed Origins**. A trusted certificate is a
separate requirement, and a failure of either looks the same from the page. See
[Control Center](docs/control-center.md) and
[API reference](docs/api.md#tls--certificates).

### How devices trust the agent

By default the agent serves a **self-signed certificate** using a key it
generates once and keeps. Nothing is added to any trust store.

Phones, readers and other native clients authenticate the agent by **pinning its
public key** rather than by trusting an authority. The pin is reported at
registration, logged at startup, and takes the form `sha256/<base64>` over the
SubjectPublicKeyInfo:

```
Agent public key pin: sha256/47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=
```

It survives certificate reissues, so a device that pins it keeps working when
the machine moves network. Pin this value, never the certificate.

Browsers cannot pin, so they need a certificate they already trust: point
`-cert` / `-key` at one you control, or use **Trust This Agent in Browsers** in
the tray (`-install-ca` on the command line) to create a local authority and
install it. A local authority can sign for any name, so prefer your own
certificate where you can arrange it.

The agent persists its certificate and API secret under a platform-specific
config directory, so paired devices keep working across restarts. Run
`./davi-nfc-agent -help` for the full list of flags. See
[Setting up an iOS or Android device](docs/device-setup.md) and
[API reference](docs/api.md#tls--certificates).

## Ports

The shipped agent listens on two ports, both configurable. Each is a plugin the
program registers, so a build can move either, or leave one out:

- **9470, agent server** (`-device-port`): One listener for both roles. NFC
  devices connect to `/ws?mode=device`, client applications to `/ws`, and the
  Control Center is served from the same port
- **9472, pairing server** (`-bootstrap-port`, `0` disables it): Serves the
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
packages it exports. Leaving out the tray, the control center or the hardware
backend is a matter of which packages you import.
See [Custom Builds](docs/custom-builds.md).

```bash
go get github.com/dotside-studios/davi-nfc-agent
```

### Plugins

An agent is made of what its program registers. A plugin is a value with one
method, handed everything it needs once, before the agent starts:

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

The listener, the pairing server and the certificate are plugins too, so a build
that registers none drives the reader and serves no HTTP at all. See
[Plugins](docs/custom-builds.md#plugins).

### NFC backends

The NFC layer takes custom readers and tag types beyond the built-in PC/SC and
smartphone backends. See [Extending NFC Support](docs/extending-nfc-support.md)
to integrate your own hardware or protocols.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, cross-compilation, and guidelines.

## License

[MIT License](LICENSE)

<hr />

Copyright © 2025-2026 Ned Palacios and Dotside Studios. All rights reserved.