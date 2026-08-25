# JavaScript Client Library

A framework-agnostic JavaScript client for integrating with the NFC Agent.

## Table of Contents

- [NFCClient (client role)](#nfcclient-client-role)
  - [Installation](#installation)
  - [Quick Start](#quick-start)
  - [Naming the tag](#naming-the-tag)
  - [API Reference](#api-reference)
  - [Examples](#examples)
  - [React](#react)
  - [TypeScript Support](#typescript-support)
- [NFCDeviceClient (Device Input)](#nfcdeviceclient-device-input)
  - [Installation](#installation-1)
  - [Quick Start](#quick-start-1)
  - [API Reference](#api-reference-1)
  - [NFC Integration Examples](#nfc-integration-examples)
    - [WebNFC (Browser)](#webnfc-browser)
    - [React Native NFC Manager](#react-native-nfc-manager)
    - [Node.js with External Reader](#nodejs-with-external-reader)
  - [TypeScript Support](#typescript-support-1)
  - [mDNS / Bonjour Discovery](#mdns--bonjour-discovery)
    - [Node.js](#nodejs)
    - [React Native](#react-native)
    - [Electron](#electron)

---

## NFCClient (client role)

Use `NFCClient` to consume NFC data as a client application. It connects to the
same agent port as [`NFCDeviceClient`](#nfcdeviceclient-device-input) (port 9470
by default, configurable via `-device-port`) — devices and clients share one
port, distinguished only by the connection path (`/ws?mode=device` for devices,
plain `/ws` for clients).

## Installation

The library is TypeScript, in `client/src`. There are two ways to take it.

**As source**, which is what the agent's own control center and the Davi apps
do — point your bundler at the package and import by name:

```ts
import { NFCClient } from "@davi/nfc-agent-client";
import { useNFCClient } from "@davi/nfc-agent-client/react";
```

**As a built file**, for a page with no build step. `client/dist` is generated
from the same source and committed:

```html
<script src="dist/nfc-client.js"></script>
<script>
  const client = new NFCClient("http://localhost:9470");
</script>
```

```bash
cp client/dist/nfc-client.js your-project/       # classic script, globals
cp client/dist/nfc-client.esm.js your-project/   # module build
cp -r client/dist/*.d.ts your-project/           # TypeScript declarations
```

Rebuild the distribution with `make client` after changing `client/src`.

## Quick Start

```javascript
// Create client instance
const client = new NFCClient('http://localhost:9470', {
  apiSecret: 'your-secret',  // Optional
  autoReconnect: true        // Auto-reconnect on disconnect
});

// Listen for tag scans
client.on('tagData', (data) => {
  console.log('Card UID:', data.uid);
  console.log('Card Type:', data.type);
  console.log('Text:', data.text);
});

// ...and for the tag leaving the field
client.on('tagRemoved', ({ uid }) => {
  console.log('Card removed:', uid);
});

// Connect to server
await client.connect();

// Write to the card on the reader
await client.write({
  records: [
    { type: 'text', content: 'Hello, NFC!' }
  ]
});

// Disconnect when done
await client.disconnect();
```

## Naming the tag

Every tag operation names the tag it applies to. The agent refuses one that does
not, with `TAG_NOT_NAMED`: it used to route by preference — its own reader while
a card was on it, otherwise whichever device had scanned most recently — which
meant a card lifted between the scan and the request silently redirected the
operation to a different tag.

`NFCClient` remembers the tag the agent last reported and names it, so the
common case needs nothing from the caller:

```javascript
client.on('tagData', async () => {
  await client.write({ records: [{ type: 'uri', content: 'https://example.com' }] });
});
```

Name a different one explicitly, or opt into the agent guessing:

```javascript
await client.write({ records, uid: '04A1B2C3' });
await client.write({ records, uid: '04A1B2C3', deviceID: 'phone-7' });
await client.write({ records, allowUntargeted: true });
```

`allowUntargeted` is per request rather than an agent-wide setting, so one
caller that cannot name its tag does not weaken the guarantee for the others.

## API Reference

### Constructor

```javascript
new NFCClient(serverUrl, options?)
```

| Parameter | Type | Description |
|-----------|------|-------------|
| `serverUrl` | string | Base URL of the NFC Agent server |
| `options.apiSecret` | string | Optional API secret for authentication |
| `options.autoReconnect` | boolean | Auto-reconnect on disconnect (default: true) |
| `options.reconnectDelay` | number | Delay before the first retry, in ms (default: 250). Doubles per attempt |
| `options.maxReconnectDelay` | number | Ceiling for that backoff, in ms (default: 5000) |
| `options.maxReconnectAttempts` | number | Attempts before giving up; 0 retries forever (default: 10) |

### Methods

#### `connect()`

Connect to the WebSocket server. First connection wins.

```javascript
await client.connect();
```

#### `disconnect()`

Disconnect from the server. Releases session automatically.

```javascript
await client.disconnect();
```

#### `write(request)`

Write NDEF data to a card, replacing whatever it holds. See
[Messages to Server](api.md#messages-to-server) for every record kind.

```javascript
const result = await client.write({
  records: [
    { type: 'text', content: 'Hello!', language: 'en' },
    { type: 'uri', content: 'https://example.com' }
  ]
});

result.verified;      // the agent read the data back and it matched
result.bytesWritten;  // how much landed
result.attempts;      // including retries on transient faults
```

Set `lock: true` to make the tag permanently read-only once the write lands, and
`idempotencyKey` to a stable value if you may retry after a lost response.

#### `lock(target?)`

Make a tag permanently read-only without writing to it. Irreversible.

```javascript
await client.lock();
```

#### `transceive(request)`

Exchange raw bytes with a tag. Takes and returns bytes; the base64 the wire uses
is the library's business.

```javascript
// PC/SC get-UID pseudo-APDU
const response = await client.transceive({
  data: new Uint8Array([0xff, 0xca, 0x00, 0x00, 0x00])
});
```

Set `raw: true` to exchange at the framing level instead of wrapping the bytes
as an APDU; a framing-level response carries no ISO 7816 status word.

#### `getCapabilities(target?)`

Ask the tag what it supports, rather than reading what was captured when it was
scanned.

```javascript
const caps = await client.getCapabilities();
if (caps.canWrite && !caps.isReadOnly) { /* ... */ }
```

#### `currentTag()`

The tag the agent last reported, or `null` once it left the field.

#### `isConnected()`

Check if WebSocket is connected.

```javascript
if (client.isConnected()) {
  // ...
}
```

#### `healthCheck()`

Perform REST API health check.

```javascript
const health = await client.healthCheck();
```

### Events

| Event | Payload | Description |
|-------|---------|-------------|
| `tagData` | Tag data object | Tag was scanned |
| `tagRemoved` | `{ uid }` | The tag left the field |
| `deviceStatus` | Status object | The agent's own reader changed state |
| `connected` | - | WebSocket connected |
| `disconnected` | - | WebSocket disconnected |
| `error` | `NFCErrorEvent` | Error occurred |

```javascript
client.on('tagData', (data) => { /* ... */ });
client.on('tagRemoved', ({ uid }) => { /* ... */ });
client.on('deviceStatus', (status) => { /* ... */ });
client.on('connected', () => { /* ... */ });
client.on('disconnected', () => { /* ... */ });
client.on('error', (err) => { /* ... */ });
```

`deviceStatus` describes the agent's own reader and nothing else, so its
`cardPresent` says nothing about a tag a paired phone is holding — and is false
the whole time one is. `tagRemoved` is the event to act on.

### Errors

A refused operation rejects with an `NFCRequestError` carrying the agent's
[error code](api.md#error-codes) and whether repeating the request could
plausibly succeed:

```javascript
try {
  await client.write({ records });
} catch (err) {
  if (err.retryable) {
    // TAG_REMOVED, WRITE_FAILED, NO_CARD — present the tag again
  } else {
    // READ_ONLY, CAPACITY_EXCEEDED, TAG_MISMATCH — retrying wastes a round trip
    console.error(err.code, err.message);
  }
}
```

## Examples

### Simple Tag Reader

```javascript
const client = new NFCClient('http://localhost:9470');

client.on('tagData', (data) => {
  document.getElementById('uid').textContent = data.uid;
  document.getElementById('text').textContent = data.text;
});

await client.connect();
```

### Write to Card

```javascript
const client = new NFCClient('http://localhost:9470');
await client.connect();

// Write single text record
await client.write({
  records: [{ type: 'text', content: 'Hello, NFC!' }]
});

// Write multiple records
await client.write({
  records: [
    { type: 'text', content: 'Welcome!' },
    { type: 'uri', content: 'https://example.com' }
  ]
});
```

### Append to Existing Data

```javascript
const client = new NFCClient('http://localhost:9470');
await client.connect();

client.on('tagData', async (data) => {
  if (!data.message) return;

  // Extract existing records
  const existingRecords = data.message.records.map(r => ({
    type: r.type,
    content: r.content,
    language: r.language
  }));

  // Append new record
  await client.write({
    records: [
      ...existingRecords,
      { type: 'text', content: 'Appended record' }
    ]
  });
});
```

### With Error Handling

```javascript
const client = new NFCClient('http://localhost:9470');

client.on('error', (err) => {
  console.error('NFC Error:', err);
});

client.on('disconnected', () => {
  console.log('Disconnected - will auto-reconnect');
});

try {
  await client.connect();
  await client.write({
    records: [{ type: 'text', content: 'Hello!' }]
  });
} catch (err) {
  console.error('Failed:', err);
}
```

## React

`@davi/nfc-agent-client/react` wraps the client in a hook that owns the
connection for the lifetime of the component:

```tsx
const {
  connectionStatus, lastTag, capabilities, diagnosis, reconnect,
  write, lock, transceive, refreshCapabilities,
} = useNFCClient("https://localhost:9470");
```

`diagnosis` is why the connection failed, in terms an operator can act on. A
WebSocket failure carries no detail — a refused connection, a rejected
certificate and a blocked origin all surface as the same contentless event — so
it comes from probing the agent over HTTP instead. `diagnoseAgent(url)` is the
same probe, for a consumer not using React.

## TypeScript Support

The library is written in TypeScript, so importing it from source needs nothing
extra. For the built distribution, declarations are generated alongside it:

```typescript
import { NFCClient, type TagData, type WriteRequest } from '@davi/nfc-agent-client';

const client = new NFCClient('http://localhost:9470');

client.on('tagData', (data: TagData) => {
  console.log(data.uid);
});
```

See `client/src/session/types.ts` for the full definitions.

---

# NFCDeviceClient (Device Input)

Use `NFCDeviceClient` to connect to the **agent** (port 9470) as an NFC device. This is a universal library that works in both Node.js and browser environments, allowing any NFC-capable device to act as a reader.

The library is **NFC-source agnostic** - integrate with any NFC library (WebNFC, React Native NFC Manager, etc.) by calling `scanTag()` when your NFC library detects a tag.

## Installation

### Browser

```html
<script src="nfc-device-client.js"></script>
```

### Node.js

```bash
cp client/nfc-device-client.js your-project/
npm install ws  # Or any WebSocket-compatible package
```

```javascript
const NFCDeviceClient = require('./nfc-device-client');
const WebSocket = require('ws');

const client = new NFCDeviceClient('ws://localhost:9470', {
  WebSocket: WebSocket,  // Pass your WebSocket class
  deviceName: 'Node.js Reader',
  platform: 'node'
});
```

## Quick Start

```javascript
const client = new NFCDeviceClient('ws://localhost:9470', {
  deviceName: 'My NFC Reader',
  platform: 'web',
  nfcType: 'webnfc'  // Describe your NFC source
});

// Listen for registration
client.on('registered', ({ deviceID }) => {
  console.log('Registered as device:', deviceID);
});

// Listen for write requests from server
client.on('writeRequest', ({ requestID, ndefMessage }) => {
  console.log('Write request:', ndefMessage);
  // Handle write with your NFC library, then respond
  client.respondToWrite(requestID, true);
});

// Connect to server
await client.connect();

// When your NFC library detects a tag, call scanTag()
await client.scanTag({
  uid: '04:AB:CD:EF:12:34:56',
  type: 'MIFARE Classic 1K',
  ndefMessage: { type: 'ndef', records: [...] }
});
```

## API Reference

### Constructor

```javascript
new NFCDeviceClient(serverUrl, options?)
```

| Parameter | Type | Description |
|-----------|------|-------------|
| `serverUrl` | string | Agent URL (e.g., `ws://localhost:9470`) |
| `options.WebSocket` | class | Custom WebSocket class (required in Node.js, optional in browser) |
| `options.deviceName` | string | Device name for registration (default: `'NFC Device'`) |
| `options.platform` | string | Platform identifier: `'web'`, `'ios'`, `'android'`, `'node'` (default: `'unknown'`) |
| `options.appVersion` | string | App version (default: `'1.0.0'`) |
| `options.canRead` | boolean | Device can read tags (default: `true`) |
| `options.canWrite` | boolean | Device can write tags (default: `false`) |
| `options.nfcType` | string | NFC library type: `'webnfc'`, `'react-native-nfc'`, `'custom'` (default: `'custom'`) |
| `options.autoHeartbeat` | boolean | Send heartbeats automatically (default: `true`) |
| `options.heartbeatInterval` | number | Heartbeat interval in ms (default: `30000`) |
| `options.autoReconnect` | boolean | Auto-reconnect on disconnect (default: `true`) |
| `options.reconnectDelay` | number | Delay before reconnecting in ms (default: `3000`) |

### Methods

#### `connect()`

Connect to the agent and register as a device.

```javascript
await client.connect();
```

#### `disconnect()`

Disconnect from the server.

```javascript
await client.disconnect();
```

#### `scanTag(tagData)`

Send a tag scan event to the server. Call this when your NFC library detects a tag.

```javascript
await client.scanTag({
  uid: '04:AB:CD:EF:12:34:56',
  technology: 'ISO14443A',        // Optional, default: 'ISO14443A'
  type: 'MIFARE Classic 1K',      // Optional, default: 'Unknown'
  atr: '',                        // Optional
  scannedAt: new Date().toISOString(),  // Optional, auto-set if not provided
  ndefMessage: {                  // Optional
    type: 'ndef',
    records: [{ type: 'T', text: 'Hello', language: 'en' }]
  },
  rawData: null                   // Optional, base64 encoded
});
```

#### `removeTag(uid)`

Notify server that a tag was removed from the reader.

```javascript
await client.removeTag('04:AB:CD:EF:12:34:56');
```

#### `respondToWrite(requestID, success, error?)`

Respond to a write request from the server.

```javascript
client.respondToWrite(requestID, true);
// or on failure:
client.respondToWrite(requestID, false, 'Write failed: card removed');
```

#### `getDeviceID()`

Get assigned device ID after registration.

```javascript
const deviceID = client.getDeviceID();
```

#### `getServerInfo()`

Get server info received during registration.

```javascript
const serverInfo = client.getServerInfo();
// { version: '1.0.0', supportedNFC: ['ndef', 'mifare'] }
```

#### `isConnected()`

Check if connected and registered.

```javascript
if (client.isConnected()) {
  // Ready to send/receive
}
```

### Events

| Event | Payload | Description |
|-------|---------|-------------|
| `registered` | `{ deviceID, serverInfo }` | Successfully registered with server |
| `writeRequest` | `{ requestID, deviceID, ndefMessage, ndefBytes, tagUID, lock, idempotencyKey }` | Server requests a write operation. `lock` with no `ndefMessage`/`ndefBytes` is a lock-only request — lock the tag as it stands and write nothing |
| `connected` | `{}` | WebSocket connected |
| `disconnected` | `{}` | WebSocket disconnected |
| `error` | `{ error, phase?, code? }` | Error occurred |

```javascript
client.on('registered', ({ deviceID }) => { /* ... */ });
client.on('writeRequest', ({ requestID, ndefMessage }) => { /* ... */ });
client.on('connected', () => { /* ... */ });
client.on('disconnected', () => { /* ... */ });
client.on('error', ({ error, phase }) => { /* ... */ });
```

---

## NFC Integration Examples

The `NFCDeviceClient` is NFC-source agnostic. Below are examples of integrating with popular NFC libraries.

### WebNFC (Browser)

WebNFC is available in Chrome on Android. Implement NFC scanning in your application code:

```javascript
const client = new NFCDeviceClient('ws://localhost:9470', {
  deviceName: 'Chrome NFC Reader',
  platform: 'web',
  nfcType: 'webnfc',
  canWrite: true
});

let nfcReader = null;
let nfcAbortController = null;

// Check WebNFC support
function isWebNFCSupported() {
  return 'NDEFReader' in window;
}

// Start WebNFC scanning
async function startNFCScanning() {
  if (!isWebNFCSupported()) {
    throw new Error('WebNFC not supported');
  }

  nfcAbortController = new AbortController();
  nfcReader = new NDEFReader();

  nfcReader.onreading = async (event) => {
    const { serialNumber, message } = event;

    // Convert NDEF message to protocol format
    const records = [];
    for (const record of message.records) {
      const recordData = {
        type: record.recordType,
        mediaType: record.mediaType
      };

      if (record.recordType === 'text') {
        const decoder = new TextDecoder(record.encoding || 'utf-8');
        recordData.text = decoder.decode(record.data);
        recordData.language = record.lang;
      } else if (record.recordType === 'url') {
        const decoder = new TextDecoder();
        recordData.uri = decoder.decode(record.data);
      }

      records.push(recordData);
    }

    // Send to server
    await client.scanTag({
      uid: serialNumber.replace(/:/g, ''),
      technology: 'NFC',
      type: 'NDEF',
      ndefMessage: { type: 'ndef', records }
    });
  };

  nfcReader.onreadingerror = (error) => {
    console.error('NFC reading error:', error);
  };

  await nfcReader.scan({ signal: nfcAbortController.signal });
}

// Stop scanning
function stopNFCScanning() {
  if (nfcAbortController) {
    nfcAbortController.abort();
    nfcAbortController = null;
  }
  nfcReader = null;
}

// Handle write requests
client.on('writeRequest', async ({ requestID, ndefMessage }) => {
  try {
    const writer = new NDEFReader();
    const records = ndefMessage.records.map(r => {
      if (r.type === 'text' || r.type === 'T') {
        return { recordType: 'text', data: r.text || r.content, lang: r.language || 'en' };
      } else if (r.type === 'uri' || r.type === 'U') {
        return { recordType: 'url', data: r.uri || r.content };
      }
      return r;
    });
    await writer.write({ records });
    client.respondToWrite(requestID, true);
  } catch (err) {
    client.respondToWrite(requestID, false, err.message);
  }
});

// Connect and start scanning
await client.connect();
if (isWebNFCSupported()) {
  await startNFCScanning();
}
```

### React Native NFC Manager

For React Native apps using [react-native-nfc-manager](https://github.com/revtel/react-native-nfc-manager):

```javascript
import NfcManager, { NfcTech, Ndef } from 'react-native-nfc-manager';
import NFCDeviceClient from './nfc-device-client';

const client = new NFCDeviceClient('ws://your-server:9470', {
  deviceName: 'React Native App',
  platform: Platform.OS,  // 'ios' or 'android'
  nfcType: 'react-native-nfc',
  canWrite: true
});

// Initialize NFC
async function initNFC() {
  await NfcManager.start();
  await client.connect();
}

// Read NFC tags
async function scanTag() {
  try {
    await NfcManager.requestTechnology(NfcTech.Ndef);

    const tag = await NfcManager.getTag();
    const ndefRecords = await NfcManager.ndefHandler.getNdefMessage();

    // Convert to protocol format
    const records = ndefRecords?.map(record => {
      const decoded = Ndef.text.decodePayload(record.payload);
      return {
        type: record.tnf === Ndef.TNF_WELL_KNOWN ? 'T' : 'unknown',
        text: decoded,
        language: 'en'
      };
    }) || [];

    // Send to server
    await client.scanTag({
      uid: tag.id,
      technology: tag.techTypes?.[0] || 'NfcA',
      type: tag.type || 'Unknown',
      ndefMessage: { type: 'ndef', records }
    });

  } finally {
    NfcManager.cancelTechnologyRequest();
  }
}

// Handle write requests
client.on('writeRequest', async ({ requestID, ndefMessage }) => {
  try {
    await NfcManager.requestTechnology(NfcTech.Ndef);

    const bytes = ndefMessage.records.map(r => {
      if (r.type === 'text' || r.type === 'T') {
        return Ndef.textRecord(r.text || r.content, r.language || 'en');
      } else if (r.type === 'uri' || r.type === 'U') {
        return Ndef.uriRecord(r.uri || r.content);
      }
    }).filter(Boolean);

    await NfcManager.ndefHandler.writeNdefMessage(bytes);
    client.respondToWrite(requestID, true);

  } catch (err) {
    client.respondToWrite(requestID, false, err.message);
  } finally {
    NfcManager.cancelTechnologyRequest();
  }
});
```

### Node.js with External Reader

For Node.js applications using external NFC readers (e.g., via serial port or USB):

```javascript
const NFCDeviceClient = require('./nfc-device-client');
const WebSocket = require('ws');

const client = new NFCDeviceClient('ws://localhost:9470', {
  WebSocket: WebSocket,
  deviceName: 'Node.js NFC Reader',
  platform: 'node',
  nfcType: 'custom'
});

// Your NFC reader library
const nfcReader = require('your-nfc-library');

client.on('registered', ({ deviceID }) => {
  console.log('Registered as:', deviceID);
});

client.on('error', ({ error }) => {
  console.error('Client error:', error);
});

await client.connect();

// When your reader detects a tag
nfcReader.on('tag', async (tag) => {
  await client.scanTag({
    uid: tag.uid,
    type: tag.type,
    technology: 'ISO14443A',
    ndefMessage: tag.ndef ? { type: 'ndef', records: tag.ndef.records } : null
  });
});

nfcReader.on('removed', async (uid) => {
  await client.removeTag(uid);
});
```

---

## TypeScript Support

TypeScript definitions are provided in `nfc-device-client.d.ts`:

```typescript
import { NFCDeviceClient, DeviceTagData, WriteRequestEvent } from './nfc-device-client';

const client = new NFCDeviceClient('ws://localhost:9470', {
  deviceName: 'TypeScript Client',
  nfcType: 'custom'
});

client.on('writeRequest', (event: WriteRequestEvent) => {
  console.log(event.requestID);
});

const tagData: DeviceTagData = {
  uid: '04:AB:CD:EF:12:34:56',
  type: 'MIFARE Classic 1K'
};

await client.scanTag(tagData);
```

See `client/nfc-device-client.d.ts` for full type definitions.

---

## mDNS / Bonjour Discovery

The agent advertises itself via mDNS/Bonjour, allowing clients to discover the server on the local network without knowing the IP address.

**Service Details:**
- **Service Type:** `_nfc-device._tcp`
- **Domain:** `local.`

### Node.js

Using [bonjour-service](https://github.com/onlxltd/bonjour-service):

```javascript
const { Bonjour } = require('bonjour-service');
const NFCDeviceClient = require('./nfc-device-client');
const WebSocket = require('ws');

const bonjour = new Bonjour();

// Find NFC Agent servers on the network
const browser = bonjour.find({ type: 'nfc-device' }, (service) => {
  console.log('Found NFC Agent:', service.name);
  console.log('  Host:', service.host);
  console.log('  Port:', service.port);
  console.log('  Addresses:', service.addresses);

  // Connect to the discovered server
  const serverUrl = `ws://${service.addresses[0]}:${service.port}`;

  const client = new NFCDeviceClient(serverUrl, {
    WebSocket: WebSocket,
    deviceName: 'Auto-discovered Client',
    platform: 'node'
  });

  client.on('registered', ({ deviceID }) => {
    console.log('Connected to:', service.name, 'as', deviceID);
  });

  client.connect();
});

// Stop browsing after 10 seconds
setTimeout(() => {
  browser.stop();
  bonjour.destroy();
}, 10000);
```

### React Native

Using [react-native-zeroconf](https://github.com/balthazar/react-native-zeroconf):

```javascript
import Zeroconf from 'react-native-zeroconf';
import NFCDeviceClient from './nfc-device-client';

const zeroconf = new Zeroconf();

// Start scanning for NFC Agent servers
zeroconf.scan('nfc-device', 'tcp', 'local.');

zeroconf.on('resolved', (service) => {
  console.log('Found NFC Agent:', service.name);

  const serverUrl = `ws://${service.addresses[0]}:${service.port}`;

  const client = new NFCDeviceClient(serverUrl, {
    deviceName: 'React Native App',
    platform: Platform.OS
  });

  client.on('registered', ({ deviceID }) => {
    console.log('Connected as:', deviceID);
    // Stop scanning once connected
    zeroconf.stop();
  });

  client.connect();
});

zeroconf.on('error', (err) => {
  console.error('Zeroconf error:', err);
});

// Stop scanning after 30 seconds if no server found
setTimeout(() => zeroconf.stop(), 30000);
```

### Electron

For Electron apps, you can use Node.js mDNS libraries in the main process:

```javascript
// main.js (main process)
const { Bonjour } = require('bonjour-service');
const { ipcMain } = require('electron');

const bonjour = new Bonjour();

ipcMain.handle('discover-nfc-servers', () => {
  return new Promise((resolve) => {
    const servers = [];

    const browser = bonjour.find({ type: 'nfc-device' }, (service) => {
      servers.push({
        name: service.name,
        host: service.host,
        port: service.port,
        addresses: service.addresses
      });
    });

    setTimeout(() => {
      browser.stop();
      resolve(servers);
    }, 5000);
  });
});

// renderer.js (renderer process)
const servers = await window.electronAPI.discoverNFCServers();
if (servers.length > 0) {
  const server = servers[0];
  const client = new NFCDeviceClient(`ws://${server.addresses[0]}:${server.port}`, {
    deviceName: 'Electron App',
    platform: 'electron'
  });
  await client.connect();
}
```
