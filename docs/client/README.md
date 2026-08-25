# Client library

`@davi/nfc-agent-client` — reads tags from the agent and writes to them.
Source in [`client/`](../../client).

[Tutorial](tutorial.md) · [Protocol](../api.md) ·
[Device role](../javascript-client.md)

- [Install](#install)
- [Connecting](#connecting)
- [Writing](#writing)
- [Targeting](#targeting)
- [Capabilities](#capabilities)
- [Locking](#locking)
- [Raw exchange](#raw-exchange)
- [Errors](#errors)
- [Reconnection](#reconnection)
- [Diagnostics](#diagnostics)
- [React](#react)
- [NFCClient](#nfcclient) · [Types](#types) · [useNFCClient](#usenfcclient)

## Install

Bundler:

```ts
import { NFCClient } from "@davi/nfc-agent-client";
import { useNFCClient } from "@davi/nfc-agent-client/react";
```

Script tag — puts `NFCClient` and the helper functions on the page:

```html
<script src="nfc-client.js"></script>
```

| File | For |
|---|---|
| `client/dist/nfc-client.js` | `<script>` tag |
| `client/dist/nfc-client.esm.js` | `import` without a package manager |
| `client/dist/*.d.ts` | Types for either |

Rebuild with `make client` after changing `client/src`.

## Connecting

```ts
const client = new NFCClient("https://localhost:9470", {
  apiSecret: process.env.NFC_API_SECRET,
});
await client.connect();
```

`apiSecret` goes on the WebSocket URL. Loopback is exempt, so a page the agent
serves needs none.

A page served from anywhere else needs its origin allowed — a separate
requirement from the secret. See
[Connecting from a web console](../../README.md#connecting-from-a-web-console).

## Writing

A write replaces the whole NDEF message. `type` selects which other fields are
read; the [record table](../api.md#messages-to-server) lists every kind.

```ts
await client.write({
  records: [
    { type: "text", content: "Hello", language: "en" },
    { type: "uri", content: "https://example.com" },
    { type: "smartposter", content: "https://example.com", title: "Example" },
    { type: "mime", mimeType: "application/json", payload: btoa('{"a":1}') },
    { type: "external", content: "example.com:mytype", payload: btoa("data") },
    { type: "raw", tnf: 1, typeBytes: btoa("T"), payload: btoa("\x02enhi") },
  ],
});
```

Blank a tag with a single `empty` record — reversible, unlike locking:

```ts
await client.write({ records: [{ type: "empty" }] });
```

To append, read the tag's records, map them to write records and send the whole
set back. See the [append pattern](../api.md#append-pattern).

Undecoded bytes come back as base64:

```ts
import { decodeBase64 } from "@davi/nfc-agent-client";

for (const record of tag.message?.records ?? []) {
  const bytes = decodeBase64(record.payload);
}
```

## Targeting

Every operation names the tag it applies to. The client fills that in from the
tag it last saw, so an untargeted call is already aimed at the tag on the
reader.

```ts
await client.write({ records });                                  // the tag in the field
await client.write({ records, uid });                             // a named tag
await client.write({ records, uid, deviceID });                   // that tag, on that device
await client.write({ records, allowUntargeted: true });           // let the agent pick
```

Naming both `uid` and `deviceID` holds the device to that tag, so an id from an
earlier scan cannot act on whatever it holds now. See
[Naming the tag](../api.md#naming-the-tag).

## Capabilities

Off the scan:

```ts
client.on("tagData", (tag) => {
  writeButton.disabled = !tag.capabilities?.canWrite || tag.capabilities.isReadOnly;
});
```

From the tag, when the scan's answer is too old:

```ts
const caps = await client.getCapabilities();
if (caps.maxNdefSize && message.length > caps.maxNdefSize) throw new Error("too large");
```

Undefined means the agent did not say, not `false`.
[What each field means](../api.md#tag-capabilities).

## Locking

Irreversible: the tag keeps its contents and can never be written again.
See [Locking tags](../api.md#locking-tags-make-read-only).

```ts
await client.write({ records, lock: true });  // atomic where the tag supports both
await client.lock();                          // lock without writing
```

## Raw exchange

Takes and returns bytes.

```ts
// PC/SC get-UID pseudo-APDU
await client.transceive({ data: new Uint8Array([0xff, 0xca, 0x00, 0x00, 0x00]) });

// NTAG GET_VERSION, at the framing level
await client.transceive({ data: new Uint8Array([0x60]), raw: true });
```

## Errors

A refusal rejects with [`NFCRequestError`](#nfcrequesterror). Branch on
`retryable`, not on the message.

```ts
try {
  await client.write({ records });
} catch (err) {
  if (err.retryable) show("Present the tag again.");
  else show(`Cannot write this tag: ${err.message}`); // READ_ONLY, TAG_MISMATCH, …
}
```

Pass a stable `idempotencyKey` when a response can be lost mid-write. A paired
device must then report the outcome it already produced rather than write
again; it does nothing on the agent's own reader, where a lost response means
the socket is gone.

## Reconnection

The delay doubles from `reconnectDelay` to `maxReconnectDelay`, giving up after
`maxReconnectAttempts`. Set it to `0` for a page that watches one reader all
day.

```ts
new NFCClient(url, { maxReconnectAttempts: 0 });
```

## Diagnostics

A failed WebSocket carries no detail — a refused connection, an untrusted
certificate and a blocked origin all arrive as the same empty `error` event.
`diagnoseAgent` works it out over HTTP.

```ts
const why = await diagnoseAgent("https://localhost:9470");
show(why.title, why.detail);
if (why.openUrl) offerLink(why.openUrl);
```

| `kind` | Cause |
|---|---|
| `origin-blocked` | The agent answers over HTTP, so it is running and trusted. This page's origin is not allowed |
| `wrong-scheme` | The page is on `https`, the agent serves plain `http` |
| `unreachable` | The agent is not running, or its certificate is untrusted. Indistinguishable from here, so `openUrl` is set for the operator to open |

A working health check does not mean the WebSocket will connect.

## React

```tsx
const { connectionStatus, lastTag, write } = useNFCClient("https://localhost:9470");

if (connectionStatus !== "connected") return <p>Connecting to the reader…</p>;
if (!lastTag) return <p>Present a card.</p>;

return <button onClick={() => write({ records })}>Write to {lastTag.uid}</button>;
```

One client per component. `serverUrl` and `options` are read on mount —
remount to change them.

---

## NFCClient

```ts
new NFCClient(serverUrl: string, options?: NFCClientOptions)
```

`serverUrl` is the agent's base URL. A trailing slash is stripped; `http`
becomes `ws`, `https` becomes `wss`.

### Options

| Option | Type | Default | |
|---|---|---|---|
| `apiSecret` | `string` | `""` | Appended to the WebSocket URL. Loopback is exempt |
| `autoReconnect` | `boolean` | `true` | Reconnect after an unintended close |
| `reconnectDelay` | `number` | `250` | Milliseconds before the first retry. Doubles per attempt |
| `maxReconnectDelay` | `number` | `5000` | Ceiling for that doubling |
| `maxReconnectAttempts` | `number` | `10` | `0` retries forever |

### Methods

| Method | Returns | |
|---|---|---|
| `connect()` | `Promise<void>` | Resolves once open. Rejects `Connection timeout` after 10s, or `Connection failed` if the socket closed first, emitting `error` with `phase: "connection"` first. Clears the intentional-disconnect flag |
| `disconnect()` | `Promise<void>` | Closes, suppresses auto-reconnect, rejects requests in flight with `connection closed` |
| `isConnected()` | `boolean` | |
| `currentTag()` | `TagData \| null` | What an untargeted request names |
| `write(request)` | `Promise<WriteResponse>` | [`writeRequest`](../api.md#messages-to-server) |
| `lock(target?)` | `Promise<LockResponse>` | [`lockRequest`](../api.md#locking-tags-make-read-only) |
| `transceive(request)` | `Promise<Uint8Array>` | [`transceiveRequest`](../api.md#raw-exchange-transceive). Throws `transceive requires a command` without a round trip on empty `data` |
| `getCapabilities(target?)` | `Promise<TagCapabilities>` | [`capabilitiesRequest`](../api.md#tag-capabilities). `{}` if the agent answers with nothing |
| `healthCheck()` | `Promise<HealthCheckResponse>` | [`GET /api/v1/health`](../api.md#health-check). Needs no connection |

Requests time out after 30s. A refusal rejects with
[`NFCRequestError`](#nfcrequesterror).

### Events

`client.on(name, handler)` / `client.off(name, handler)`.

| Name | Payload | Emitted when |
|---|---|---|
| `connected` | `{}` | The socket opens |
| `disconnected` | `{}` | The socket closes, intentionally or not |
| `tagData` | `TagData` | A tag is scanned, by the local reader or a paired device |
| `tagRemoved` | `{ uid: string }` | The tag leaves. `uid` is the tag that went away |
| `deviceStatus` | `DeviceStatus` | The agent's own reader changes state |
| `error` | `NFCErrorEvent` | A transport failure, or an agent error not tied to a request |

An error tied to a request rejects that request instead of emitting `error`. A
throwing handler is caught and logged.

## Types

### TagData

```ts
{
  uid: string;
  type: string;
  technology: string;
  scannedAt: Date | null;
  text: string;
  message: TagMessage | null;
  ndefRecords?: NDEFRecord[];
  capabilities?: TagCapabilities;
  deviceID?: string;
  error: string | null;
  _raw: unknown;
}
```

`text` is the first text record's content — empty on a tag holding only a URI.
`ndefRecords` is set only when `message.type` is `"ndef"`. `deviceID` is the
paired device that scanned it, absent for the local reader.

### TagMessage, NDEFRecord

```ts
{ type: "ndef" | "raw"; records?: NDEFRecord[]; data?: string }

{ type: string; content?: string; language?: string; tnf: number;
  id?: string; payload: string }
```

`data` and `payload` are base64. `content` is the decoded text or URI.

### TagCapabilities

```ts
{
  canRead?, canWrite?, canTransceive?, canLock?, isReadOnly?: boolean;
  readsAreSnapshot?: boolean;
  memorySize?, maxNdefSize?: number;
  technology?, tagFamily?: string;
  supportsNdef?, supportsCrypto?, supportsAuthentication?: boolean;
  supportsPassword?: boolean;
}
```

All optional; undefined means the agent did not say.
[Field meanings](../api.md#tag-capabilities).

### TagTarget

```ts
{ uid?: string; deviceID?: string; allowUntargeted?: boolean }
```

Extended by `WriteRequest` and `TransceiveRequest`; taken as an argument by
`lock` and `getCapabilities`. Omit all three and the client fills in `uid`, and
`deviceID` when there is one, from `currentTag()`.

### WriteRequest, WriteRecord

```ts
{ records: WriteRecord[]; lock?: boolean; idempotencyKey?: string } & TagTarget

{ type: string; content?: string; language?: string; mimeType?: string;
  title?: string; payload?: string; tnf?: number; typeBytes?: string; id?: string }
```

`payload`, `typeBytes` and `id` are base64. Which fields each `type` reads is
the [record table](../api.md#messages-to-server). `NDEFRecordWrite` is a
deprecated alias for `WriteRecord`.

### WriteResponse, LockResponse

```ts
{ message: string; uid?: string; tagType?: string; bytesWritten?: number;
  verified?: boolean; attempts?: number; locked?: boolean }

{ message: string; uid?: string; tagType?: string; locked?: boolean }
```

[Write guarantees](../api.md#write-response).

### TransceiveRequest

```ts
{ data: Uint8Array; raw?: boolean } & TagTarget
```

`data` is base64-encoded by the client. `raw` exchanges at the framing level
rather than as an APDU; a framing-level response carries no ISO 7816 status
word, an APDU-level one ends in two status bytes.

### DeviceStatus

```ts
{ connected: boolean; deviceName?: string; message?: string; cardPresent?: boolean }
```

`cardPresent` describes the agent's **own** reader, and is false the whole time
a paired device holds a tag. Use `tagRemoved` to know a tag has gone.

### NFCErrorEvent

```ts
{ error: Error; code?: string; retryable?: boolean; op?: string; tagUID?: string;
  phase?: "connection" | "websocket" | "reconnection" }
```

`phase` is set for transport failures, absent for agent errors.
[Codes](../api.md#error-codes).

### AgentDiagnosis

```ts
{ kind: "reachable" | "origin-blocked" | "wrong-scheme" | "unreachable";
  title: string; detail: string; openUrl?: string }
```

`title` and `detail` are addressed to whoever is at the reader.

## NFCRequestError

Thrown by every refused operation. Extends `Error` with `code`, `retryable`,
`op` and `tagUID`; `name` is `"NFCRequestError"`.

## Functions

| Function | Returns | |
|---|---|---|
| `parseTagData(payload)` | `TagData` | Applied to every broadcast. Exported for a payload from elsewhere |
| `encodeBase64(bytes)` | `string` | |
| `decodeBase64(value)` | `Uint8Array` | |
| `diagnoseAgent(serverUrl)` | `Promise<AgentDiagnosis>` | One or two HTTP requests to loopback |

## useNFCClient

```ts
useNFCClient(serverUrl: string, options?: NFCClientOptions): UseNFCClientReturn
```

| Field | Type | |
|---|---|---|
| `connectionStatus` | `"disconnected" \| "connecting" \| "connected"` | |
| `lastTag` | `TagData \| null` | Cleared on `tagRemoved` |
| `capabilities` | `TagCapabilities \| null` | From the scan, replaced by `refreshCapabilities` |
| `error` | `NFCErrorEvent \| null` | |
| `diagnosis` | `AgentDiagnosis \| null` | Null while connected, and while the first attempt is in flight |
| `reconnect` | `() => Promise<void>` | Disconnects, clears the diagnosis, connects again |
| `clearLastTag` | `() => void` | |
| `write` | `(request) => Promise<WriteResponse>` | |
| `lock` | `(target?) => Promise<LockResponse>` | |
| `transceive` | `(request) => Promise<Uint8Array>` | |
| `refreshCapabilities` | `(target?) => Promise<TagCapabilities>` | Also updates `capabilities` |

The four operations throw `NFC reader is not connected` before the client
exists.
