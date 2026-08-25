# Client library

`@davi/nfc-agent-client` — for an application that consumes tags from the agent
and writes to them. Source in [`client/`](../../client).

New to it? [tutorial.md](tutorial.md) builds a page that reads and writes a tag,
end to end.

The protocol underneath — message shapes, record kinds, error codes, what the
agent does with each request — is [api.md](../api.md), and is not repeated here.
`NFCDeviceClient`, for a phone or Node process acting as a reader *for* the
agent, is a different library on the same port:
[javascript-client.md](../javascript-client.md).

- [Install](#install)
- [Recipes](#recipes)
- [NFCClient](#nfcclient)
- [Types](#types)
- [NFCRequestError](#nfcrequesterror)
- [Functions](#functions)
- [useNFCClient](#usenfcclient)

## Install

**With a bundler**, import the package by name:

```ts
import { NFCClient } from "@davi/nfc-agent-client";
import { useNFCClient } from "@davi/nfc-agent-client/react";
```

**Without one**, copy the built file and load it with a `<script>` tag. It puts
`NFCClient` and the helper functions on the page:

```bash
cp client/dist/nfc-client.js your-project/
```

```html
<script src="nfc-client.js"></script>
```

**As an ES module without a package manager**, copy `client/dist/nfc-client.esm.js`
instead and `import` from it. Declarations for both live beside them in
`client/dist`.

Regenerate all three with `make client` after changing `client/src`.

## Recipes

### Connect to an agent that requires a secret

```ts
const client = new NFCClient("https://localhost:9470", {
  apiSecret: process.env.NFC_API_SECRET,
});
await client.connect();
```

The secret goes on the WebSocket URL. Connections from loopback are exempt, so
a page served by the agent itself does not need one.

A page served from anywhere else also needs its origin allowed, which is a
separate requirement — see
[Connecting from a web console](../../README.md#connecting-from-a-web-console).

### Write a record that is not text or a URI

A write replaces the whole NDEF message, and `type` selects how the rest of the
record is read. The
[record table in api.md](../api.md#messages-to-server) lists every kind and the
fields it uses; the ones that need more than a `content` string:

```ts
await client.write({
  records: [
    { type: "smartposter", content: "https://example.com", title: "Example" },
    { type: "mime", mimeType: "application/json", payload: btoa('{"a":1}') },
    { type: "external", content: "example.com:mytype", payload: btoa("data") },
    { type: "raw", tnf: 1, typeBytes: btoa("T"), payload: btoa("\x02enhi") },
  ],
});
```

To blank a tag, write a single `empty` record — reversible, unlike locking:

```ts
await client.write({ records: [{ type: "empty" }] });
```

To add to what a tag already holds, read its records, map them to write records
and send the whole set back. The
[append pattern](../api.md#append-pattern) shows the shape.

### Write to a tag other than the one in the field

The client names the tag it last saw. Override that by naming one yourself:

```ts
await client.write({ records, uid: "04A2B33C4D5E80" });
```

Name the device holding it as well to hold that device to the UID, so an id
remembered from an earlier scan cannot act on whatever it is holding now:

```ts
await client.write({ records, uid: "04A2B33C4D5E80", deviceID: "phone-7" });
```

Or let the agent pick, if your caller genuinely cannot know:

```ts
await client.write({ records, allowUntargeted: true });
```

[What the agent does with each of these](../api.md#naming-the-tag).

### Check what a tag supports before offering a write

Read `capabilities` off the scan for the cheap answer:

```ts
client.on("tagData", (tag) => {
  const writable = tag.capabilities?.canWrite && !tag.capabilities.isReadOnly;
  writeButton.disabled = !writable;
});
```

Ask the tag directly when you need the answer now rather than as of the scan:

```ts
const caps = await client.getCapabilities();
if (caps.maxNdefSize && message.length > caps.maxNdefSize) {
  throw new Error("message will not fit");
}
```

### Lock a tag permanently

Locking is irreversible: the tag keeps its contents and can never be written
again. Confirm with the operator first, and read
[Locking Tags](../api.md#locking-tags-make-read-only) before shipping it.

Lock as part of a write. On tags that support both in one exchange this is
atomic, so a failure can't leave the data written and the lock not applied:

```ts
await client.write({ records, lock: true });
```

Or lock a tag without changing it:

```ts
await client.lock();
```

### Send a raw APDU

`transceive` takes and returns bytes:

```ts
// PC/SC get-UID pseudo-APDU
const response = await client.transceive({
  data: new Uint8Array([0xff, 0xca, 0x00, 0x00, 0x00]),
});
```

Set `raw: true` to exchange at the framing level instead of wrapping the bytes
as an APDU — an NTAG `GET_VERSION`, for instance:

```ts
const version = await client.transceive({
  data: new Uint8Array([0x60]),
  raw: true,
});
```

### Read a record's undecoded bytes

`content` is the decoded text or URI. For anything the agent cannot decode, or
when you want the exact bytes, `payload` carries them as base64:

```ts
import { decodeBase64 } from "@davi/nfc-agent-client";

client.on("tagData", (tag) => {
  for (const record of tag.message?.records ?? []) {
    const bytes = decodeBase64(record.payload);
  }
});
```

`encodeBase64` goes the other way, for a `payload` you are about to write.

### Handle a refused operation

A refusal rejects with an [`NFCRequestError`](#nfcrequesterror). Branch on
`retryable` rather than on the message:

```ts
try {
  await client.write({ records });
} catch (err) {
  if (err.retryable) {
    show("Present the tag again.");
  } else {
    show(`Cannot write this tag: ${err.message}`);
    report(err.code); // READ_ONLY, CAPACITY_EXCEEDED, TAG_MISMATCH, …
  }
}
```

If a response can be lost mid-write — a flaky link, a page that may reload —
pass a stable `idempotencyKey` and reuse it on the retry. A paired device must
then report the outcome it already produced rather than write again. It does
nothing on the agent's own hardware reader, where a lost response means the
socket is gone.

```ts
await client.write({ records, idempotencyKey: key });
```

### Use the library from React

```tsx
import { useNFCClient } from "@davi/nfc-agent-client/react";

function Encoder({ url }: { url: string }) {
  const { connectionStatus, lastTag, write } = useNFCClient(
    "https://localhost:9470",
  );

  if (connectionStatus !== "connected") return <p>Connecting to the reader…</p>;
  if (!lastTag) return <p>Present a card.</p>;

  return (
    <button onClick={() => write({ records: [{ type: "uri", content: url }] })}>
      Write to {lastTag.uid}
    </button>
  );
}
```

The hook owns one client for the lifetime of the component. `serverUrl` and
`options` are read on mount only, so remount to point it elsewhere.

### Tell an operator why the connection failed

Browsers don't tell a page why a WebSocket failed — a refused connection, an
untrusted certificate and a blocked origin all arrive as the same empty `error`
event. `diagnoseAgent` works it out over HTTP instead:

```ts
import { diagnoseAgent } from "@davi/nfc-agent-client";

try {
  await client.connect();
} catch {
  const why = await diagnoseAgent("https://localhost:9470");
  show(why.title, why.detail);
  if (why.openUrl) offerLink(why.openUrl);
}
```

| `kind` | Means |
|---|---|
| `origin-blocked` | The agent is running and trusted, so the socket was refused above the transport: this page's origin is not allowed |
| `wrong-scheme` | The page is pointed at `https` and the agent serves plain `http` |
| `unreachable` | Either the agent isn't running or its certificate is untrusted. These are indistinguishable from here, so `openUrl` is set — opening it makes the browser answer |

A working health check does not mean the WebSocket will connect; the two have
separate requirements.

The React hook runs this for you and exposes the result as `diagnosis`.

### Keep retrying for as long as the page is open

The client gives up after ten attempts by default. For a page that watches one
reader all day, remove the limit:

```ts
const client = new NFCClient(url, { maxReconnectAttempts: 0 });
```

Adjust the pace with `reconnectDelay` and `maxReconnectDelay` — the delay
doubles from the first up to the second.

## NFCClient

```ts
new NFCClient(serverUrl: string, options?: NFCClientOptions)
```

`serverUrl` is the agent's base URL, e.g. `https://localhost:9470`. A trailing
slash is stripped; `http` becomes `ws` and `https` becomes `wss` for the socket.

### Options

| Option | Type | Default | Description |
|---|---|---|---|
| `apiSecret` | `string` | `""` | Appended to the WebSocket URL. Loopback connections are exempt |
| `autoReconnect` | `boolean` | `true` | Reconnect after an unintended close |
| `reconnectDelay` | `number` | `250` | Milliseconds before the first retry. Doubles per attempt |
| `maxReconnectDelay` | `number` | `5000` | Ceiling for that doubling |
| `maxReconnectAttempts` | `number` | `10` | Attempts before giving up. `0` retries forever |

### Methods

| Method | Returns | Notes |
|---|---|---|
| `connect()` | `Promise<void>` | Resolves once the socket is open. Rejects `Connection timeout` after 10s, or `Connection failed` if the socket closed first, emitting `error` with `phase: "connection"` first. Clears the intentional-disconnect flag |
| `disconnect()` | `Promise<void>` | Closes the socket, suppresses auto-reconnect, rejects every request in flight with `connection closed` |
| `isConnected()` | `boolean` | |
| `currentTag()` | `TagData \| null` | The tag the agent last reported. What the client names on an untargeted request |
| `write(request)` | `Promise<WriteResponse>` | [`writeRequest`](../api.md#messages-to-server) |
| `lock(target?)` | `Promise<LockResponse>` | [`lockRequest`](../api.md#locking-tags-make-read-only) |
| `transceive(request)` | `Promise<Uint8Array>` | [`transceiveRequest`](../api.md#raw-exchange-transceive). Throws `transceive requires a command` without a round trip if `data` is empty |
| `getCapabilities(target?)` | `Promise<TagCapabilities>` | [`capabilitiesRequest`](../api.md#tag-capabilities). `{}` if the agent answers with nothing |
| `healthCheck()` | `Promise<HealthCheckResponse>` | [`GET /api/v1/health`](../api.md#health-check). The only method that needs no connection |

Each request times out after 30s. A refusal rejects with
[`NFCRequestError`](#nfcrequesterror).

### Events

```ts
client.on(name, handler);
client.off(name, handler);
```

| Name | Payload | Emitted when |
|---|---|---|
| `connected` | `{}` | The socket opens |
| `disconnected` | `{}` | The socket closes, intentionally or not |
| `tagData` | `TagData` | A tag is scanned, by the local reader or a paired device |
| `tagRemoved` | `{ uid: string }` | The tag leaves the field. `uid` is the tag that went away |
| `deviceStatus` | `DeviceStatus` | The agent's own reader changes state |
| `error` | `NFCErrorEvent` | A transport failure, or an agent error not tied to a request |

An error tied to a request rejects that request's promise instead of emitting
`error`. A handler that throws is caught and logged; it does not stop the others.

## Types

Each mirrors a wire payload from [api.md](../api.md); the notes below cover the
fields whose meaning does not follow from the name.

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

- `text` — the first text record's content. Empty when the tag holds none,
  including a tag holding only a URI.
- `ndefRecords` — set only when `message.type` is `"ndef"`.
- `deviceID` — the paired device that scanned it; absent for the local reader.
- `_raw` — the payload as it arrived.

### TagMessage, NDEFRecord

```ts
{ type: "ndef" | "raw"; records?: NDEFRecord[]; data?: string }

{
  type: string;
  content?: string;
  language?: string;
  tnf: number;
  id?: string;
  payload: string;
}
```

`data` and `payload` are base64 — `decodeBase64` gives the bytes. `content` is
the decoded text or URI for every kind the agent can decode.

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

Every field is optional, and undefined means the agent did not say — which is
not the same as `false`. [What each one means](../api.md#tag-capabilities).

### TagTarget

Which tag an operation applies to. `WriteRequest` and `TransceiveRequest` extend
it; `lock` and `getCapabilities` take one as their argument.

```ts
{ uid?: string; deviceID?: string; allowUntargeted?: boolean }
```

Omit all three and the client fills in `uid` — and `deviceID`, when there is one
— from `currentTag()`. [Why requests name a tag](../api.md#naming-the-tag).

### WriteRequest, WriteRecord

```ts
{
  records: WriteRecord[];
  lock?: boolean;
  idempotencyKey?: string;
} & TagTarget
```

`WriteRecord` is the wire record verbatim — `type`, `content`, `language`,
`mimeType`, `title`, `payload`, `tnf`, `typeBytes`, `id`, with the last three
plus `payload` in base64. Which fields each `type` reads is the
[record table in api.md](../api.md#messages-to-server).

`NDEFRecordWrite` is an alias kept from when only `text` and `uri` were
reachable.

### WriteResponse, LockResponse

```ts
{
  message: string;
  uid?: string;
  tagType?: string;
  bytesWritten?: number;
  verified?: boolean;
  attempts?: number;
  locked?: boolean;
}

{ message: string; uid?: string; tagType?: string; locked?: boolean }
```

[What the agent guarantees about a successful write](../api.md#write-response).

### TransceiveRequest

```ts
{ data: Uint8Array; raw?: boolean } & TagTarget
```

`data` is base64-encoded by the client. `raw` exchanges at the framing level
rather than as an APDU, and a framing-level response carries no ISO 7816 status
word — an APDU-level one ends in two status bytes.

### DeviceStatus

```ts
{ connected: boolean; deviceName?: string; message?: string; cardPresent?: boolean }
```

`cardPresent` describes the agent's **own** reader only, and is false the whole
time a paired device is holding a tag. Use `tagRemoved` to know a tag has gone.

### NFCErrorEvent

```ts
{
  error: Error;
  code?: string;
  retryable?: boolean;
  op?: string;
  tagUID?: string;
  phase?: "connection" | "websocket" | "reconnection";
}
```

`phase` is set for transport failures and absent for agent errors. `code` and
`retryable` are the agent's; [the codes](../api.md#error-codes).

## NFCRequestError

Thrown by every operation the agent refuses. Extends `Error` with `code`,
`retryable`, `op` and `tagUID`, carrying the same values as `NFCErrorEvent`.
`name` is `"NFCRequestError"`.

`retryable` true means the same request could succeed if repeated; false means
it was refused on its merits.

## Functions

| Function | Returns | Description |
|---|---|---|
| `parseTagData(payload)` | `TagData` | Turns a raw `tagData` payload into a `TagData`. Called for you on every broadcast; exported for a payload from elsewhere |
| `encodeBase64(bytes)` | `string` | |
| `decodeBase64(value)` | `Uint8Array` | |
| `diagnoseAgent(serverUrl)` | `Promise<AgentDiagnosis>` | Probes the agent over HTTP to work out why a connection failed. One or two requests to loopback |

### AgentDiagnosis

```ts
{
  kind: "reachable" | "origin-blocked" | "wrong-scheme" | "unreachable";
  title: string;
  detail: string;
  openUrl?: string;
}
```

`title` and `detail` are addressed to whoever is standing at the reader.
`openUrl` is present when two causes cannot be told apart from here.

## useNFCClient

```ts
useNFCClient(serverUrl: string, options?: NFCClientOptions): UseNFCClientReturn
```

Owns one `NFCClient` for the lifetime of the component and connects on mount.
`serverUrl` and `options` are read on mount only — remount to change them.

| Field | Type | Description |
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

The four operations throw `NFC reader is not connected` if no client exists yet.
