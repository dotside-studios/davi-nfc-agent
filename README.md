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
  writing, device revocation and the reader's preferences
- **Buildable as a library**: Import the agent and write your own `main.go`, in
  any shape from the full tray application to a headless service
- **Assembled from plugins**: One method registers a background component, a
  route and a tray entry. The listener and the pairing server are plugins
  themselves
- **Subscribable**: Scans, lifecycle state, preferences, connected clients,
  pairings and blocked origins are published as typed signals, so an embedding
  program follows the agent without polling it
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
and troubleshooting, [Setting up an iOS or Android device](docs/device-setup.md)
for pairing a phone, and [Control Center](docs/control-center.md) for the
built-in web console.

### Command-line Options

```bash
./davi-nfc-agent                       # System tray mode (default)
./davi-nfc-agent -version              # Print version information and exit
./davi-nfc-agent -device "ACS ACR122U" # Use a specific PC/SC reader by name
./davi-nfc-agent -device-port 9480     # Custom agent server port (default 9470, serves both devices and clients)
./davi-nfc-agent -api-secret mysecret  # Set the API authentication secret
./davi-nfc-agent -allowed-origins app.example.com  # Let a hosted web console connect
./davi-nfc-agent -require-paired-devices  # Admit only devices that have paired
./davi-nfc-agent -allow-loopback-bypass   # Admit connections from this host with no secret (off by default)
./davi-nfc-agent -allow-raw-apdu          # Open the raw APDU channel so clients can send raw exchanges (off by default)
./davi-nfc-agent -install-ca           # Trust this agent in browsers (installs a local CA)
./davi-nfc-agent -auto-tls=false       # Disable automatic TLS certificate management
./davi-nfc-agent -cert cert.pem -key key.pem  # Use your own TLS certificate
./davi-nfc-agent -config-dir ./config  # Override the config directory
```

## Usage Examples

The agent listens on two ports, both configurable:
- **Agent server** (port 9470): NFC devices connect to `/ws?mode=device`, client
  applications to `/ws`, devices pair at `/pair`, and the Control Center is
  served from the same port
- **Bootstrap server** (port 9472): serves the page a phone opens to set itself
  up, and hands out the local certificate authority. Cleartext, because it runs
  before the device trusts the agent's certificate

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
backend is a matter of which packages you import, and the listener, the pairing
server and the certificate are plugins a program registers. See
[Custom Builds](docs/custom-builds.md).

```bash
go get github.com/dotside-studios/davi-nfc-agent
```

The NFC layer takes custom readers and tag types beyond the built-in PC/SC and
smartphone backends. See [Extending NFC Support](docs/extending-nfc-support.md)
to integrate your own hardware or protocols.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for development setup, cross-compilation, and guidelines.

## License

[MIT License](LICENSE)

<hr />

Copyright © 2025-2026 Ned Palacios and Dotside Studios. All rights reserved.