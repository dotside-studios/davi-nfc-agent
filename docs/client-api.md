# Client API reference

The WebSocket protocol an application speaks to read scans and act on tags. For
a first connection end to end, see [Getting started](getting-started.md). For
the typed wrapper over this protocol, see
[JavaScript client](javascript-client.md).

## Connecting

```
wss://<host>:9470/ws?secret=<api-secret>
```

| | |
|---|---|
| Port | 9470, set with `-device-port`. Devices use the same port with `?mode=device`, see [Device API](device-api.md) |
| Scheme | `wss://`. The agent serves TLS by default and refuses cleartext on this port. `ws://` works only when the agent runs with `-auto-tls=false` and no `-cert` |
| Credential | The API secret as `?secret=` or `Authorization: Bearer <secret>`. Required from every host, this one included, unless the agent runs with `-allow-loopback-bypass`. See [Configuration](configuration.md#files) for where the secret is |
| Origin | A browser's `Origin` must be on the agent's allowlist. A connection that sends no `Origin`, such as a Node.js script, is not checked. See [Connect from a browser](browser-access.md) |

The upgrade fails with HTTP `401` for a missing or wrong credential and `403`
for an origin that is not allowed. Neither is a WebSocket close: the
connection never opens.

Any number of clients can be connected at once. Every client receives every
broadcast, and each request is answered on the connection that sent it.

## Message envelope

A client sends:

```json
{ "id": "req_1", "type": "writeRequest", "payload": { } }
```

`id` is optional and echoed on the answer, so a client can match the two.

A successful answer has `type` set to the request's response type and
`success: true`:

```json
{ "id": "req_1", "type": "writeResponse", "success": true, "payload": { } }
```

A refused request is answered with `type: "error"`, whatever the request was.
See [Errors](errors.md):

```json
{
  "id": "req_1",
  "type": "error",
  "success": false,
  "error": "no reader or device is holding tag 04A1B2C3D4E5F6",
  "payload": { "code": "NO_CARD", "retryable": true }
}
```

A message type the agent does not know is answered with `UNKNOWN_TYPE`.

## Messages from the agent

### tagData

Sent to every client when a tag is read:

```json
{
  "type": "tagData",
  "payload": {
    "uid": "04A1B2C3D4E5F6",
    "type": "NTAG215",
    "technology": "ISO14443A",
    "scannedAt": "2026-03-14T09:26:53Z",
    "capabilities": {
      "canRead": true,
      "canWrite": true,
      "canLock": true,
      "maxNdefSize": 496,
      "tagFamily": "NTAG",
      "supportsNdef": true
    },
    "message": {
      "type": "ndef",
      "records": [
        {
          "tnf": 1,
          "type": "text",
          "content": "Hello, NFC!",
          "language": "en",
          "payload": "AmVuSGVsbG8sIE5GQyE="
        }
      ]
    },
    "text": "Hello, NFC!",
    "err": null
  }
}
```

| Field | Description |
|-------|-------------|
| `uid` | The tag's identifier. Echo it unchanged when naming the tag in a request: a tag on the agent's reader reports bare hex (`04A1B2C3D4E5F6`), a tag a phone holds reports colon-separated hex (`04:A1:B2:C3:D4:E5:F6`), and a QR or barcode reports the decoded value |
| `type` | `MIFARE Classic 1K`, `MIFARE Classic 4K`, `DESFire`, `MIFARE Ultralight`, `NTAG213`, `NTAG215`, `NTAG216`, `NTAG424`, `Type4`, `FeliCa`. Whatever the device reported for a tag a device holds |
| `technology` | `ISO14443A`, `ISO14443B`, `ISO18092` (FeliCa), or whatever the device reported |
| `scannedAt` | RFC 3339 timestamp |
| `capabilities` | What can be done to this tag now. See [Tag capabilities](#tag-capabilities) |
| `deviceID` | The paired device that scanned the tag. Absent when the agent's own reader read it |
| `message` | What the tag holds. Absent when it holds no NDEF message, see [Identity-only scans](#identity-only-scans) |
| `text` | The first text record, or `""` |
| `err` | Why reading the tag failed, or `null` |

`message.type` is `ndef`, with `records`, or `raw`, with the undecoded contents
base64 in `data`. Each record has:

| Field | Description |
|-------|-------------|
| `tnf` | Type Name Format, 0 to 7. `1` is Well Known |
| `type` | The record type, human-readable: `text`, `uri`, `mime`, `smartposter`, `aar`, `external`, … |
| `content` | The decoded value: the text of a text record, the URI of a URI record. Absent when nothing decodes |
| `language` | Text records only |
| `id` | The record ID, when it has one |
| `payload` | The record's payload bytes as stored on the tag, base64. A text record's payload starts with a status byte and the language code, so it does not decode to `content` |

The [write request](#writerequest) takes the same field names, so a record read
from one tag can be written to another.

#### Removal and failed reads

A `tagData` whose `uid` is `""` means no tag is in the field. It also carries a
read that failed before any tag was identified, with the reason in `err`:

```json
{ "type": "tagData", "payload": { "uid": "", "text": "", "err": null } }
```

This is sent for the agent's own reader. When a phone's tag leaves its field,
clients are not sent a message; a request for that tag fails with `NO_CARD`.

#### Identity-only scans

A readable tag holding no NDEF message scans with `err: null`, no `message`,
`text: ""` and `capabilities.supportsNdef` false. This covers a DESFire whose
NDEF file needs keys the agent does not hold, a MIFARE Classic with keys the
agent does not hold, and every FeliCa, whose identifier is read and whose
command set is not implemented.

```json
{
  "uid": "04A1B2C3D4E5F6",
  "type": "DESFire",
  "technology": "ISO14443A",
  "capabilities": { "canRead": true, "supportsNdef": false },
  "text": "",
  "err": null
}
```

A request for such a tag's message fails with `NO_PAYLOAD`.

### deviceStatus

The state of the agent's own reader:

```json
{
  "type": "deviceStatus",
  "payload": {
    "connected": true,
    "device": "ACS ACR122U PICC Interface",
    "message": "Device connected",
    "cardPresent": false
  }
}
```

It describes the agent's reader and nothing else: `cardPresent` is false the
whole time a phone holds a tag. `device` may be absent.

## Naming the tag

`writeRequest`, `lockRequest`, `capabilitiesRequest`, `transceiveRequest`,
`transceiveSequenceRequest`, `rawSessionBeginRequest` and `ntag424Request` name
the tag they apply to, with these payload fields:

| Field | Description |
|-------|-------------|
| `uid` | The `uid` from `tagData`, unchanged. Compared case-insensitively |
| `deviceID` | The `deviceID` from `tagData`. The request goes to that device or fails, never to the agent's reader. With `uid` as well, the device must still be holding that tag |
| `allowUntargeted` | `true` lets the agent pick the tag when neither of the above is given: the tag on a reader, otherwise the device that reported the most recent scan |

The agent finds whichever source holds the named tag, its own reader or a paired
device:

| Situation | Answer |
|-----------|--------|
| Neither `uid` nor `deviceID`, and no `allowUntargeted` | `TAG_NOT_NAMED` |
| Nothing holds the named tag | `NO_CARD`, retryable: present the tag and send the same request |
| A different tag is present where the named one was expected | `TAG_MISMATCH`, not retryable: read the tag again |

A request is never redirected to another tag. Resolving by "the most recent
scan" is decided when the request arrives, so a card swapped in the meantime
would receive a write meant for the one before it. That is why naming is the
default and guessing has to be asked for per request.

When an operator selects a reader in the tray or the Control Center, only that
reader's scans are broadcast and only its tag can be reached. A request naming
a tag on another reader fails as though nothing held it. Paired phones are not
affected.

## Requests

### writeRequest

Replaces the tag's contents with an NDEF message:

```json
{
  "id": "req_1",
  "type": "writeRequest",
  "payload": {
    "uid": "04A1B2C3D4E5F6",
    "records": [
      { "type": "text", "content": "Hello, NFC!", "language": "en" },
      { "type": "uri", "content": "https://example.com" }
    ]
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `records` | array | The message, one or more records. See below |
| `lock` | bool | Make the tag permanently read-only after the write. Irreversible |
| `idempotencyKey` | string | Identifies the logical write, passed on to a device. Defaults to the request `id` |
| `uid`, `deviceID`, `allowUntargeted` | | See [Naming the tag](#naming-the-tag) |

Record fields:

| Field | Type | Description |
|-------|------|-------------|
| `type` | string | The record type, below. Defaults to `text` |
| `content` | string | The main value: text, URI, `domain:type`, package name |
| `language` | string | Language code for `text` and `smartposter`. Defaults to `en` |
| `mimeType` | string | Media type for `mime` |
| `title` | string | Label for `smartposter` |
| `payload` | base64 | Raw bytes for `mime`, `vcard`, `external` and `raw` |
| `tnf` | number | Type Name Format, 0 to 7, for `raw` |
| `typeBytes` | base64 | Type field for `raw` |
| `id` | base64 | Record ID for `raw` |

| `type` | Uses | Notes |
|--------|------|-------|
| `text` | `content`, `language` | |
| `uri`, `url` | `content` | The URI prefix is abbreviated on the tag |
| `mailto` / `email`, `tel`, `sms`, `geo` | `content` | The scheme is added when absent |
| `smartposter` / `smart-poster` / `sp` | `content` (URI), `title`, `language` | |
| `mime` | `mimeType`, `payload` or `content` | Wi-Fi credentials are `mimeType: "application/vnd.wfa.wsc"` with a WSC `payload` |
| `vcard` / `vcf` | `content` or `payload` | A `text/vcard` record |
| `external` | `content` (`domain:type`), `payload` | NFC Forum external type |
| `aar` / `android` | `content` (package name) | Android Application Record |
| `empty` / `erase` | none | An empty record, which blanks the tag |
| `raw` / `custom` | `tnf`, `typeBytes`, `id`, `payload` | Any record, built by hand |

Before writing, the agent checks the encoded message against the tag's usable
capacity (`CAPACITY_EXCEEDED` if it does not fit). On its own readers it retries
transient failures and reads the data back to confirm it.

```json
{
  "id": "req_1",
  "type": "writeResponse",
  "success": true,
  "payload": {
    "message": "Write operation completed successfully",
    "uid": "04A1B2C3D4E5F6",
    "tagType": "NTAG215",
    "bytesWritten": 28,
    "verified": true,
    "attempts": 1,
    "locked": false
  }
}
```

| Field | Description |
|-------|-------------|
| `uid`, `tagType` | The tag written |
| `bytesWritten` | Size of the encoded NDEF message |
| `verified` | The agent read the data back and it matched. `false` for a tag a phone holds, whose reads are what it reported at the scan (`capabilities.readsAreSnapshot`), so the device's word is all there is |
| `attempts` | Write attempts, including retries |
| `locked` | The tag was made read-only |

A write that reads back wrong after its retries is answered with an error, not
with `verified: false`.

To add a record rather than replace the message, write the records the tag
already holds followed by the new one:

```javascript
const records = tag.message?.records.map(({ type, content, language }) => ({ type, content, language })) ?? [];
ws.send(JSON.stringify({
  id: "append-1",
  type: "writeRequest",
  payload: { uid: tag.uid, records: [...records, { type: "text", content: "Appended" }] },
}));
```

That keeps decodable records. A record with no `content`, such as a MIME
record, is copied faithfully only as `type: "raw"` with its `tnf`, `typeBytes`
and `payload`.

### lockRequest

Makes the tag permanently read-only without writing. Irreversible. Supported by
tags whose `capabilities.canLock` is true, such as NTAG21x and Ultralight.

```json
{ "id": "req_2", "type": "lockRequest", "payload": { "uid": "04A1B2C3D4E5F6" } }
```

```json
{
  "id": "req_2",
  "type": "lockResponse",
  "success": true,
  "payload": { "uid": "04A1B2C3D4E5F6", "tagType": "NTAG215", "locked": true }
}
```

Takes `idempotencyKey` as a write does. A lock is refused rather than redirected
when the tag present is not the one named. `tagType` is absent for a tag a
phone holds.

### capabilitiesRequest

Asks for the tag's capabilities now, rather than as they were at the scan:

```json
{ "id": "req_3", "type": "capabilitiesRequest", "payload": { "uid": "04A1B2C3D4E5F6" } }
```

The answer's payload is the capabilities object itself:

```json
{
  "id": "req_3",
  "type": "capabilitiesResponse",
  "success": true,
  "payload": { "canRead": true, "canWrite": true, "canLock": true, "maxNdefSize": 496, "tagFamily": "NTAG", "supportsNdef": true }
}
```

For a tag a phone holds it is answered from what the phone declared, with no
round trip to the phone. For an NTAG 424 DNA whose file settings were never
read, the agent reads them once so `sdmEnabled` is filled in.

### transceiveRequest

Sends raw bytes to the tag and returns its reply. Off until the operator opens
the raw APDU channel (`-allow-raw-apdu`, the tray's **Allow Raw APDU Channel**,
or the Control Center); until then it fails with `RAW_CHANNEL_DISABLED`. It is
also refused with `READ_ONLY` while the agent is in read-only mode, since the
agent cannot tell a read from a write in raw bytes. A raw command can lock a tag
or burn its one-time bits.

```json
{ "id": "req_4", "type": "transceiveRequest", "payload": { "uid": "04A1B2C3D4E5F6", "data": "/8oAAAA=" } }
```

| Field | Type | Description |
|-------|------|-------------|
| `data` | base64 | The command |
| `raw` | bool | Framing-level exchange instead of APDU-level. See [Framing-level exchange](#framing-level-exchange) |
| `autoGetResponse` | bool | Follow `61xx` and `6Cxx` replies. See [Response chaining](#response-chaining). Not valid with `raw` |
| `sessionId` | string | Send inside a [raw session](#raw-sessions), which already names the tag |

```json
{
  "id": "req_4",
  "type": "transceiveResponse",
  "success": true,
  "payload": { "data": "BKKzxNXmgJAA", "sw": "9000", "body": "BKKzxNXmgA==" }
}
```

`data` is the card's whole reply. For an APDU-level reply of two bytes or more,
`sw` is the status word as four uppercase hex characters and `body` is the reply
without it (absent when empty). A framing-level reply has neither.

Any status word is `success: true`, including `91 AF` and errors: the exchange
happened, and SW1SW2 is the caller's to interpret. `success` is false only when
the exchange could not be performed.

A tag a phone holds is reached only if the phone declared `canTransceive`. Each
exchange is a network round trip to the phone, so an exchange-heavy read is
slow there.

#### Response chaining

Without `autoGetResponse`, a `61 10` reply comes back as `61 10` and the client
sends `GET RESPONSE` itself. With it, the agent follows ISO 7816-4:

- A `6C xx` reply is answered once by resending the command with Le set to
  `xx`. An extended-length command is not retried.
- A `61 xx` reply is answered with `GET RESPONSE` (`INS C0`, Le `xx`), for as
  long as the card answers `61 xx`, at most 64 times. A card still answering
  `61 xx` after that has its last reply returned as it is.
- The reply is every data field received, concatenated, then the final status
  word.

`GET RESPONSE` takes its class byte from the command: the logical channel bits
for `0x00` to `0x0F`, the channel bits and `0x40` for `0x40` to `0x7F`, and the
command's own CLA for `0x80` and above. The follow-up commands run in the same
tag operation, so nothing else reaches the card between them.

#### Framing-level exchange

NTAG21x, Ultralight and MIFARE Classic tags do not speak APDUs, so an APDU-level
exchange is refused for them. `raw: true` sends the tag's own frame, with the
reader adding and checking the CRC: `30 04` (READ), `3A 04 07` (FAST_READ),
`3C 00` (READ_SIG), `39 02` (READ_CNT), `1B` and a password (PWD_AUTH). The
reply has no status word.

On the agent's readers it depends on the reader:

| Reader | Method | `canTransceiveRaw` |
|--------|--------|--------------------|
| ACR122 class (named `ACR122…`) | Direct Transmit carrying PN532 `InCommunicateThru` | `true` |
| Other PC/SC readers | PC/SC Part 3 transparent session (`FF C2`) | `true` once the reader accepts a start-session command, probed with its first card |
| Neither | | absent |

A reader that can do neither refuses `raw: true` with `NOT_SUPPORTED`; the frame
is never sent as an APDU instead. A framing-level exchange is refused inside a
raw session, and a sequence has no framing-level steps.

### transceiveSequenceRequest

Several APDU exchanges in one tag operation, so nothing else reaches the card
between them. Same gates as `transceiveRequest`.

```json
{
  "id": "req_5",
  "type": "transceiveSequenceRequest",
  "payload": {
    "uid": "04A1B2C3D4E5F6",
    "steps": [
      { "data": "AKQEAAfSdgAAhQEBAA==", "expectSW": ["9000"] },
      { "data": "kHEAAAIAAA==", "stopOnSW": ["6A82", "6700"] }
    ]
  }
}
```

| Field | Type | Description |
|-------|------|-------------|
| `steps` | array | 1 to 32 steps, run in order |
| `steps[].data` | base64 | The command |
| `steps[].expectSW` | string[] | Stop after this step unless its status word is one of these |
| `steps[].stopOnSW` | string[] | Stop after this step when its status word is one of these. Wins over `expectSW` |
| `steps[].autoGetResponse` | bool | Chain this step's reply. The stop rules see the final status word |
| `sessionId` | string | Run inside a [raw session](#raw-sessions) |

Status words are four hex characters, compared case-insensitively. A reply too
short to have a status word fails `expectSW`.

```json
{
  "id": "req_5",
  "type": "transceiveSequenceResponse",
  "success": true,
  "payload": {
    "results": [
      { "data": "kAA=", "sw": "9000" },
      { "data": "aoI=", "sw": "6A82" }
    ],
    "stoppedAt": 1
  }
}
```

`results` has one entry per step that ran, shaped as a `transceiveResponse`
payload. `stoppedAt` is the step that ended the run early, or `-1`. If an
exchange cannot be performed the request fails and earlier results are lost
with it. No steps, more than 32, bad base64 or a malformed status word is
`INVALID_REQUEST`.

On a phone's tag, the whole sequence is sent in one message when the phone
declared `canTransceiveSequence`, and one exchange at a time otherwise, with the
same result either way. A step with `autoGetResponse` makes the agent send the
sequence one exchange at a time.

### Raw sessions

Polling and other clients send commands to the card, and on some cards, an NTAG
424 DNA among them, any `SELECT` ends an authenticated session. A raw session
leases the reader to one client:

```json
{ "id": "req_6", "type": "rawSessionBeginRequest", "payload": { "uid": "04A1B2C3D4E5F6", "ttlMs": 5000 } }
```

```json
{ "id": "req_6", "type": "rawSessionBeginResponse", "success": true, "payload": { "sessionId": "9f2c0e…", "expiresInMs": 5000 } }
```

`ttlMs` defaults to 5000 and is capped at 30000. While the session is held the
agent sends the card nothing of its own, and other tag operations wait,
failing with `BUSY` if it is not released in time. Each exchange carrying the
`sessionId` renews it. It ends with `rawSessionEndRequest`
(`{ "sessionId": "…" }`, answered by `rawSessionEndResponse`), after `ttlMs`
without an exchange, when the card leaves, or when the client disconnects. A
request naming a session that has ended fails with `RAW_SESSION_EXPIRED`.

A tag a phone holds cannot be leased: the phone's operating system owns the tag
session. `rawSessionBeginRequest` answers `NOT_SUPPORTED` for it. A
`transceiveSequenceRequest` or `ntag424Request` runs uninterrupted on a phone
instead.

### ntag424Request

Operations on an NTAG 424 DNA, run by the agent in an authenticated session
with keys it holds. Keys are loaded at startup, see [Card keys](card-keys.md),
and never leave the agent. The tag can be on one of the agent's readers or in a
phone's field.

```json
{ "id": "req_7", "type": "ntag424Request", "payload": { "op": "getFileSettings", "uid": "04A1B2C3D4E5F6" } }
```

The answer is `ntag424Response` with `op` echoed and that operation's fields.

| `op` | Arguments | Answer fields | Changes the tag |
|------|-----------|---------------|-----------------|
| `getFileSettings` | `fileNo` (default 2, the NDEF file) | `fileSettings`: type, size, `commMode` (`plain`, `mac`, `full`), access rights, SDM settings | No |
| `getCardUID` | | `uid`: the real UID, which differs from the presented one under random ID | No |
| `getKeyVersion` | `keyNo` (0 to 4) | `keyNo`, `keyVersion` | No |
| `readSig` | | `signature`: the 56-byte originality signature, base64, not verified | No |
| `planSDM` | `urlTemplate`, `sdm` | `plan`: `ndefHex`, `length`, `settings` | No |
| `configureSDM` | `urlTemplate`, `sdm` | `sdm`: `url`, `verified`, `verifyError`, `uid`, `counter` | Yes |
| `changeKey` | `keyNo`, `authKeyNo`, `version`, `newKeySource`, `newKey`, `confirm` | `changed` | Yes, irreversible |
| `lock` | `confirm` | `locked` | Yes, irreversible |

**configureSDM** makes the tag serve a URL with per-tap data mirrored into it.
`urlTemplate` contains `{picc}` (encrypted UID and counter) or `{uid}` and
`{ctr}` (in the clear), optionally `{enc}` (encrypted file data), and always
`{mac}`, for example `https://example.com/t?p={picc}&m={mac}`. `sdm` sets key
numbers and access rights, each 0 to 4, 14 for free or 15 for never; omitted
ones default to `read` free, `counterRet` never and the rest key 0. `encLength`
sets the width of `{enc}`. The agent writes the message and the file settings,
then reads the file back as a tap and checks its MAC. The settings stay applied
when that check fails, with `verified: false` and the reason in `verifyError`.
The read-back counts as a tap, so `counter` is one higher than before. A failed
`configureSDM` says in its error what it left on the tag; running it again with
the same arguments completes it. See
[Pair a phone](device-setup.md#recovering-a-half-configured-tag) for the list.

**planSDM** returns what `configureSDM` would write without touching a tag. It
needs no tag and no key, works in read-only mode, and is not audited.
`plan.ndefHex` is the whole NDEF file (NLEN, then one URI record with every
mirror as ASCII zeros of its final width), `plan.length` its size, and
`plan.settings` the file settings in the shape `getFileSettings` returns.
Offsets count from the start of `ndefHex`. A template it cannot lay out is
`INVALID_REQUEST`.

**changeKey** replaces key `keyNo`, authenticating with key `authKeyNo`, and
sets its version byte. `newKeySource: "configured"` uses the key the agent
holds for `keyNo` for this tag (diversified when the key set is);
`"explicit"` uses `newKey`, 32 hex characters. The key is never echoed, logged
or audited. The agent's own key set is not updated, so load the new keys
before the next operation.

**lock** makes the NDEF file read-only by setting its write right to never. It
needs the change key.

`changeKey` and `lock` need `"confirm": true`, or fail with `INVALID_REQUEST`.
`configureSDM`, `changeKey` and `lock` are refused with `READ_ONLY` in read-only
mode. None needs the raw APDU channel.

| Failure | Code |
|---------|------|
| Not an NTAG 424 DNA, or a phone that declared no APDU exchange | `NOT_SUPPORTED` |
| No key held, key refused, the card's authentication delay (`91 AD`), a permission denial, an LRP-mode card the keys do not allow | `AUTH_FAILED` |
| Any other card status | `TRANSCEIVE_FAILED` |
| On a phone, the operation would not finish within its deadline | `TIMEOUT`, retryable, sent before any write |

On a phone's tag the operation has 15 seconds overall, counted from the first
exchange sent to the phone and never longer than the request's own timeout.
Before each step that changes the tag, the agent estimates the step's exchanges
times the slowest exchange so far (at least 100 ms) and fails with `TIMEOUT`
rather than start a write it cannot finish. On iOS a tag session lasts about 20
seconds and ends when the tag moves.

A tag in LRP mode is driven only when the key file sets `ntag424.allowLRP`. The
LRP implementation is checked against NXP's published test vectors for the
primitive and the authentication, and has not been checked against hardware or
a published transcript for secure messaging and SDM, so it is off by default.

To verify a tapped SDM URL on your backend, use the Go package
`github.com/dotside-studios/davi-nfc-agent/nfc/ntag424`: `VerifyURLFresh(url,
keys, store)` checks the MAC and refuses a replayed counter with `ErrReplay`,
tracking the highest counter per UID in a `CounterStore` such as
`MemoryCounterStore`. The agent has no verification route of its own.

## Tag capabilities

`tagData.capabilities` and `capabilitiesResponse` describe what can be done to
the tag now:

| Field | Description |
|-------|-------------|
| `canRead`, `canWrite` | NDEF read and write |
| `canLock` | Can be made permanently read-only |
| `canTransceive` | Raw APDU exchange |
| `isReadOnly` | Already locked |
| `readsAreSnapshot` | Reading returns what was captured at the scan, so a write cannot be confirmed by reading it back. True for a tag a phone holds |
| `memorySize` | Total memory in bytes |
| `maxNdefSize` | Largest NDEF message the tag holds, in bytes |
| `technology` | As on `tagData` |
| `tagFamily` | `MIFARE Classic`, `DESFire`, `DESFire EV1`, `DESFire EV2`, `DESFire EV3`, `NTAG`, `MIFARE Ultralight`, `Type 4`, `FeliCa`, … A phone may report its own value |
| `supportsNdef` | Holds or can hold an NDEF message |
| `supportsCrypto`, `supportsAuthentication` | The tag family has cryptographic authentication |
| `supportsPassword` | NTAG21x `PWD`/`PACK` password protection. Configuring it is not available over this protocol |
| `sdmEnabled` | NTAG 424 DNA: the NDEF file mirrors per-tap data, as last read |
| `keysHeld` | NTAG 424 DNA: key numbers the agent holds a key for |
| `randomID` | NTAG 424 DNA: the card presented a random UID |
| `lrp` | NTAG 424 DNA: the card is in LRP mode |

A field that is absent was not determined, which is not the same as `false`.

`canWrite`, `canLock` and `canTransceive` say what the agent will do, not only
what the tag is built for. They are false while the agent is in read-only mode,
and for a tag a phone holds they are false unless the phone declared the
operation and is still connected.

## Health check

```bash
curl --cacert ~/.config/davi-nfc-agent/tls/server.crt https://localhost:9470/api/v1/health
```

```json
{ "status": "ok", "type": "agent", "timestamp": "2026-03-14T09:26:53Z", "clients": 2 }
```

Served at `/health` and `/api/v1/health` on the agent port, over the same scheme
as the WebSocket, with no credential. `clients` is how many clients are
connected. `type` is always `agent`.
