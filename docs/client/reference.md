# Client library reference

The TypeScript surface of `@davi/nfc-agent-client`, built from `client/src`.

This describes the library. The protocol it speaks — message shapes, record
kinds, error codes, what the agent does with each request — is
[api.md](../api.md), and is not repeated here.

## Exports

| Export | Kind | From |
|---|---|---|
| `NFCClient` | class | `@davi/nfc-agent-client` |
| `NFCRequestError` | class | `@davi/nfc-agent-client` |
| `parseTagData` | function | `@davi/nfc-agent-client` |
| `encodeBase64`, `decodeBase64` | function | `@davi/nfc-agent-client` |
| `diagnoseAgent` | function | `@davi/nfc-agent-client` |
| `useNFCClient` | hook | `@davi/nfc-agent-client/react` |

The classic-script build (`client/dist/nfc-client.js`) assigns all of these to
the page as globals.

## NFCClient

```ts
new NFCClient(serverUrl: string, options?: NFCClientOptions)
```

`serverUrl` is the agent's base URL, e.g. `https://localhost:9470`. A trailing
slash is stripped; `http` becomes `ws` and `https` becomes `wss` for the socket.

### NFCClientOptions

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

Each mirrors a wire payload from [api.md](../api.md); the tables below give the
TypeScript shape and the fields whose meaning does not follow from the name.

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
rather than as an APDU.

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

Branch on `retryable` rather than on the message: true means the same request
could succeed if repeated, false means it was refused on its merits.

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
`openUrl` is present when two causes cannot be told apart from here — opening it
makes the browser answer.

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
