# Device API reference

The WebSocket protocol a phone, a WebNFC browser or your own hardware speaks to
report scans to the agent and carry out the tag operations clients ask for. To
pair a native app step by step, see [Pair a phone](device-setup.md).

## Pairing

A device presents its own credential, issued once against the pairing PIN.

```
POST https://<host>:9470/pair?pin=123456
Content-Type: application/json

{"deviceName": "Operator iPhone", "platform": "ios"}
```

```json
{
  "deviceID": "6f1c…",
  "deviceToken": "kQ8x…",
  "publicKeyPin": "sha256/47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=",
  "agentPort": 9470
}
```

| | |
|---|---|
| Method | `POST` only, otherwise `405` |
| PIN | Six digits, as `?pin=` or the `X-Bootstrap-PIN` header. Shown in the tray (**Copy Pairing PIN**), on the setup page at `http://<host>:9472`, and in the log |
| Channel | TLS, or loopback. Cleartext from anywhere else is refused with `426 Upgrade Required` before the PIN is checked |
| Lockout | After five wrong PINs every PIN-gated request answers `429` until the agent restarts or the PIN is regenerated from the tray |
| `deviceToken` | Returned once. The agent keeps only its hash, so a lost token means pairing again |
| `publicKeyPin` | The agent's public key pin. See [Trusting the agent](#trusting-the-agent) |

The agent logs a pairing QR when its setup listener starts, carrying where to
pair, the pin and the PIN:

```
davi-pair://<host>:9470/?code=123456&name=Davi+NFC+Agent&spki=sha256%2F47DE…
```

Read it off the kiosk screen. Its `spki` must equal the `publicKeyPin` the
pairing answers with.

Port 9472 serves the setup page and the local certificate authority in
cleartext, for a device that does not trust the agent yet. It does not pair.

## Connecting

```
wss://<host>:9470/ws?mode=device
```

| | |
|---|---|
| Mode | `?mode=device`, or the header `X-Device-Mode: true`. Without either, the connection is a client |
| Credential | `Authorization: Bearer <deviceToken>`, or `?secret=<deviceToken>`. The shared API secret is accepted too, until the agent runs with `-require-paired-devices` |
| Subprotocol | Offer `davi-nfc-device.v1`. If the agent echoes it, send `hello`; if not, it predates versioning, so send `registerDevice` |
| Frame size | Inbound frames are capped at 256 KB. A larger one ends the session |

The upgrade fails with `401` for a missing or wrong credential. Revoking a
device's credential from the tray or the Control Center closes its session
with close code `1008`.

```javascript
const ws = new WebSocket("wss://agent.local:9470/ws?mode=device&secret=DEVICE_TOKEN", ["davi-nfc-device.v1"]);
const version = ws.protocol === "davi-nfc-device.v1" ? 1 : 0;
```

### Trusting the agent

The agent's certificate is self-signed. A device verifies it by comparing the
SHA-256 of the certificate's SubjectPublicKeyInfo, base64, against the stored
`publicKeyPin`, and refuses the connection when they differ. The pin is the
same across certificate reissues, which happen when the host's addresses
change. Code for Android and iOS is in
[Pair a phone](device-setup.md#3-verify-the-agent-by-its-pin).

### Discovery

The agent advertises itself over mDNS:

| | |
|---|---|
| Service | `_nfc-device._tcp`, domain `local.` |
| Instance | `Davi NFC Agent Device` |
| Port | The agent port |
| TXT | `version=1.0`, `protocol=websocket`, `path=/ws`, `type=agent` |

The advertisement does not say whether the agent serves TLS; it does unless it
was started with `-auto-tls=false`.

## Registration

The first frame is `hello`. Anything else first is refused with
`INVALID_MESSAGE_TYPE`.

```json
{
  "id": "req_1",
  "type": "hello",
  "payload": {
    "protocolVersion": 1,
    "deviceName": "My Device",
    "platform": "ios",
    "appVersion": "1.0.0",
    "capabilities": {
      "canRead": true,
      "canWrite": true,
      "nfcType": "corenfc",
      "canTransceive": true,
      "canLock": true,
      "deviceType": "smartphone",
      "supportedTagTypes": ["NTAG", "MIFARE Ultralight"],
      "maxHoldMs": 20000
    },
    "metadata": { "userAgent": "…" }
  }
}
```

```json
{
  "id": "req_1",
  "type": "helloResponse",
  "success": true,
  "payload": {
    "protocolVersion": 1,
    "deviceID": "6f1c…",
    "sessionToken": "",
    "serverInfo": {
      "version": "1.0.0",
      "supportedNFC": ["mifare", "desfire", "type4", "ultralight"],
      "publicKeyPin": "sha256/47DE…"
    }
  }
}
```

The answer's `protocolVersion` is what both sides speak: never higher than the
device asked for, and the agent's highest when the device asked for more. Read
it rather than assuming the request was honoured. `deviceID` is the paired
device's ID. `sessionToken` is unused.

`platform` is free-form (`ios`, `android`, `web`, `node`, …), shown in the
Control Center and the device list, and `unknown` when omitted.

A device predating versioning sends `registerDevice` with the same payload less
`protocolVersion`, and is answered with `registerDeviceResponse`. The first
frame's type decides the dialect, so a device that offered no subprotocol but
sends `hello` is served at version 1.

### Device capabilities

`canRead`, `canWrite` and `nfcType` are the version 0 fields. The others are
version 1 additions; leave out any that do not apply.

| Field | Meaning |
|-------|---------|
| `canRead`, `canWrite` | Reads and writes NDEF |
| `nfcType` | The NFC stack: `nfca`, `isodep`, `corenfc`, `webnfc`, … |
| `canTransceive` | APDU-level exchange: Android `IsoDep.transceive`, iOS `sendCommand`, PN532 `InDataExchange` |
| `canTransceiveRaw` | Framing-level exchange: Android `NfcA.transceive`, PN532 `InCommunicateThru` |
| `canTransceiveSequence` | Runs a batch of exchanges itself and answers [`deviceTransceiveSequenceRequest`](#devicetransceivesequencerequest). Needs `canTransceive` |
| `canLock` | Makes a tag read-only |
| `deviceType` | Free-form kind, such as `smartphone`, `camera`, `pn532-serial`. Defaults to `smartphone` |
| `supportedTagTypes` | Tag families the device handles |
| `maxBaudRate` | Serial readers: the maximum baud rate |
| `maxHoldMs` | How long a tag stays available after it is reported. CoreNFC holds a tag for about 20 seconds, so an iOS device declares `20000`. Omit it for a reader that holds a tag until it leaves |

A device that sends `capabilities` is taken at its word: a field set to `false`,
or left out, refuses that operation for every tag the device reports. A device
that sends no `capabilities` object at all has declared nothing, and requests
are sent to it.

`maxHoldMs` is advisory. The deadline for a tag is roughly its `tagScanned`
plus `maxHoldMs`; a hold that ends early arrives as `tagRemoved`.

## Messages from the device

### tagScanned

```json
{
  "type": "tagScanned",
  "payload": {
    "deviceID": "6f1c…",
    "uid": "04A1B2C3D4E5F6",
    "technology": "ISO14443A",
    "type": "NTAG215",
    "scannedAt": "2026-03-14T09:26:53Z",
    "ndefMessage": {
      "records": [
        { "recordType": "text", "content": "Hello, NFC!", "language": "en" }
      ]
    }
  }
}
```

| Field | Required | Description |
|-------|----------|-------------|
| `deviceID` | yes | The `deviceID` from `helloResponse`. Another value is `INVALID_DEVICE` |
| `uid` | yes | The tag's serial as hex. Clients receive it colon-separated (`04:A1:B2:…`). A value that is not hex, such as a decoded QR code, is passed on unchanged |
| `technology` | yes | `ISO14443A`, `ISO14443B`, … Free-form, passed on to clients |
| `type` | | `NTAG215`, `MIFARE Classic 1K`, `Type4`, … Passed on to clients and used to infer the tag's capabilities |
| `scannedAt` | | RFC 3339 |
| `atr` | | Answer to reset |
| `ndefMessage` | | The tag's NDEF message as `records`. See below |
| `rawData` | | The tag's contents, base64 |
| `capabilities` | | What the device determined about this tag. See below |

Each record is either high-level or low-level:

| Field | Description |
|-------|-------------|
| `recordType` | `text`, `uri`, `mime` or `external`. The record type's name is `recordType`, not `type` |
| `content` | The text or URI |
| `language` | Text records. Defaults to `en` |
| `mimeType` | MIME records |
| `tnf` | Low-level: Type Name Format, 0 to 7 |
| `type` | Low-level: the record type bytes, base64 |
| `id` | Low-level: the record ID, base64 |
| `payload` | Low-level: the payload bytes, base64 |

`capabilities` takes the [tag capability fields](client-api.md#tag-capabilities)
of the client protocol, and replaces what the agent would infer from `type`. An
object that names only some fields sets the rest to false, so a device that
sends `capabilities` sends `canRead`, `canWrite` and `supportsNdef` as well.
Whatever the device sends, `canWrite`, `canLock` and `canTransceive` reach
clients true only when the device declared the operation at registration and
the agent is not in read-only mode.

#### QR codes and barcodes

A camera reports a decoded code as an ordinary scan; the agent never receives
images. Put the value where a consumer looks for it, such as a card URL in a
`uri` record, give any stable non-empty `uid`, and register with
`canWrite: false`:

```json
{
  "type": "tagScanned",
  "payload": {
    "deviceID": "dev_cam01",
    "uid": "https://davi.social/c/QR-ABC123",
    "technology": "qr",
    "type": "qr_card",
    "ndefMessage": {
      "records": [{ "recordType": "uri", "content": "https://davi.social/c/QR-ABC123" }]
    }
  }
}
```

Send `tagRemoved` when the code leaves the frame.

### tagRemoved

```json
{
  "type": "tagRemoved",
  "payload": { "deviceID": "6f1c…", "uid": "04A1B2C3D4E5F6", "removedAt": "2026-03-14T09:27:10Z" }
}
```

### deviceHeartbeat

```json
{ "type": "deviceHeartbeat", "payload": { "deviceID": "6f1c…", "timestamp": "2026-03-14T09:27:30Z" } }
```

Send one every 30 seconds. A device silent for 90 seconds is dropped.

### goodbye

Sent before closing deliberately (version 1). The agent then records a
departure rather than a lost device:

```json
{ "type": "goodbye", "payload": { "deviceID": "6f1c…", "reason": "user stopped scanning" } }
```

`reason` is optional and goes to the agent's log. Without a goodbye, a normal or
going-away close is still a departure; anything else is a dropped device.

## Requests to the device

Each request carries a `requestID` that the answer echoes, and the `tagUID` the
agent expects in the field. If a different tag is there, answer with
`errorCode: "TAG_REMOVED"`. Requests to one device are sent one at a time.

Answers carry `success`, and on failure `error` (a message) and `errorCode`, a
code from the [errors reference](errors.md), so the agent need not parse the
message.

### deviceWriteRequest

Write an NDEF message to the tag, sent when a client writes to a tag this
device reported.

```json
{
  "type": "deviceWriteRequest",
  "payload": {
    "requestID": "write-1",
    "deviceID": "6f1c…",
    "tagUID": "04:A1:B2:C3:D4:E5:F6",
    "ndefMessage": null,
    "ndefBytes": "0QEMVQRleGFtcGxlLmNvbQ==",
    "lock": false,
    "idempotencyKey": "req_1"
  }
}
```

| Field | Description |
|-------|-------------|
| `ndefBytes` | The encoded NDEF message, base64. Write these bytes as the tag's message |
| `ndefMessage` | Always `null` |
| `lock` | Make the tag read-only after a successful write. Irreversible |
| `idempotencyKey` | Identifies the logical write. A device that has already applied a key reports the earlier outcome instead of writing again, since an answer lost to a dropped connection is followed by the same request |

A request with `lock: true` and no `ndefBytes` is a lock on its own: make the
tag read-only as it stands and write nothing.

A stack that writes only records, such as Web NFC, decodes `ndefBytes` into
records first. See the
[JavaScript client](javascript-client.md#webnfc) for a decoder.

Answer within 20 seconds; after that the client is told the write failed.

```json
{
  "type": "deviceWriteResponse",
  "payload": { "requestID": "write-1", "success": false, "error": "tag is read-only", "errorCode": "READ_ONLY" }
}
```

### deviceTransceiveRequest

Exchange bytes with the tag. Sent only to a device that declared
`canTransceive`.

```json
{
  "type": "deviceTransceiveRequest",
  "payload": {
    "requestID": "req_abc",
    "deviceID": "6f1c…",
    "tagUID": "04:A1:B2:C3",
    "data": "AKQEAA==",
    "raw": false,
    "timeoutMs": 5000
  }
}
```

| Field | Description |
|-------|-------------|
| `data` | The command, base64 |
| `raw` | `false` for an APDU-level exchange (`IsoDep.transceive`, iOS `sendCommand`), `true` for framing-level (`NfcA.transceive`) |
| `timeoutMs` | The bound for this exchange |

```json
{
  "type": "deviceTransceiveResponse",
  "payload": { "requestID": "req_abc", "success": true, "data": "kAA=" }
}
```

`data` is the card's whole reply, ending with SW1SW2 for an APDU-level
exchange. iOS `sendCommand` returns the status word separately, so append
`sw1` and `sw2` before answering. A reply of the status word alone is valid. A
card's error status is still `success: true`; `success: false` means the
exchange could not be performed.

Every exchange is a network round trip, so the agent uses this for what needs
it (DESFire, ISO-DEP applications, NTAG 424 DNA operations) and the NDEF path
for reads and writes.

### deviceTransceiveSequenceRequest

Run several APDU-level exchanges in order with nothing else reaching the tag
between them. Sent only to a device that declared `canTransceive` and
`canTransceiveSequence`; other devices are sent one `deviceTransceiveRequest`
per step instead.

```json
{
  "type": "deviceTransceiveSequenceRequest",
  "payload": {
    "requestID": "req_abc",
    "deviceID": "6f1c…",
    "tagUID": "04:A1:B2:C3",
    "steps": [
      { "data": "AKQEAAfSdgAAhQEBAA==", "expectSW": ["9000"] },
      { "data": "kEEAAAA=", "stopOnSW": ["91AF"] }
    ],
    "timeoutMs": 5000
  }
}
```

| Field | Description |
|-------|-------------|
| `steps` | At most 32, each `data` (base64) with optional stop rules |
| `steps[].expectSW` | Stop after this step unless its status word, the reply's last two bytes as four hex characters, is one of these |
| `steps[].stopOnSW` | Stop after this step when its status word is one of these |
| `timeoutMs` | The bound for each exchange |

```json
{
  "type": "deviceTransceiveSequenceResponse",
  "payload": { "requestID": "req_abc", "success": true, "replies": ["kAA=", "ka8="], "stoppedAt": 1 }
}
```

| Field | Description |
|-------|-------------|
| `replies` | The whole reply of every step that ran, in order, each as in `deviceTransceiveResponse` |
| `stoppedAt` | The step whose reply ended the run early, or `-1` when every step ran |

The agent checks the answer against the rules it sent, and treats replies that
run past a stop, stop early, or disagree with `stoppedAt` as a failed exchange.

## Errors from the agent

A device message the agent refuses is answered with an `error` frame, shaped as
on the [client endpoint](errors.md#shape):

```json
{ "type": "error", "success": false, "error": "Invalid tag data format", "payload": { "code": "INVALID_PAYLOAD", "retryable": false } }
```
