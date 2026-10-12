# JavaScript client reference

Two libraries in `client/`, one per side of the agent:

| Library | Role | Speaks |
|---------|------|--------|
| `NFCClient` | An application reading scans and acting on tags | [Client API](client-api.md) |
| `NFCDeviceClient` | A phone, browser or Node.js process acting as a reader | [Device API](device-api.md) |

Neither is published to npm. Both are in [`client/`](../client) of the agent's
repository.

## NFCClient

### Getting it

`client/dist` holds built copies, committed:

| File | Use |
|------|-----|
| `nfc-client.esm.js` | An ES module: `import { NFCClient } from "./nfc-client.esm.js"` |
| `nfc-client.js` | A classic script. Its exports become globals: `new NFCClient(…)` |
| `index.d.ts`, `session/*.d.ts` | Type declarations |

With a bundler that compiles TypeScript, such as Vite, add the package from a
checkout instead. Its entry points are the TypeScript sources:

```bash
npm install ../davi-nfc-agent/client
```

```ts
import { NFCClient } from "@davi/nfc-agent-client";
import { useNFCClient } from "@davi/nfc-agent-client/react";
```

Connecting from a browser also needs the page's origin allowed and the agent's
certificate trusted. See [Connect from a browser](browser-access.md).

### Example

```javascript
import { NFCClient, NFCRequestError } from "./nfc-client.esm.js";

const client = new NFCClient("https://localhost:9470", { apiSecret: API_SECRET });

client.on("tagData", async (tag) => {
  console.log(tag.uid, tag.type, tag.text);
  if (!tag.capabilities?.canWrite) return;

  try {
    const result = await client.write({ records: [{ type: "uri", content: "https://example.com" }] });
    console.log("wrote", result.bytesWritten, "bytes");
  } catch (err) {
    if (err instanceof NFCRequestError && err.retryable) {
      console.log("Present the tag again");
    } else {
      throw err;
    }
  }
});

client.on("tagRemoved", ({ uid }) => console.log("removed", uid));

await client.connect();
```

### Constructor

```javascript
new NFCClient(serverUrl, options)
```

| Parameter | Default | Description |
|-----------|---------|-------------|
| `serverUrl` | | The agent's base URL, `https://localhost:9470`. `http` becomes `ws` and `/ws` is appended |
| `options.apiSecret` | | The API secret, sent as `?secret=`. Required unless the agent runs with `-allow-loopback-bypass` |
| `options.autoReconnect` | `true` | Reconnect after the connection drops, unless `disconnect()` was called |
| `options.reconnectDelay` | `250` | Milliseconds before the first retry, doubling per attempt |
| `options.maxReconnectDelay` | `5000` | The ceiling for that doubling |
| `options.maxReconnectAttempts` | `10` | Attempts before giving up. `0` retries forever |

### Connection

| Method | Description |
|--------|-------------|
| `connect()` | Opens the connection. Rejects after 10 seconds, or when the upgrade is refused |
| `disconnect()` | Closes it, and rejects requests still waiting |
| `isConnected()` | Whether the connection is open |
| `healthCheck()` | `GET /api/v1/health`, returning `{ status, type, timestamp, clients }` |

### Tag operations

Each takes the target fields `uid`, `deviceID` and `allowUntargeted` of
[Naming the tag](client-api.md#naming-the-tag). With none of them, the client
names the tag the agent last reported, so the tag in front of the operator is
the one acted on. After that tag leaves, a request with no target is sent
naming nothing and the agent refuses it with `TAG_NOT_NAMED`.

| Method | Returns | Sends |
|--------|---------|-------|
| `write({ records, lock?, idempotencyKey?, …target })` | `{ message, uid, tagType, bytesWritten, verified, attempts, locked }` | [`writeRequest`](client-api.md#writerequest) |
| `lock(target?)` | `{ uid, tagType, locked }` | [`lockRequest`](client-api.md#lockrequest) |
| `getCapabilities(target?)` | capabilities | [`capabilitiesRequest`](client-api.md#capabilitiesrequest) |
| `transceive({ data, raw?, autoGetResponse?, sessionId?, …target })` | `Uint8Array`, the whole reply | [`transceiveRequest`](client-api.md#transceiverequest) |
| `transceiveSequence({ steps, sessionId?, …target })` | `{ results: [{ data, sw?, body? }], stoppedAt }` | [`transceiveSequenceRequest`](client-api.md#transceivesequencerequest) |
| `beginRawSession(target?, ttlMs?)` | `{ sessionId, expiresInMs }` | [`rawSessionBeginRequest`](client-api.md#raw-sessions) |
| `endRawSession(sessionId)` | | `rawSessionEndRequest` |
| `ntag424({ op, …arguments, …target })` | the `ntag424Response` payload | [`ntag424Request`](client-api.md#ntag424request) |
| `currentTag()` | The tag the agent last reported, or `null` | |

Bytes are `Uint8Array` in both directions; the library does the base64. A
request not answered within 30 seconds rejects.

`getCapabilities()` reads a `capabilities` field from the answer, which the
agent does not send: the capabilities are the answer's payload itself, so it
currently resolves to `{}`. Read `tag.capabilities` from `tagData`, or send
`capabilitiesRequest` over the protocol, until that is fixed.

### Events

`client.on(event, handler)` adds a handler and `client.off(event, handler)`
removes it.

| Event | Payload | When |
|-------|---------|------|
| `tagData` | `TagData` | A tag was read |
| `tagRemoved` | `{ uid }` | The tag the client last reported left the reader or device that read it |
| `deviceStatus` | `{ connected, device?, message?, cardPresent? }` | The agent's own reader changed state |
| `connected` | `{}` | The connection opened |
| `disconnected` | `{}` | The connection closed |
| `error` | `{ error, code?, retryable?, phase? }` | The connection failed, or the agent sent an error that answered no request |

`TagData` is the [`tagData` payload](client-api.md#tagdata) with three
differences: `scannedAt` is a `Date`, `err` is named `error`, and the original
payload is kept in `_raw`.

`tagRemoved` fires and `currentTag()` returns `null` when the agent reports a
[removal](client-api.md#removal-and-failed-reads) about the current tag: one
naming the tag's `deviceID`, or for a tag the agent's own reader read, one
naming no device or a `deviceStatus` with `cardPresent: false`. A removal
naming another device leaves the current tag in place.

### Errors

A refused request rejects with an `NFCRequestError`:

| Property | Description |
|----------|-------------|
| `message` | The agent's message |
| `code` | The [error code](errors.md) |
| `retryable` | Whether the same request could succeed if sent again |
| `op`, `tagUID` | When the agent sent them |

A request that fails before reaching the agent, for not being connected or
timing out, rejects with a plain `Error`.

A connection runs one request at a time and queues a bounded number. Past that
the agent answers `BUSY`, so `await` operations rather than starting many at
once.

### diagnoseAgent

```javascript
import { diagnoseAgent } from "./nfc-client.esm.js";

const why = await diagnoseAgent("https://localhost:9470");
// { kind, title, detail, openUrl? }
```

Probes the agent over HTTP after a connection fails, since a failed WebSocket
carries no reason. `kind` is `origin-blocked`, `wrong-scheme` or `unreachable`;
what each means is in
[Connect from a browser](browser-access.md#4-connect).

`encodeBase64(bytes)` and `decodeBase64(text)` are exported for the byte fields
of the wire.

### React

```tsx
import { useNFCClient } from "@davi/nfc-agent-client/react";

function Reader({ apiSecret }: { apiSecret: string }) {
  const { connectionStatus, lastTag, diagnosis, write } = useNFCClient("https://localhost:9470", { apiSecret });

  if (connectionStatus !== "connected") return <p>{diagnosis?.title ?? "Connecting…"}</p>;
  if (!lastTag) return <p>Present a card.</p>;

  return <button onClick={() => write({ records: [{ type: "text", content: "Hi" }] })}>Write to {lastTag.uid}</button>;
}
```

The hook owns one client for the component's lifetime and returns
`connectionStatus` (`disconnected`, `connecting`, `connected`), `lastTag`,
`capabilities`, `error`, `diagnosis` (run when a connection attempt fails),
`reconnect`, `clearLastTag`, `write`, `lock`, `transceive` and
`refreshCapabilities`. The URL and options are read on mount, so remount to
change them.

### TypeScript

The package exports its types, among them `TagData`, `TagMessage`,
`NDEFRecord`, `TagCapabilities`, `TagTarget`, `WriteRequest`, `WriteRecord`,
`WriteResponse`, `LockResponse`, `TransceiveRequest`,
`TransceiveSequenceRequest`, `NTAG424Request`, `NTAG424Response`, `RawSession`,
`DeviceStatus`, `NFCClientOptions`, `NFCErrorEvent`, `NFCErrorCode` and
`AgentDiagnosis`.

`NFCErrorCode` is the union of codes this version knows. `err.code` is typed
`NFCErrorCodeValue`, that union widened to any string, so a code from a newer
agent still type-checks.

## NFCDeviceClient

`client/nfc-device-client.js` connects to the agent as a reader. It does the
registration, heartbeats and reconnection; your code reads tags with whatever
NFC stack it has, reports them, and answers the agent's requests.

It is one plain JavaScript file: `require()` it in Node.js, or load it with a
`<script>` tag, which defines `window.NFCDeviceClient`. Declarations are in
`client/nfc-device-client.d.ts`.

Pair the device first, as in [Pair a phone](device-setup.md#1-pair), and keep
the `deviceToken` and `publicKeyPin`.

### Node.js

In Node.js, pass a WebSocket class built on the `ws` package. A subclass
presents the token as a header and verifies the agent's key pin before
anything is sent:

```javascript
const fs = require("node:fs");
const tls = require("node:tls");
const { createHash, X509Certificate } = require("node:crypto");
const WebSocket = require("ws");
const NFCDeviceClient = require("./nfc-device-client");

const { deviceToken, publicKeyPin } = JSON.parse(fs.readFileSync("pairing.json", "utf8"));

function pinnedConnection(expectedPin) {
  return (options) => {
    const socket = tls.connect({ ...options, rejectUnauthorized: false });
    socket.once("secureConnect", () => {
      const spki = new X509Certificate(socket.getPeerCertificate().raw).publicKey.export({ type: "spki", format: "der" });
      const pin = "sha256/" + createHash("sha256").update(spki).digest("base64");
      if (pin !== expectedPin) socket.destroy(new Error("agent key pin mismatch"));
    });
    return socket;
  };
}

class PinnedWebSocket extends WebSocket {
  constructor(url, protocols) {
    super(url, protocols, {
      createConnection: pinnedConnection(publicKeyPin),
      headers: { Authorization: `Bearer ${deviceToken}` },
    });
  }
}

const device = new NFCDeviceClient("wss://AGENT_HOST:9470", {
  WebSocket: PinnedWebSocket,
  deviceName: "Bench reader",
  platform: "node",
  canWrite: true,
});

device.on("registered", ({ deviceID }) => console.log("Registered as", deviceID));
device.on("error", ({ error, phase }) => console.error(phase, error.message));

device.connect().then(() => {
  // When your reader sees a tag:
  // device.scanTag({ uid, technology: "ISO14443A", type: "NTAG215", ndefMessage });
});
```

`pairing.json` here is the response the agent gave at pairing, saved.

### Browsers

A browser cannot set headers on a WebSocket, so pass the token as `secret`. The
library sends it as `?secret=` on the connection URL:

```javascript
const device = new NFCDeviceClient("wss://AGENT_HOST:9470", {
  secret: deviceToken,
  deviceName: "Chrome on Android",
  platform: "web",
  nfcType: "webnfc",
  canWrite: true,
});
```

The browser must trust the agent's certificate, as in
[Connect from a browser](browser-access.md#2-make-the-browser-trust-the-agents-certificate),
and a page cannot check the key pin.

### Constructor

```javascript
new NFCDeviceClient(serverUrl, options)
```

| Option | Default | Description |
|--------|---------|-------------|
| `WebSocket` | the global | The WebSocket class. Required in Node.js |
| `deviceName` | `NFC Device` | Shown in the tray and the Control Center |
| `platform` | `unknown` | `ios`, `android`, `web`, `node`, … |
| `appVersion` | `1.0.0` | |
| `canRead` | `true` | |
| `canWrite` | `false` | Answers `writeRequest` |
| `canLock` | `false` | Answers a `writeRequest` with `lock` |
| `canTransceive` | `false` | Answers `transceiveRequest` |
| `canTransceiveRaw` | `false` | Answers `transceiveRequest` with `raw` |
| `canTransceiveSequence` | `false` | Answers `transceiveSequenceRequest` |
| `nfcType` | `custom` | The NFC stack |
| `deviceType` | | See [Device capabilities](device-api.md#device-capabilities) |
| `supportedTagTypes` | | |
| `maxBaudRate` | | |
| `autoHeartbeat` | `true` | Send a heartbeat every `heartbeatInterval` |
| `heartbeatInterval` | `30000` | Milliseconds |
| `autoReconnect` | `true` | Reconnect after a drop, up to 10 times |
| `reconnectDelay` | `3000` | Milliseconds between attempts |
| `secret` | | The device token or API secret, sent as `?secret=`. Replaces one already in `serverUrl` |

`serverUrl` may use `http(s)` or `ws(s)`. `/ws` is appended to its path unless
the path already ends in it, and `mode=device` is added to its query, keeping
any parameters already there. The capabilities beyond `canRead`,
`canWrite` and `nfcType` are sent only to an agent that speaks protocol version
1.

### Methods

| Method | Description |
|--------|-------------|
| `connect()` | Connects and registers. Rejects after 10 seconds |
| `disconnect(reason?)` | Sends `goodbye` with `reason`, then closes |
| `scanTag(tag)` | Reports a tag: `{ uid, technology, type?, ndefMessage?, capabilities?, atr?, rawData?, scannedAt? }`. `technology` defaults to `ISO14443A` and `type` to `Unknown` |
| `removeTag(uid)` | Reports that the tag left |
| `respondToWrite(requestID, success, error?, errorCode?)` | Answers `writeRequest` |
| `respondToTransceive(requestID, success, data?, error?, errorCode?)` | Answers `transceiveRequest`. `data` is base64 and ends with the status word |
| `respondToTransceiveSequence(requestID, success, replies?, stoppedAt?, error?, errorCode?)` | Answers `transceiveSequenceRequest` |
| `getDeviceID()`, `getServerInfo()` | From registration |
| `isConnected()` | Connected and registered |

Records in `scanTag`'s `ndefMessage` name their type in `recordType`:

```javascript
await device.scanTag({
  uid: "04A1B2C3D4E5F6",
  technology: "ISO14443A",
  type: "NTAG215",
  ndefMessage: { records: [{ recordType: "text", content: "Hello", language: "en" }] },
});
```

See [tagScanned](device-api.md#tagscanned) for every field, and for the
`capabilities` rules.

### Events

| Event | Payload |
|-------|---------|
| `registered` | `{ deviceID, serverInfo, protocolVersion }` |
| `writeRequest` | `{ requestID, deviceID, tagUID, ndefBytes, ndefMessage, lock, idempotencyKey }` |
| `transceiveRequest` | `{ requestID, deviceID, tagUID, data, raw, timeoutMs }` |
| `transceiveSequenceRequest` | `{ requestID, deviceID, tagUID, steps, timeoutMs }` |
| `connected` | `{ protocolVersion }` |
| `disconnected` | `{}` |
| `error` | `{ error, phase }` for a connection failure; `{ error, code, retryable }` for an error frame from the agent |

`ndefMessage` is always `null`; the message is in `ndefBytes`. A
`writeRequest` with `lock: true` and no `ndefBytes` asks for a lock alone.

### WebNFC

Web NFC writes records, not bytes, so a WebNFC device decodes `ndefBytes`
first. This decoder handles text, URI, MIME, external and empty records, which
covers what clients write except smart posters and hand-built records:

```javascript
const URI_PREFIXES = ["", "http://www.", "https://www.", "http://", "https://", "tel:", "mailto:",
  "ftp://anonymous:anonymous@", "ftp://ftp.", "ftps://", "sftp://", "smb://", "nfs://", "ftp://",
  "dav://", "news:", "telnet://", "imap:", "rtsp://", "urn:", "pop:", "sip:", "sips:", "tftp:",
  "btspp://", "btl2cap://", "btgoep://", "tcpobex://", "irdaobex://", "file://", "urn:epc:id:",
  "urn:epc:tag:", "urn:epc:pat:", "urn:epc:raw:", "urn:epc:", "urn:nfc:"];

function ndefBytesToWebNFC(base64) {
  const bytes = Uint8Array.from(atob(base64), (c) => c.charCodeAt(0));
  const records = [];
  let i = 0;
  while (i < bytes.length) {
    const header = bytes[i++];
    const tnf = header & 0x07;
    const shortRecord = header & 0x10;
    const hasId = header & 0x08;
    const typeLength = bytes[i++];
    let payloadLength;
    if (shortRecord) {
      payloadLength = bytes[i++];
    } else {
      payloadLength = ((bytes[i] << 24) | (bytes[i + 1] << 16) | (bytes[i + 2] << 8) | bytes[i + 3]) >>> 0;
      i += 4;
    }
    const idLength = hasId ? bytes[i++] : 0;
    const type = new TextDecoder().decode(bytes.subarray(i, (i += typeLength)));
    const id = new TextDecoder().decode(bytes.subarray(i, (i += idLength)));
    const payload = bytes.subarray(i, (i += payloadLength));
    const record = id ? { id } : {};

    if (tnf === 0) {
      records.push({ ...record, recordType: "empty" });
    } else if (tnf === 1 && type === "T") {
      const langLength = payload[0] & 0x3f;
      const encoding = payload[0] & 0x80 ? "utf-16" : "utf-8";
      records.push({
        ...record,
        recordType: "text",
        lang: new TextDecoder().decode(payload.subarray(1, 1 + langLength)),
        data: new TextDecoder(encoding).decode(payload.subarray(1 + langLength)),
      });
    } else if (tnf === 1 && type === "U") {
      const prefix = URI_PREFIXES[payload[0]] ?? "";
      records.push({ ...record, recordType: "url", data: prefix + new TextDecoder().decode(payload.subarray(1)) });
    } else if (tnf === 2) {
      records.push({ ...record, recordType: "mime", mediaType: type, data: payload });
    } else if (tnf === 4) {
      records.push({ ...record, recordType: type, data: payload });
    } else {
      throw new Error(`record type ${tnf}/${type} is not supported by this decoder`);
    }
  }
  return records;
}
```

Reading and writing with it, on the `device` from [Browsers](#browsers):

```javascript
const reader = new NDEFReader();

reader.onreading = ({ serialNumber, message }) => {
  const records = message.records
    .filter((r) => r.recordType === "text" || r.recordType === "url")
    .map((r) => ({
      recordType: r.recordType === "url" ? "uri" : "text",
      content: new TextDecoder(r.encoding || "utf-8").decode(r.data),
      ...(r.lang ? { language: r.lang } : {}),
    }));

  device.scanTag({ uid: serialNumber, technology: "ISO14443A", type: "NDEF", ndefMessage: { records } });
};

device.on("writeRequest", async ({ requestID, ndefBytes, lock }) => {
  try {
    if (!ndefBytes || lock) {
      device.respondToWrite(requestID, false, "locking is not supported over Web NFC", "NOT_SUPPORTED");
      return;
    }
    await reader.write({ records: ndefBytesToWebNFC(ndefBytes) });
    device.respondToWrite(requestID, true);
  } catch (err) {
    device.respondToWrite(requestID, false, err.message, "WRITE_FAILED");
  }
});

await device.connect();
document.querySelector("#scan").addEventListener("click", () => reader.scan());
```

Web NFC is available in Chrome on Android. Call `scan()` from a user gesture,
such as the button click above, since Chrome asks for the NFC permission then.

### Finding the agent

The agent advertises `_nfc-device._tcp` over mDNS (see
[Discovery](device-api.md#discovery)). In Node.js, with
[bonjour-service](https://github.com/onlxltd/bonjour-service):

```javascript
const { Bonjour } = require("bonjour-service");

const bonjour = new Bonjour();
const browser = bonjour.find({ type: "nfc-device" }, (service) => {
  console.log(`${service.name}: wss://${service.addresses[0]}:${service.port}`);
});

setTimeout(() => {
  browser.stop();
  bonjour.destroy();
}, 5000);
```
