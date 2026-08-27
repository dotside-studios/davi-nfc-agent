# JavaScript Client Library

A framework-agnostic JavaScript client for integrating with the NFC Agent.

## Table of Contents

- [NFCClient (client role)](#nfcclient-client-role)
  - [Installation](#installation)
  - [Quick Start](#quick-start)
  - [Naming the tag](#naming-the-tag)
  - [API Reference](#api-reference)
  - [Examples](#examples)
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
by default, configurable via `-device-port`): devices and clients share one
port, distinguished only by the connection path (`/ws?mode=device` for devices,
plain `/ws` for clients).

## Installation

The library is TypeScript, in `client/src`. With a bundler, import it by name:

```ts
import { NFCClient } from "@davi/nfc-agent-client";
import { useNFCClient } from "@davi/nfc-agent-client/react";
```

Without one, copy a built file. `client/dist` is generated from the same
source and committed:

```bash
cp client/dist/nfc-client.js your-project/       # classic script, globals
cp client/dist/nfc-client.esm.js your-project/   # module build
cp client/dist/*.d.ts your-project/              # For TypeScript
```

Or include directly in HTML:

```html
<script src="nfc-client.js"></script>
```

Rebuild `client/dist` with `make client` after changing `client/src`.

## Quick Start

```javascript
// Create client instance
const client = new NFCClient('http://localhost:9470', {
  apiSecret: 'your-secret',  // Required whenever the agent has one set
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

// Listen for device status
client.on('deviceStatus', (status) => {
  console.log('Device connected:', status.connected);
});

// Connect to server
await client.connect();

// Write to a card
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
not, with `TAG_NOT_NAMED`. See [Naming the Tag](api.md#naming-the-tag) for why.

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

Giving both `uid` and `deviceID` holds that device to that tag, so an id
remembered from an earlier scan cannot act on whatever it is holding now. The
client forgets its tag when the tag leaves, so a write after removal is refused
rather than landing on whatever appears next.

**The reader the operator picked.** When a reader is selected in the console or
the tray, that reader's tags are the only ones broadcast and the only ones a
request can reach. Naming another reader, or a UID only another reader has seen,
fails with `NO_CARD`, and `allowUntargeted` resolves among the selected reader
alone. Paired phones are unaffected. A client naming the tag it was last told
about needs no change.

## API Reference

### Constructor

```javascript
new NFCClient(serverUrl, options?)
```

| Parameter | Type | Description |
|-----------|------|-------------|
| `serverUrl` | string | Base URL of the NFC Agent server |
| `options.apiSecret` | string | The agent's API secret. Required whenever the agent has one set, from the agent's own host too. See [The loopback bypass](api.md#the-loopback-bypass) |
| `options.autoReconnect` | boolean | Auto-reconnect on disconnect (default: true) |
| `options.reconnectDelay` | number | Milliseconds before the first retry, doubling per attempt (default: 250) |
| `options.maxReconnectDelay` | number | Ceiling for that doubling (default: 5000) |
| `options.maxReconnectAttempts` | number | Attempts before giving up; 0 retries forever (default: 10) |

### Methods

#### `connect()`

Connect to the WebSocket server. First connection wins.

```javascript
await client.connect();
```

#### `disconnect()`

Disconnect from the server. Releases session automatically. Requests still in
flight are rejected rather than left to time out.

```javascript
await client.disconnect();
```

#### `write(request)`

Write NDEF data to a card, replacing whatever it holds. Every record kind in
[Messages to Server](api.md#messages-to-server) is accepted.

```javascript
const result = await client.write({
  records: [
    { type: 'text', content: 'Hello!', language: 'en' },
    { type: 'uri', content: 'https://example.com' }
  ]
});

result.verified;      // the agent read the data back and it matched
result.bytesWritten;
result.attempts;      // including retries on transient faults
```

Set `lock: true` to make the tag permanently read-only once the write lands, and
`idempotencyKey` to a stable value if you may retry after a lost response.

#### `lock(target?)`

Make a tag permanently read-only without writing to it. Irreversible; see
[Locking Tags](api.md#locking-tags-make-read-only).

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

// NTAG GET_VERSION, at the framing level
await client.transceive({ data: new Uint8Array([0x60]), raw: true });
```

`raw` exchanges at the framing level instead of wrapping the bytes as an APDU; a
framing-level response carries no ISO 7816 status word.

#### `getCapabilities(target?)`

Ask the tag what it supports, rather than reading what was captured when it was
scanned.

```javascript
const caps = await client.getCapabilities();
if (caps.canWrite && !caps.isReadOnly) { /* ... */ }
```

An undefined field means the agent did not say, which is not the same as
`false`. See [Tag Capabilities](api.md#tag-capabilities).

#### `currentTag()`

The tag the agent last reported, or `null` once it left the field. This is what
an untargeted request names.

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

#### `diagnoseAgent(serverUrl)`

A standalone export, not a method. A failed WebSocket carries no detail: a
refused connection, an untrusted certificate and a blocked origin all arrive as
the same empty `error` event, so this probes the agent over HTTP instead.

```javascript
const why = await diagnoseAgent('https://localhost:9470');
show(why.title, why.detail);
if (why.openUrl) offerLink(why.openUrl);
```

| `kind` | Cause |
|--------|-------|
| `origin-blocked` | The agent answers over HTTP, so it is running and trusted. This page's origin is not allowed |
| `wrong-scheme` | The page is on `https`, the agent serves plain `http` |
| `unreachable` | The agent is not running, or its certificate is untrusted. Indistinguishable from here, so `openUrl` is set for the operator to open |

A working health check does not mean the WebSocket will connect.

`encodeBase64(bytes)` and `decodeBase64(value)` are exported alongside it, for
the byte slices the wire carries as base64.

### Events

| Event | Payload | Description |
|-------|---------|-------------|
| `tagData` | Tag data object | Tag was scanned |
| `tagRemoved` | `{ uid }` | The tag left the field |
| `deviceStatus` | Status object | The agent's own reader changed state |
| `connected` | - | WebSocket connected |
| `disconnected` | - | WebSocket disconnected |
| `error` | Error object | Error occurred |

```javascript
client.on('tagData', (data) => { /* ... */ });
client.on('tagRemoved', ({ uid }) => { /* ... */ });
client.on('deviceStatus', (status) => { /* ... */ });
client.on('connected', () => { /* ... */ });
client.on('disconnected', () => { /* ... */ });
client.on('error', (err) => { /* ... */ });
```

`deviceStatus` describes the agent's own reader, so its `cardPresent` says
nothing about a tag a paired phone is holding, and is false
the whole time one is. `tagRemoved` is the event to act on. Its `device` names
the reader it describes; agents that do not send it leave it undefined.

### Errors

A refused operation rejects with an `NFCRequestError` carrying the agent's
[error code](api.md#error-codes) and whether repeating the request could
plausibly succeed.

A connection serves one request at a time and queues a bounded number of the
rest. Beyond that the agent answers `BUSY`, which is retryable, so await
operations rather than fanning them out. Requests in flight are cancelled when
the connection drops; the library rejects them on close as it already did.

```javascript
try {
  await client.write({ records });
} catch (err) {
  if (err.retryable) {
    // TAG_REMOVED, WRITE_FAILED, NO_CARD: present the tag again
    // BUSY: earlier work has not finished; retry once it answers
  } else if (err.code === 'MULTIPLE_TAGS') {
    // More than one card in the field; retrying changes nothing until they
    // are separated.
  } else {
    // READ_ONLY, CAPACITY_EXCEEDED, TAG_MISMATCH: retrying wastes a round trip
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

### React

`@davi/nfc-agent-client/react` wraps the client in a hook that owns the
connection for the lifetime of the component.

```tsx
const {
  connectionStatus, lastTag, capabilities, diagnosis, reconnect,
  write, lock, transceive, refreshCapabilities,
} = useNFCClient('https://localhost:9470');

if (connectionStatus !== 'connected') return <p>Connecting to the reader…</p>;
if (!lastTag) return <p>Present a card.</p>;

return <button onClick={() => write({ records })}>Write to {lastTag.uid}</button>;
```

`serverUrl` and the options are read on mount, so remount the component to point
it elsewhere. `diagnosis` is `diagnoseAgent`'s answer, run for you when a
connection attempt fails.

## TypeScript Support

The library is written in TypeScript, so importing it from source needs nothing
extra. Declarations are generated alongside the built files for the copied case:

```typescript
import { NFCClient, type TagData, type WriteRequest } from '@davi/nfc-agent-client';

const client = new NFCClient('http://localhost:9470');

client.on('tagData', (data: TagData) => {
  console.log(data.uid);
});
```

Exported types: `TagData`, `TagMessage`, `NDEFRecord`, `TagCapabilities`,
`TagTarget`, `WriteRequest`, `WriteRecord`, `WriteResponse`, `LockResponse`,
`TransceiveRequest`, `DeviceStatus`, `NFCErrorCode`, `NFCErrorCodeValue`,
`NFCErrorEvent`, `NFCClientOptions`, `HealthCheckResponse`, `AgentDiagnosis`,
`RawTagPayload`, `WireMessage`. See `client/src/session/types.ts` for the full
definitions.

`NFCErrorCode` is the union of the codes this release knows, for a switch the
compiler can check. `NFCErrorCodeValue` is what `err.code` is declared as: that
union widened to any string, so a code added by a newer agent still type-checks.

---

# NFCDeviceClient (Device Input)

Use `NFCDeviceClient` to connect to the **agent** (port 9470) as an NFC device. This is a universal library that works in both Node.js and browser environments, allowing any NFC-capable device to act as a reader.

The library is **NFC-source agnostic** - integrate with any NFC library (WebNFC, React Native NFC Manager, etc.) by calling `scanTag()` when your NFC library detects a tag.

Pair the device first: read the agent's `davi-pair://` QR off the kiosk screen,
pin the TLS connection to its `spki`, and POST to
`https://<agent-host>:9470/pair?pin=<code>`. Pairing is served from the agent's
port, not the cleartext bootstrap listener, and is refused with
`426 Upgrade Required` over cleartext from anything but loopback. See
[Device setup](device-setup.md#1-pair). Present the `deviceToken` as `?secret=`
on the URL below, or as an `Authorization: Bearer` header; loopback needs one
too, unless the agent runs with `-allow-loopback-bypass`.

The device endpoint caps an inbound frame at 256 KB and drops the session of a
device that exceeds it. Revoking a device's credential closes its session with a
policy-violation close (1008). Both arrive as `disconnected`.

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
| `writeRequest` | `{ requestID, deviceID, ndefMessage, ndefBytes, tagUID, lock, idempotencyKey }` | Server requests a write operation. `lock` with no `ndefMessage`/`ndefBytes` is a lock-only request: lock the tag as it stands and write nothing |
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
