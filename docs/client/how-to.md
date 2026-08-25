# How-to guides

Each recipe assumes a connected `NFCClient`. See the [tutorial](tutorial.md) if
you don't have one, and the [reference](reference.md) for the full surface of
anything used here.

- [Install the library](#install-the-library)
- [Connect to an agent that requires a secret](#connect-to-an-agent-that-requires-a-secret)
- [Write a record that is not text or a URI](#write-a-record-that-is-not-text-or-a-uri)
- [Write to a tag other than the one in the field](#write-to-a-tag-other-than-the-one-in-the-field)
- [Check what a tag supports before offering a write](#check-what-a-tag-supports-before-offering-a-write)
- [Lock a tag permanently](#lock-a-tag-permanently)
- [Send a raw APDU](#send-a-raw-apdu)
- [Read a record's undecoded bytes](#read-a-records-undecoded-bytes)
- [Handle a refused operation](#handle-a-refused-operation)
- [Use the library from React](#use-the-library-from-react)
- [Tell an operator why the connection failed](#tell-an-operator-why-the-connection-failed)
- [Keep retrying for as long as the page is open](#keep-retrying-for-as-long-as-the-page-is-open)

## Install the library

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

## Connect to an agent that requires a secret

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

## Write a record that is not text or a URI

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

## Write to a tag other than the one in the field

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

## Check what a tag supports before offering a write

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

An undefined field means the agent did not say, which is not the same as
`false`. [What each capability means](../api.md#tag-capabilities).

## Lock a tag permanently

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

## Send a raw APDU

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

A framing-level response carries no ISO 7816 status word; an APDU-level one
ends in two status bytes. [What the agent does with the bytes](../api.md#raw-exchange-transceive).

## Read a record's undecoded bytes

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

## Handle a refused operation

A refusal rejects with an `NFCRequestError`. Branch on `retryable` rather than
on the message:

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

[The codes](../api.md#error-codes), and which of them are retryable.

If a response can be lost mid-write — a flaky link, a page that may reload —
pass a stable `idempotencyKey` and reuse it on the retry. A paired device must
then report the outcome it already produced rather than write again. It does
nothing on the agent's own hardware reader, where a lost response means the
socket is gone.

```ts
await client.write({ records, idempotencyKey: key });
```

## Use the library from React

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

## Tell an operator why the connection failed

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

## Keep retrying for as long as the page is open

The client gives up after ten attempts by default. For a page that watches one
reader all day, remove the limit:

```ts
const client = new NFCClient(url, { maxReconnectAttempts: 0 });
```

Adjust the pace with `reconnectDelay` and `maxReconnectDelay` — the delay
doubles from the first up to the second.
