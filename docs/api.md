# API Reference

The NFC Agent serves both roles from a single server on one port:

| Server | Port | Purpose |
|--------|------|---------|
| **Agent Server** | 9470 | Serves both NFC devices (hardware readers, smartphones, browsers) via `/ws?mode=device` and client applications via `/ws` |
| **CA Bootstrap** | 9472 | Serves TLS certificates for device setup |

The agent server port is configurable via `-device-port` (default 9470).

Both listeners are plugins the program registers, so what an agent serves is
decided by the build rather than fixed here: this page describes the shipped
binary. See [Custom Builds](custom-builds.md#plugins) for the Go API behind it.

---

## Device API

The device endpoint accepts connections from NFC devices that provide tag data.

### Pairing

A device authenticates with its own credential, obtained once by presenting the
PIN shown on the kiosk (tray, logs, and the pairing QR).

The QR printed at startup carries where to pair, the agent's key pin and the
PIN:

```
davi-pair://[host]:9470/?spki=sha256%2F47DE…&code=123456&name=Davi%20NFC%20Agent
```

Read it off the kiosk screen, pin the TLS connection to `spki`, then:

```
POST https://[host]:9470/pair?pin=123456
Content-Type: application/json

{"deviceName": "Operator iPhone", "platform": "ios"}
```

Pairing is served from the agent's port, which serves the certificate `spki`
covers. Over a cleartext connection it is refused with `426 Upgrade Required`
from anything but loopback. Port 9472 is the cleartext bootstrap listener; it
serves the setup page and the certificate authority, and does not pair.

```json
{
  "deviceID": "6f1c…",
  "deviceToken": "kQ8x…",
  "publicKeyPin": "sha256/47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=",
  "agentPort": 9470
}
```

Store all three. `deviceToken` is presented on every later connection, as
`?secret=` or `Authorization: Bearer`. `publicKeyPin` is how the device
recognizes this agent again. See [TLS & Certificates](#tls--certificates).

The token is shown once. The agent keeps only its hash, so a lost token means
pairing again.

Each device holds its own credential, so one can be revoked from the tray under
**Paired Devices** without disturbing the others. The shared API secret still
works for devices configured with it, but rotating it locks out every device
configured with it, each on its next connection. Per-device tokens avoid that.

Wrong PINs lock pairing after five attempts until the agent restarts.

#### Requiring pairing

By default a device may also present the shared API secret. It remains so that
upgrading strands nothing.

`-require-paired-devices` (or **Require pairing** in the tray, or
`DAVI_NFC_REQUIRE_PAIRED_DEVICES=1`) withdraws it: only a credential issued at
pairing admits a device. Turn it on once the devices you care about have
paired. With none paired, every device connection is refused.

Browser consoles are unaffected: a browser has no way to pair and is gated by
the origin allowlist instead. This setting governs the device endpoint only.

The tray toggle takes effect immediately, so the policy can be tried against a
real device without restarting.

### Connecting

Connect via WebSocket with device mode:

```
wss://[host]:9470/ws?mode=device
```

Offer the `davi-nfc-device.v1` subprotocol during the upgrade. If the agent
echoes it back, it supports the `hello` handshake below. If it echoes nothing,
it predates versioning: fall back to [Legacy Registration](#legacy-registration-v0).

```javascript
const ws = new WebSocket('wss://host:9470/ws?mode=device', ['davi-nfc-device.v1']);
const version = ws.protocol === 'davi-nfc-device.v1' ? 1 : 0;
```

### Device Registration

Send `hello` as the first frame. It carries the protocol version alongside the
registration fields, so setup costs one round trip:

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
      "canWrite": false,
      "nfcType": "corenfc",
      "canTransceive": false,
      "canTransceiveRaw": false,
      "canTransceiveSequence": false,
      "canLock": false,
      "deviceType": "smartphone",
      "supportedTagTypes": ["NTAG", "MIFARE Ultralight"]
    },
    "metadata": {
      "userAgent": "..."
    }
  }
}
```

#### Device Capabilities

`canRead`, `canWrite`, and `nfcType` are the original v0 declaration and are
always sent. The rest are v1 additions: omit any that do not apply, and a
device declaring nothing extra sends exactly the v0 object.

The `capabilities` object itself is optional, and omitting it is not the same as
sending one of all falses. A device that sends the object is taken at its word:
a field it sets to `false` refuses that operation for every tag the device
holds, since a bridge that cannot carry an operation cannot carry it for any
tag. A device that omits the object has declared nothing about itself, so
requests go out and it answers them.

Per-tag capabilities on `tagScanned` are read the same way, and take precedence
for the tag they describe. See [Tag Capabilities](#tag-capabilities).

| Field | Meaning |
|-------|---------|
| `canRead` / `canWrite` | Device can read / write NDEF |
| `nfcType` | Radio technology or library: `nfca`, `isodep`, `corenfc`, `webnfc`, … |
| `canTransceive` | APDU-level exchange: Android `IsoDep.transceive`, iOS `sendCommand`, PN532 `InDataExchange` |
| `canTransceiveRaw` | Framing-level exchange: Android `NfcA.transceive`, PN532 `InCommunicateThru`. An agent's own PC/SC readers report it too, see [Framing-level exchange on a reader](#framing-level-exchange-on-a-reader) |
| `canTransceiveSequence` | Device runs a batch of APDU exchanges itself and answers [`deviceTransceiveSequenceRequest`](#transceive-sequence-request). Presumes `canTransceive` |
| `canLock` | Device can make a tag read-only |
| `deviceType` | Free-form kind, e.g. `smartphone`, `pn532-serial`. Defaults to `smartphone` |
| `supportedTagTypes` | Tag families this device handles, e.g. `["MIFARE Classic", "NTAG"]` |
| `maxBaudRate` | Maximum baud rate in bps, for serial-attached readers |
| `maxHoldMs` | How long a tag stays available for work after being reported. Omit for open-ended |

#### How long a tag stays available

A reader holding a tag in its field can act on it until it leaves, so it omits
`maxHoldMs` and the agent may take as long as it likes. A phone need not be so
lucky: CoreNFC connects a tag for roughly twenty seconds and cannot renew that,
so an iOS device declares `"maxHoldMs": 20000`, and everything the agent does
with the tag must fit inside it.

The deadline for a particular tag is the arrival of its `tagScanned` plus
`maxHoldMs`. That sum is optimistic, since the tag was already connected when
the message was sent, so leave margin rather than treating it as exact. A hold
that ends early, because the tag was pulled or the session was invalidated,
arrives as `tagRemoved` like any other departure.

The field is advisory. A device that declares nothing places no bound. Use it
to decide what to attempt; do not refuse a device that omitted it.

Capability is a set rather than a level: a PN532 reader can declare
`canTransceive` and MIFARE Classic support that an iPhone cannot, while the
iPhone declares NDEF abilities the reader lacks. Declare what is true and let
the agent decide what it can use.

**Response:**

```json
{
  "id": "req_1",
  "type": "helloResponse",
  "success": true,
  "payload": {
    "protocolVersion": 1,
    "deviceID": "dev_abc123",
    "serverInfo": {
      "version": "1.0.0",
      "supportedNFC": ["ndef", "mifare"]
    }
  }
}
```

`protocolVersion` in the response is what both sides will speak. It is never
higher than the version the device asked for: a device declaring a version newer
than the agent implements is answered at the agent's maximum rather than
refused. Devices should read this field rather than assume their request was
honoured.

`platform` is a free-form identifier describing the device, such as `ios`,
`android`, `web`, `node`, or `pn532-serial`. Nothing in the agent branches on
it; it is reported back in the console and the device list. Omit it and the
agent records `unknown`.

### Legacy Registration (v0)

Devices predating versioning send `registerDevice` as the first frame and get
`registerDeviceResponse` back. This exchange is unchanged and remains supported;
the payload is identical to `hello` minus `protocolVersion`.

```json
{
  "type": "registerDevice",
  "payload": {
    "deviceName": "My Device",
    "platform": "ios",
    "appVersion": "1.0.0",
    "capabilities": { "canRead": true, "canWrite": false, "nfcType": "corenfc" }
  }
}
```

The first frame's type selects the dialect, so the subprotocol offer is a hint
rather than a commitment: a device that offers nothing but sends `hello` is
still served at v1.

### Messages from Device

#### Tag Scanned

Send when a tag is detected:

```json
{
  "type": "tagScanned",
  "payload": {
    "deviceID": "dev_abc123",
    "uid": "04A1B2C3D4E5F6",
    "technology": "ISO14443A",
    "type": "MIFARE Classic 1K",
    "scannedAt": "2024-10-06T12:34:56Z",
    "ndefMessage": {
      "records": [
        {
          "recordType": "text",
          "content": "Hello, NFC!",
          "language": "en"
        }
      ]
    },
    "capabilities": {
      "memorySize": 1024,
      "maxNdefSize": 716,
      "tagFamily": "MIFARE Classic",
      "supportsNdef": true
    }
  }
}
```

`capabilities` (v1, optional) is what the device determined about this specific
tag. See [Tag Capabilities](#tag-capabilities) for the field list. Omit it and
the agent infers them from `type`, which is all a v0 device allows. Declared
values win over inference, except that operations the bridge cannot yet route
(`canWrite`, `canTransceive`, `canLock`) are reported as false whatever the
device claims.

#### Non-NFC scans (QR and barcodes)

A camera is a device like any other: it decodes a QR or barcode itself and
reports the value, exactly as a phone decodes NDEF off an NFC tag and reports
records rather than raw RF. **The agent never receives images or frames.**

The agent does not model optical codes; it carries the scan and stays out of the
way. Two things make that work:

- **A non-hex UID is carried verbatim.** The agent normalizes a hex NFC serial
  to its canonical colon form, but a UID that is not hex is not an NFC serial,
  so it is passed through byte-for-byte. A consumer keys on the exact value that
  was scanned.
- **Read-only falls out of the device's own capabilities.** A camera registers
  `canWrite: false` (and no lock or transceive), so the agent already refuses
  those operations. No special-casing is needed.

Report the scan as an ordinary `tagScanned` frame. Put the decoded value where a
consumer already looks: a card URL as a `uri` record (davi keys on the
`/c/{identifier}` path), and any stable non-empty `uid`. Nothing new is needed
on the wire:

```json
{
  "type": "tagScanned",
  "payload": {
    "deviceID": "dev_cam01",
    "uid": "https://davi.social/c/QR-ABC123",
    "technology": "qr",
    "type": "qr_card",
    "ndefMessage": {
      "records": [
        { "recordType": "uri", "content": "https://davi.social/c/QR-ABC123" }
      ]
    }
  }
}
```

`uid` must be non-empty, but its exact value is the device's choice — a consumer
that keys on the URL record uses `uid` only as a fallback. `technology` and
`type` are free-form and reported straight back to clients; the agent branches on
neither. A device that only scans codes registers with `deviceType: "camera"`,
`canWrite: false`. Send `tagRemoved` when a code leaves the frame, as for any
tag.

#### Goodbye

Send before disconnecting deliberately (v1). The agent acknowledges with a
normal WebSocket close and records a departure rather than a lost device:

```json
{
  "type": "goodbye",
  "payload": {
    "deviceID": "dev_abc123",
    "reason": "user stopped scanning"
  }
}
```

`reason` is optional and only reaches the agent's logs. Without a goodbye the
agent classifies the disconnect from the close handshake: a normal or
going-away close is still a clean departure. Anything else, an abrupt reset or
a dead radio, is reported as a dropped device.

#### Tag Removed

Send when a tag leaves the reader:

```json
{
  "type": "tagRemoved",
  "payload": {
    "deviceID": "dev_abc123",
    "uid": "04A1B2C3D4E5F6",
    "removedAt": "2024-10-06T12:35:00Z"
  }
}
```

#### Device Heartbeat

Keep connection alive:

```json
{
  "type": "deviceHeartbeat",
  "payload": {
    "deviceID": "dev_abc123",
    "timestamp": "2024-10-06T12:35:30Z"
  }
}
```

#### Write Response

Respond to a write request from the server. Required: the agent holds the
client's request open until this arrives, the device disconnects, or 20 seconds
pass:

```json
{
  "type": "deviceWriteResponse",
  "payload": {
    "requestID": "req_xyz789",
    "success": false,
    "error": "tag is read-only",
    "errorCode": "READ_ONLY"
  }
}
```

`errorCode` is optional but preferred: it lets the agent classify the failure
instead of parsing `error`. Use any code from [NFC errors](#nfc-errors).

### Messages to Device

#### Write Request

The agent asks the device to write the tag it is currently holding. A write is
routed to a device when no hardware reader has a card present and that device
reported the most recent scan.

```json
{
  "type": "deviceWriteRequest",
  "payload": {
    "requestID": "req_xyz789",
    "deviceID": "dev_abc123",
    "tagUID": "04:A1:B2:C3",
    "lock": false,
    "idempotencyKey": "req_xyz789",
    "ndefBytes": "0QEOVAJlbkhlbGxvLCBORkMh",
    "ndefMessage": {
      "records": [
        {
          "recordType": "text",
          "content": "Hello!",
          "language": "en"
        }
      ]
    }
  }
}
```

| Field | Description |
|-------|-------------|
| `ndefBytes` | The encoded NDEF message, base64 in transit. **Authoritative** where it and `ndefMessage` disagree: prefer it if the device can write raw NDEF |
| `ndefMessage` | The same message as records, for APIs like Web NFC that only accept records. Cannot express every record type faithfully |
| `tagUID` | UID the agent expects to be in the field. Report `TAG_REMOVED` if a different tag is present |
| `lock` | Make the tag permanently read-only after a successful write. Irreversible |
| `idempotencyKey` | Identifies the logical write |

**On `idempotencyKey`:** a device that has already applied a given key must
report the previous outcome rather than write again. The same request can arrive
twice: the agent sends a write, the device applies it, and the response is lost
to a dropped connection. Without the check, the retry writes a second time.

**Lock-only requests.** A client `lockRequest` arrives as the same frame with
`lock: true` and no `ndefBytes` or `ndefMessage`, since the protocol has one
tag-modifying frame, not two. Lock the tag as it stands and write nothing.
Answer with `deviceWriteResponse` as for any other write.

#### Transceive Request

The agent asks the device to exchange raw data with the tag it is holding. Sent
only to devices that declared `canTransceive`, and only for tags that support
it: the NDEF path handles ordinary reads and writes.

```json
{
  "type": "deviceTransceiveRequest",
  "payload": {
    "requestID": "req_abc",
    "deviceID": "dev_abc123",
    "tagUID": "04:A1:B2:C3",
    "data": "AKQEAA==",
    "raw": false,
    "timeoutMs": 5000
  }
}
```

| Field | Description |
|-------|-------------|
| `data` | Command bytes, base64 in transit |
| `raw` | `false` for APDU-level exchange (`IsoDep.transceive`, iOS `sendCommand`, PN532 `InDataExchange`); `true` for framing-level (`NfcA.transceive`, PN532 `InCommunicateThru`) |
| `tagUID` | UID the agent expects in the field. Report `TAG_REMOVED` if a different tag is present |
| `timeoutMs` | Bound for this single exchange |

Respond with `deviceTransceiveResponse`:

```json
{
  "type": "deviceTransceiveResponse",
  "payload": {
    "requestID": "req_abc",
    "success": true,
    "data": "kAA="
  }
}
```

`data` is the card's whole reply and must end with SW1SW2. A phone API that
returns the status word separately (iOS `sendCommand` returns it beside the
payload) must append it before answering, so the agent can tell `91 AF` from
`91 00` and leave interpretation to the client. A reply of status word alone is
valid.

There is no connect/disconnect pair around a transceive: a tag session is
already delimited by `tagScanned` and `tagRemoved`, and on phones the OS owns
the session.

**This costs one network round trip per command.** Reading NDEF off a MIFARE
Classic 1K is ~60 exchanges, seconds of tag-in-field time over WiFi, against a
single message on the NDEF path. Use the command channel for what genuinely
needs it (DESFire, ISO-DEP applets, capability probing), not as a general read
path. iOS also enforces its own session timeouts, so long sequences are more
likely to fail there.

#### Transceive Sequence Request

The agent asks the device to run several APDU-level exchanges with the tag it is
holding, one after another, with nothing else reaching the tag between them.
Sent only to devices that declared both `canTransceive` and
`canTransceiveSequence`. A device that did not declare it is sent one
`deviceTransceiveRequest` per step instead, under the same stop rules, so a
client's `transceiveSequenceRequest` works either way. The sequence request
saves a round trip per step, which matters while the user is holding the tag
against the phone.

```json
{
  "type": "deviceTransceiveSequenceRequest",
  "payload": {
    "requestID": "req_abc",
    "deviceID": "dev_abc123",
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
| `steps` | At most 32. Each is `data` (command bytes, base64) with the optional stop rules below |
| `steps[].expectSW` | Stop after this step unless the reply's status word is one of these, each four hex characters. Absent expects anything |
| `steps[].stopOnSW` | Stop after this step when the reply's status word is one of these |
| `tagUID` | UID the agent expects in the field. Report `TAG_REMOVED` if a different tag is present |
| `timeoutMs` | Bound for each exchange |

Run the steps in order, each as an APDU-level exchange as for
`deviceTransceiveRequest`. After each reply, take its last two bytes as the
status word: stop if it is in `stopOnSW`, or if `expectSW` is present and does
not contain it. The reply of the step that stops the run is still returned.
Respond with `deviceTransceiveSequenceResponse`:

```json
{
  "type": "deviceTransceiveSequenceResponse",
  "payload": {
    "requestID": "req_abc",
    "success": true,
    "replies": ["kAA=", "ka8="],
    "stoppedAt": 1
  }
}
```

| Field | Description |
|-------|-------------|
| `replies` | The whole reply of every step that ran, in order, each ending with SW1SW2 as in `deviceTransceiveResponse.data` |
| `stoppedAt` | Index of the step whose reply ended the run early, or `-1` when every step ran. Always sent on success |
| `success` | False only when an exchange could not be performed; then send `error` and `errorCode`, and no replies |

The agent checks the answer against the rules it sent: replies that run past a
stop, end before one, or a `stoppedAt` that disagrees with them, are refused as
a failed exchange rather than believed. Keep the same per-request timeout and
`TAG_REMOVED` behaviour as a single exchange. A phone cannot hold a raw session
for the agent (the operating system owns the tag session), and does not need
to: this request is the unit the agent treats as one tag operation.

### mDNS Discovery

The agent advertises via mDNS/Bonjour:

- **Service Type**: `_nfc-device._tcp`
- **Domain**: `local.`

Devices can discover the agent on the local network without knowing the IP address.

---

## Client API

The agent provides NFC data to client applications on the same port as devices
(plain `/ws`, without the `?mode=device` query). This is the agent server port
(default 9470, configurable via `-device-port`).

### Connecting

Connect via WebSocket:

```javascript
const ws = new WebSocket('ws://localhost:9470/ws');
```

**With API secret:**

```javascript
const ws = new WebSocket('ws://localhost:9470/ws?secret=your-secret');
```

A client on the agent's own host presents the secret like any other. See
[The loopback bypass](#the-loopback-bypass) for the setting that exempts it.

### Session Behavior

- First connection claims the session (automatic lock)
- Session released automatically on disconnect
- Subsequent connections rejected with `409 Conflict` until first disconnects

### Messages from Server

#### Device Status

```json
{
  "type": "deviceStatus",
  "payload": {
    "connected": true,
    "message": "Device connected",
    "cardPresent": false
  }
}
```

#### Tag Data

When a card is detected and read:

```json
{
  "type": "tagData",
  "payload": {
    "uid": "04A1B2C3D4E5F6",
    "type": "MIFARE Classic 1K",
    "technology": "ISO14443A",
    "scannedAt": "2024-10-06T12:34:56Z",
    "deviceID": "dev_abc123",
    "capabilities": {
      "canRead": true,
      "canWrite": true,
      "canLock": true,
      "maxNdefSize": 716,
      "tagFamily": "MIFARE Classic",
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

**Payload Fields:**

| Field | Description |
|-------|-------------|
| `uid` | Card unique identifier (hex string). For a non-NFC scan (a QR or barcode), the raw value the device reported, carried verbatim. See [Non-NFC scans](#non-nfc-scans-qr-and-barcodes) |
| `type` | Card type: `MIFARE Classic 1K`, `MIFARE Classic 4K`, `DESFire`, `MIFARE Ultralight`, `NTAG213`, `NTAG215`, `NTAG216`, `NTAG424`, `Type4`, `FeliCa`. Free-form for a non-NFC scan (whatever the device reported). A DESFire reports one type across its generations; `capabilities.tagFamily` names the generation |
| `technology` | NFC technology standard (`ISO14443A`, `ISO14443B`, etc.), or whatever the device reported for a non-NFC scan |
| `scannedAt` | ISO 8601 timestamp |
| `deviceID` | The paired device that scanned the tag. Omitted when the agent's own hardware reader read it. That is the only reader `deviceStatus` describes, so a client holding a tag can tell whether that status has anything to say about it |
| `capabilities` | What the tag supports. See [Tag Capabilities](#tag-capabilities) |
| `message` | Structured NDEF message data. Absent when the tag holds none, see [Identity-only scans](#identity-only-scans) |
| `text` | Quick access to first text record |
| `err` | Error message or `null` on success |

### Identity-only scans

A readable tag holding no NDEF message scans normally: `err: null`, no
`message` key, `text: ""`, and `capabilities.supportsNdef` false. This covers a
DESFire whose NDEF file requires keys the agent does not hold, a MIFARE Classic
not using default keys, and every FeliCa: the agent reads a FeliCa's IDm and
reports `technology: "ISO18092"`, but its command set is not implemented, so
transit cards and access badges scan for their identity alone.

Read `capabilities.supportsNdef` to distinguish it from a failed read, which
sets `err`:

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

A request that asks such a tag for its message is answered with `NO_PAYLOAD`.

**NDEF Message Structure:**

```json
{
  "type": "ndef",
  "records": [
    {
      "tnf": 1,
      "type": "text",
      "content": "Decoded text",
      "language": "en",
      "payload": "AmVuRGVjb2RlZCB0ZXh0"
    }
  ]
}
```

- `tnf`: Type Name Format (0x01 = Well Known)
- `type`: record type, human-readable. One of `text`, `uri`, `mime`,
  `smartposter`, `aar`, `external`, and so on. Not the raw NFC Forum type byte
- `content`: the record's decoded value, whatever its type. The text of a text
  record, the URI of a URI record. One field rather than one per type, since a
  record carries a single value and `type` beside it already says which kind.
  Omitted for a record with nothing decodable
- `language`: language code, text records only
- `id`: record ID, when the record carries one
- `payload`: the raw record payload, base64-encoded. This is the record's bytes
  as they sit on the tag, not the decoded value. A text record's payload leads
  with a status byte and the language code, which is why it does not simply
  base64-decode to `content`

The write direction uses these same names (see
[Write Request](#write-request)), so a record read from one tag can be written
back to another unchanged.

### Messages to Server

All client messages support an optional `id` field for request/response correlation.

#### Write Request

Write NDEF data to a card (complete overwrite):

```json
{
  "id": "req_1",
  "type": "writeRequest",
  "payload": {
    "records": [
      {
        "type": "text",
        "content": "Hello, NFC!",
        "language": "en"
      }
    ]
  }
}
```

**Multiple records:**

```json
{
  "id": "req_2",
  "type": "writeRequest",
  "payload": {
    "records": [
      {
        "type": "text",
        "content": "Hello, NFC!",
        "language": "en"
      },
      {
        "type": "uri",
        "content": "https://example.com"
      }
    ]
  }
}
```

**Record Fields:**

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `type` | string | No | Record type (see below). Defaults to `text`. |
| `content` | string | Varies | Primary value: text, URI, domain, package name, etc. |
| `language` | string | No | ISO language code for `text`/`smartposter` (default: `en`) |
| `mimeType` | string | No | Media type for `mime` records |
| `title` | string | No | Display title for `smartposter` records |
| `payload` | bytes (base64) | No | Raw bytes for `mime`, `vcard`, `external`, `raw` |
| `tnf` | number | No | Type Name Format (0–7) for `raw` records |
| `typeBytes` | bytes (base64) | No | NDEF type bytes for `raw` records |
| `id` | bytes (base64) | No | Optional record ID for `raw` records |

**Supported `type` values:**

| `type` | Fields used | Notes |
|--------|-------------|-------|
| `text` | `content`, `language` | Default when `type` omitted |
| `uri` / `url` | `content` | Prefix is auto-abbreviated to save tag space |
| `mailto` / `email`, `tel`, `sms`, `geo` | `content` | URI shortcut; scheme prepended if absent |
| `smartposter` | `content` (URI), `title`, `language` | "Tap to open *title*": URI + label |
| `mime` | `mimeType`, `payload` (or `content`) | Arbitrary MIME media record |
| `vcard` | `content` or `payload` | Contact card (`text/vcard` MIME) |
| `external` | `content` (`domain:type`), `payload` | NFC Forum external type |
| `aar` | `content` (package name) | Android Application Record (app launch) |
| `empty` / `erase` | none | Empty record: blanks/formats the tag (reversible) |
| `raw` | `tnf`, `typeBytes`, `id`, `payload` | Fully custom record |

WiFi credentials can be written as a `mime` record with `mimeType` set to
`application/vnd.wfa.wsc` and a WSC-formatted `payload`.

### Write Response

**Success:**

```json
{
  "id": "req_1",
  "type": "writeResponse",
  "success": true,
  "payload": {
    "message": "Write operation completed successfully",
    "uid": "04A1B2C3D4E5F6",
    "tagType": "MIFARE Ultralight",
    "bytesWritten": 28,
    "verified": true,
    "attempts": 1
  }
}
```

The agent confirms every write before reporting success: it checks the encoded
message against the tag's capacity, retries transient failures, and reads the
data back to verify it landed.

**Success Payload Fields:**

| Field | Type | Description |
|-------|------|-------------|
| `message` | string | Human-readable status |
| `uid` | string | UID of the tag that was written |
| `tagType` | string | Detected tag type |
| `bytesWritten` | number | Size of the encoded NDEF message written |
| `verified` | bool | `true` when the write was confirmed by reading it back |
| `attempts` | number | Number of write attempts before success |
| `locked` | bool | `true` when the tag was made read-only (see below) |

A write that cannot be confirmed (verification mismatch after retries) returns an
error response rather than a success: `success: true` means the data is on the
tag. A response with `verified: false` only occurs if verification was explicitly
disabled by the agent.

### Raw Exchange (transceive)

Exchange raw bytes with the tag currently present. Command and response are
base64 in transit, matching how the device protocol carries byte slices.

```json
{
  "id": "req_3",
  "type": "transceiveRequest",
  "payload": {
    "data": "/8oAAAA=",
    "raw": false
  }
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `data` | bytes (base64) | Yes | Command bytes to send |
| `raw` | bool | No | Framing-level exchange (`NfcA.transceive`, `InCommunicateThru`) instead of APDU-level (`IsoDep.transceive`, `InDataExchange`). See [Framing-level exchange on a reader](#framing-level-exchange-on-a-reader) |
| `sessionId` | string | No | Send the exchange inside a [raw session](#raw-sessions). The session already names the tag |
| `autoGetResponse` | bool | No | Follow `61xx` and `6Cxx` replies for the client; see [Response chaining](#response-chaining-autogetresponse). Not valid with `raw` |

**Response:**

```json
{
  "id": "req_3",
  "type": "transceiveResponse",
  "success": true,
  "payload": { "data": "BKKzxNXmgJAA", "sw": "9000", "body": "BKKzxNXmgA==" }
}
```

`data` is the card's whole reply, status word included, byte for byte. For an
APDU-level exchange (`raw` false) whose reply is at least two bytes, `sw` is the
trailing status word as four uppercase hex characters and `body` is the reply
without it, base64 (omitted when empty). Framing-level replies carry no status
word, so neither field is sent.

The request is routed like a write: to the remote device holding a tag when no
hardware reader has a card present, otherwise to the reader.

A tag answering with any status word is still `success: true`, because the
exchange happened, and interpreting SW1SW2 is the caller's job. That includes
`91 AF` (more frames, as in `AuthenticateEV2First`) and `91 AE`. `success` is
false only when the exchange itself could not be performed.

#### Response chaining (autoGetResponse)

By default the agent is a passthrough: a `61 10` reply comes back as `61 10`,
and a `6C 08` as `6C 08`, and the client sends the `GET RESPONSE` or the corrected
command itself, one round trip each. With `autoGetResponse: true` on a
`transceiveRequest`, or on a step of a `transceiveSequenceRequest`, the agent does
that following, as ISO 7816-4 describes:

- A `6C xx` reply (wrong Le) is answered once by re-sending the original command
  with Le replaced by `xx`. A command with no Le gets one appended. An
  extended-length command is not retried, as `6C` belongs to the short form.
- A `61 xx` reply is answered with `GET RESPONSE` (`INS C0`, P1 and P2 `00`, Le
  `xx`, where `00` means 256), repeated for as long as the card answers `61 xx`.
- The reply is every data field received, concatenated, followed by the final
  status word. A card that answers an error after sending some data has that data
  returned with the error status word.

`GET RESPONSE` takes its CLA from the command: for the first interindustry range
(`0x00` to `0x0F`) the logical channel bits only, so channel 2 sends `02 C0`;
for the further range (`0x40` to `0x7F`) the channel bits and the `0x40` marker;
for a proprietary class (`0x80` and above) the command's CLA unchanged.

The rounds are bounded at 64 `GET RESPONSE` commands. A card still answering
`61 xx` after that has its last reply returned as it is, ending in `61 xx`, so
the client can tell the data is incomplete. A command shorter than an APDU
header is never chained.

The follow-up commands share the exchange's tag operation, so polling and other
clients cannot reach the card between them, and inside a
[raw session](#raw-sessions) they run on the lease and renew it. A tag held by a
phone is chained by the agent over repeated device exchanges inside one
operation on that phone, a round trip each. A sequence with any
`autoGetResponse` step is sent to a phone step by step even when it declared
`canTransceiveSequence`, since the device message carries no such flag. Each follow-up is written to the audit log as part
of the exchange it followed, by its decoded summary, never its bytes.

#### Raw sessions

Polling and other operations send commands to the card, and on some cards
(an NTAG 424 DNA among them) any ISO `SELECT` or probe ends an authenticated
session. A raw session leases the reader to one client for a multi-step
exchange:

```json
{ "id": "req_4", "type": "rawSessionBeginRequest",
  "payload": { "uid": "04A1B2C3D4E5F6", "ttlMs": 5000 } }
```

```json
{ "id": "req_4", "type": "rawSessionBeginResponse", "success": true,
  "payload": { "sessionId": "9f2c0e...", "expiresInMs": 5000 } }
```

The request takes the usual tag target fields. `ttlMs` is optional: the default
is 5000 and the most is 30000. While the session is held the agent sends the
card nothing of its own, and other tag operations wait for the reader and fail
with `BUSY` if it is not released within the operation timeout. Each
`transceiveRequest` carrying the `sessionId` renews the time to live; the
session ends when the client sends `rawSessionEndRequest`
(`{ "sessionId": "..." }`, answered by `rawSessionEndResponse`), after `ttlMs`
without an exchange, when the card leaves, or when the client disconnects.

A request naming an unknown, ended or expired session fails with
`RAW_SESSION_EXPIRED`. Sessions are subject to the same gates as an exchange
(`RAW_CHANNEL_DISABLED`, `READ_ONLY`) and are written to the audit log, as is
each exchange.

**A tag held by a phone is never leased.** The agent does not poll a phone's
tag, so there is no poll for a lease to hold off, and the phone's operating
system owns the tag session (CoreNFC and Android's tag dispatch keep it open for
as long as the tag is in the field and end it on their own terms). A lease the
agent could not enforce would only promise what it cannot deliver, so
`rawSessionBeginRequest` for such a tag keeps answering `NOT_SUPPORTED`, with a
message saying why. What a lease is for is covered another way: a
`transceiveSequenceRequest` or an `ntag424Request` on a phone's tag runs as one
tag operation, with nothing else from the agent reaching the device between its
steps.

#### Framing-level exchange on a reader

An NTAG21x, Ultralight or MIFARE Classic tag does not speak APDUs, so an
APDU-level exchange (`raw` false) is refused for it. `raw: true` sends the tag's
own frame instead, with the reader adding and checking the CRC: `3C 00`
(READ_SIG), `39 02` (READ_CNT), `3A 04 07` (FAST_READ), `1B` and a password
(PWD_AUTH), `30 04` (READ). The reply is the tag's answer without a status word.

A phone honours `raw` as before. On a PC/SC reader it depends on the reader:

| Reader | Method | `canTransceiveRaw` |
|--------|--------|--------------------|
| ACR122 class (PN532/PN533 behind CCID, named `ACR122...`) | Direct Transmit (`FF 00 00 00 Lc`) carrying PN532 `InCommunicateThru` (`D4 42`). A nonzero PN532 status byte is an error: `01` is a timeout, the tag did not answer | `true`, known from the reader's name |
| Any other PC/SC reader | PC/SC Part 3 transparent exchange session (`FF C2`): start, transceive, end | `true` only once the reader accepted a start-session command, probed once per reader when its first card is connected |
| A reader that does neither | | omitted (false) |

A reader that cannot carry a framing-level exchange refuses `raw: true` with
`NOT_SUPPORTED`. The frame is never sent as an APDU instead. A framing-level
exchange is also refused with `NOT_SUPPORTED` inside a [raw session](#raw-sessions),
which carries APDU-level exchanges only, and `transceiveSequence` has no `raw`
step. A framing-level exchange runs under the reader's operation slot like any
other tag I/O, takes the same gates and is written to the audit log decoded as
a framing-level command.

> **Gated behind the raw APDU channel.** The channel that carries raw exchanges
> is off by default and refuses one with `RAW_CHANNEL_DISABLED` until an operator
> opens it — on the command line with `-allow-raw-apdu`, from the tray's *Allow
> Raw APDU Channel* toggle, or in the Control Center. A raw command reaches the
> tag unmodified and can burn OTP bits or lock a tag permanently, and the agent
> can neither recognise nor undo that, so opening the channel is a deliberate
> step.
>
> **Refused in read-only mode.** The channel being open is not enough: the agent
> cannot tell a `SELECT` from a write to a configuration page, so a raw exchange
> is treated as a write and also refused with `READ_ONLY` while the reader is
> read-only. The mode is checked first, so its refusal is the one you see when
> both apply.

Accepts an optional `deviceID`. See [Naming the tag](#naming-the-tag).

### Raw Sequence (transceiveSequence)

Several APDU exchanges under one tag operation, so polling and other clients
cannot reach the card between them. It is the multi-step counterpart of
[transceive](#raw-exchange-transceive) and shares its gates, audit and routing.

```json
{
  "id": "req_5",
  "type": "transceiveSequenceRequest",
  "payload": {
    "sessionId": "9f2c0e...",
    "steps": [
      { "data": "AKQEAAdE...", "expectSW": ["9000"] },
      { "data": "kHEAAAIAAA==", "stopOnSW": ["6A82", "6700"] }
    ]
  }
}
```

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `steps` | array | Yes | One to 32 steps, run in order |
| `steps[].data` | bytes (base64) | Yes | The command |
| `steps[].expectSW` | string[] | No | Four-hex-character status words. The run stops after this step unless its reply's status word is one of these |
| `steps[].stopOnSW` | string[] | No | The run stops after this step when its reply's status word is one of these. Wins over `expectSW` |
| `steps[].autoGetResponse` | bool | No | Chain this step's reply as on a [transceive](#response-chaining-autogetresponse). `expectSW` and `stopOnSW` then see the final status word |
| `sessionId` | string | No | Run inside a [raw session](#raw-sessions), which already names the tag |

Every step is APDU-level. Status words compare case-insensitively. A reply
shorter than a status word cannot match `expectSW`, so a run with `expectSW`
stops on it.

**Response** (`type: "transceiveSequenceResponse"`):

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

`results` has one entry per step that ran, each shaped as a
`transceiveResponse` payload (`data` the whole reply, `sw`, `body`). `stoppedAt`
is the index of the step whose reply ended the run early, or `-1` when every
step ran. A step that stops the run still returns its result with
`success: true`; `success` is false only when an exchange could not be performed
(the results of earlier steps are then lost with the error).

Gating is the same as a single exchange: `RAW_CHANNEL_DISABLED` while the raw
channel is closed and `READ_ONLY` in read-only mode. Each step is written to the
audit log before the run. A tag a phone holds is driven too, outside a raw
session: see [Sequences on a phone](#sequences-on-a-phone). A request with no steps, more than 32, bad base64 or a status
word that is not four hex characters is `INVALID_REQUEST`.

#### Sequences on a phone

A `transceiveSequenceRequest` for a tag a phone holds runs as one tag operation
on the device: the agent sends the whole batch in one
[`deviceTransceiveSequenceRequest`](#transceive-sequence-request) when the phone
declared `canTransceiveSequence`, and one `deviceTransceiveRequest` per step when
it did not, with the same `expectSW` and `stopOnSW` rules and the same
`stoppedAt` either way. A phone that declared no APDU exchange answers
`NOT_SUPPORTED`. Operations on one phone are serialized, and a request naming a
UID the phone is not holding fails instead of being sent. A sequence cannot run
in a [raw session](#raw-sessions) on a phone, since none can be begun.

### NTAG 424 DNA (ntag424Request)

Operations on an NTAG 424 DNA the agent holds keys for (see
`Supervisor.SetNTAG424Keys`). The agent builds every command and runs it in an
authenticated session; keys are held by the agent and never returned. The tag
is on one of the agent's own readers or in a phone's field (see
[NTAG 424 on a phone](#ntag-424-on-a-phone)), and is named like any other
([Naming the tag](#naming-the-tag)).

```json
{
  "id": "req_6",
  "type": "ntag424Request",
  "payload": { "op": "getFileSettings", "uid": "04A1B2C3D4E5F6" }
}
```

The response is `ntag424Response` with the `op` echoed and only that
operation's fields set.

| `op` | Arguments | Response fields | Changes the tag |
|------|-----------|-----------------|-----------------|
| `getFileSettings` | `fileNo` (default 2, the NDEF file) | `fileSettings`: file type and size, `commMode` (`plain`, `mac`, `full`), access rights, SDM flags and offsets | No |
| `getCardUID` | none | `uid`: the real UID, which differs from the presented one under random ID | No |
| `getKeyVersion` | `keyNo` (0 to 4) | `keyNo`, `keyVersion` | No |
| `readSig` | none | `signature`: the 56-byte originality signature, base64, unverified | No |
| `configureSDM` | `urlTemplate`, optional `sdm` | `sdm`: `url`, `verified`, `verifyError`, `uid`, `counter` | Yes |
| `changeKey` | `keyNo`, `authKeyNo`, `version`, `newKeySource`, `newKey`, `confirm` | `changed` | Yes, irreversible |
| `lock` | `confirm` | `locked` | Yes, irreversible |
| `planSDM` | `urlTemplate`, optional `sdm` | `plan`: `ndefHex`, `length`, `settings` | No |

**configureSDM** writes a URL that the tag mirrors per tap. `urlTemplate` holds
`{picc}` (encrypted UID and counter) or `{uid}` and `{ctr}` (in the clear),
optionally `{enc}` (encrypted file data), and always `{mac}`, for example
`https://example.com/t?p={picc}&m={mac}`. `sdm` carries key numbers and access
rights, each 0 to 4, 14 for free or 15 for never; an omitted one takes its
default (`read` free, `counterRet` never, the rest key 0), and `encLength` sets
the width of `{enc}`. The agent plans the layout, writes the NDEF message and
file settings, reads the file back as a tap and checks its MAC under the keys it
holds. The settings are applied even when verification fails: `verified` is then
false and `verifyError` says why. The read-back counts as one tap, so
`counter` is one higher than before.

**planSDM** is the dry run of `configureSDM`: it lays out the same URL template
with the same options and returns what would be written, touching no tag. It
needs no tag present and no key, is allowed in read-only mode, and is not
audited. `plan.ndefHex` is the whole NDEF file content (NLEN, then one URI
record with every mirror as ASCII zeros of its final width), `plan.length` its
size, and `plan.settings` the file settings that would be applied, in the shape
`getFileSettings` returns. The offsets count from the start of `ndefHex`, so the
first record byte is at 2. A template the planner refuses is `INVALID_REQUEST`.

**changeKey** replaces key `keyNo`, authenticating with `authKeyNo`, and sets
its version byte. `newKeySource` is `"configured"`, meaning the key the agent
holds for `keyNo` for this tag's UID (diversified when the key set is), or
`"explicit"` with `newKey` as 32 hex characters. The key is never echoed,
logged or audited: the audit entry names key numbers and the source only. The
agent's own key set is not updated; set the new keys before the next operation.
When `keyNo` is not `authKeyNo` the card checks the old key too, which is the
one the agent holds.

**lock** makes the NDEF file read-only by setting its write rights to never,
keeping the change right. It needs the change key.

`changeKey` and `lock` cannot be undone and need `"confirm": true`, or fail with
`INVALID_REQUEST`. `configureSDM`, `changeKey` and `lock` are refused with
`READ_ONLY` in read-only mode; the rest are reads. None needs the raw channel,
and each is written to the audit log (changes at warning level).

Errors: a tag that is not an NTAG 424 DNA, or a phone that declared no
APDU exchange, is `NOT_SUPPORTED`; a missing key, a refused key, the card's authentication delay
(`91 AD`), a permission denial or an LRP-mode card the keys do not allow is
`AUTH_FAILED`; other card
statuses are `TRANSCEIVE_FAILED`.

To check a tapped SDM URL on the backend, use the Go library
`github.com/dotside-studios/davi-nfc-agent/nfc/ntag424`: `VerifyURLFresh(url,
keys, store)` verifies the MAC and refuses a replay with `ErrReplay` by tracking
the highest counter seen per UID in a `CounterStore` (`MemoryCounterStore` is
one). The agent itself exposes no verify route. For a tag in LRP mode set
`Keys.LRP`; it is verified the same way.

A tag switched to the LRP cipher suite is driven only when the keys passed to
`SetNTAG424Keys` have `AllowLRP` set. The LRP primitive is checked against every
test vector in NXP's AN12304 and AuthenticateLRPFirst against the worked example
in AN12321, but LRP secure messaging and LRP SDM follow the NT4H2421Gx data
sheet with no published example to check them against, and none of it has run
against real hardware, so it is off by default.

A custom build can switch a tag to LRP with `NTAG424LRPSwitch.EnableLRP`, which
the NTAG 424 driver implements; it authenticates under key 0 and sends
SetConfiguration option `05h` as AN12321 Table 3 shows it. The switch is
permanent and disables the tag's SDM configuration, so it is not offered over
the client protocol.

#### NTAG 424 on a phone

The agent runs the same session against a tag a phone holds, over the phone's
`deviceTransceiveRequest` (or the sequence request below). Nothing is asked of
the phone beyond what it already declared with `canTransceive`. The cost is
round trips: an EV2 authentication is two exchanges after the application
select, and every protected command one more, so an operation that needs a
session takes at least four, and the next one authenticates again, since the
session lives only as long as the operation. The phone's reply must follow the
[reply format](#transceive-request), and a phone's own timeouts apply (iOS ends
a tag session after about twenty seconds). The agent's configured keys are used
as for a reader.

Operations on one phone are serialized, as a reader's are, so two clients'
exchanges do not interleave on the tag. A phone that names another card family
for the tag it holds (MIFARE Classic, Ultralight, NTAG21x, DESFire) is refused
with `NOT_SUPPORTED` before anything is sent; one that reports only `Type4` or
ISO-DEP is taken as it comes and the card's own answers decide.

### Tag Capabilities

Every `tagData` broadcast includes a `capabilities` object describing what the
present tag supports, so a client can gate its UI (show "lock"/"password" only
when supported, render a capacity meter, etc.) without a round-trip.

```json
{
  "canRead": true,
  "canWrite": true,
  "canTransceive": false,
  "canLock": true,
  "isReadOnly": false,
  "memorySize": 540,
  "maxNdefSize": 504,
  "technology": "ISO14443A",
  "tagFamily": "NTAG",
  "supportsNdef": true,
  "supportsPassword": true
}
```

| Field | Description |
|-------|-------------|
| `canRead` / `canWrite` | Whether read / write operations are supported |
| `canTransceive` | Raw APDU transceive supported |
| `canLock` | Tag can be made permanently read-only |
| `isReadOnly` | Tag is already locked (omitted when false) |
| `memorySize` | Total memory in bytes (omitted when unknown) |
| `maxNdefSize` | Maximum NDEF message size in bytes (omitted when unknown) |
| `tagFamily` | `MIFARE Classic`, `DESFire`, `DESFire EV1`, `DESFire EV2`, `DESFire EV3`, `NTAG`, `MIFARE Ultralight`, `Type 4`, `FeliCa`, … |
| `supportsNdef` | Tag supports NDEF |
| `supportsPassword` | Tag supports simple password protection (NTAG21x `PWD`/`PACK`) |
| `sdmEnabled` | NTAG 424 DNA: the NDEF file mirrors per-tap data, as far as its settings were last read (omitted when false) |
| `keysHeld` | NTAG 424 DNA: key numbers (0 to 4) the agent holds a key for, never the keys (omitted when none) |
| `randomID` | NTAG 424 DNA: the card presented a random UID for this tap (omitted when false) |
| `lrp` | NTAG 424 DNA: the card is in LRP mode, which the agent authenticates to only when `NTAG424Keys.AllowLRP` is set (omitted when false) |

`canWrite`, `canLock` and `canTransceive` describe what the agent will actually
do, not just what the tag is built for: they are reported false while the agent
is in read-only mode, and, for a tag held by a remote device, false unless
that device declared the operation and is still connected. A capability the
agent would refuse is never advertised.

**Query on demand**: to fetch capabilities without waiting for the next scan,
send a `capabilitiesRequest`:

```json
{
  "id": "req_cap",
  "type": "capabilitiesRequest"
}
```

Response (`type: "capabilitiesResponse"`):

```json
{
  "id": "req_cap",
  "type": "capabilitiesResponse",
  "success": true,
  "payload": {
    "capabilities": { "canWrite": true, "canLock": true, "supportsPassword": true, "maxNdefSize": 504 }
  }
}
```

The query is routed like a write: to the device holding a tag when no hardware
reader has a card, otherwise to the reader. For a device-held tag it is answered
from what the device declared at the scan, with no round trip, so it costs
nothing to ask. Accepts an optional `deviceID`; see
[Naming the tag](#naming-the-tag).

For an NTAG 424 DNA, a `capabilitiesRequest` reads the NDEF file's settings once
if they were never read and no session is open, so `sdmEnabled` is filled in;
the NTAG 424 fields otherwise come from what the driver already holds.

If nothing is holding a tag, `success` is `false` with `NO_CARD`.

### Locking Tags (Make Read-Only)

Locking is **irreversible**: once a tag is made read-only it can never be
written again. Only tags that support locking (e.g. NTAG, MIFARE Ultralight)
can be locked; others return an error.

**Write and lock in one step**: add `"lock": true` to a write request:

```json
{
  "id": "req_1",
  "type": "writeRequest",
  "payload": {
    "lock": true,
    "records": [{ "type": "uri", "content": "https://example.com" }]
  }
}
```

The write response then includes `"locked": true`.

**Lock an already-written tag**: send a `lockRequest`:

```json
{
  "id": "req_9",
  "type": "lockRequest"
}
```

Response (`type: "lockResponse"`):

```json
{
  "id": "req_9",
  "type": "lockResponse",
  "success": true,
  "payload": {
    "message": "Lock operation completed successfully",
    "uid": "04A1B2C3D4E5F6",
    "tagType": "MIFARE Ultralight",
    "locked": true
  }
}
```

If the present tag does not support locking, `success` is `false` with an error.

The request is routed like a write: to whichever source is holding the tag it
names. A device receives it as a `deviceWriteRequest` with `lock: true` and no
message.

Both take the `uid` of the tag they apply to, optionally a `deviceID`, and
optionally an `idempotencyKey`. See [Naming the tag](#naming-the-tag). A lock
cannot be undone, so it is refused rather than redirected when the tag named is
not the tag present.

> **Refused in read-only mode.** Locking is irreversible, so the agent's
> read-only mode refuses it with `READ_ONLY` on every route. A tag held by a
> phone included. Writes are refused the same way.

### Naming the Tag

`writeRequest`, `lockRequest`, `transceiveRequest` and `capabilitiesRequest` all
name the tag they apply to, with the `uid` from the `tagData` they are
responding to:

```json
{
  "id": "req_10",
  "type": "writeRequest",
  "payload": {
    "uid": "04A1B2C3D4E5F6",
    "records": [{ "type": "text", "content": "Hello" }]
  }
}
```

The agent finds whichever source is holding that tag, its own reader or a
paired device, and refuses the request if none is. It does not matter which
scanned most recently, or whether anything has been scanned since.

Naming the tag is what makes the target deterministic. Resolving instead by
whichever source scanned most recently is evaluated when the request arrives,
not when the tag was scanned, so a card lifted in between moves the write to a
different tag: a payload encoded for one tag lands on another, irreversibly so
when the request also locks.

A request whose tag is not present fails with `NO_CARD` and is never applied
somewhere else. It is retryable: present the tag again and the same request
works. If a tag is present but is not the one named, the failure is
`TAG_MISMATCH`, which is not retryable. Re-read the tag instead, because the
one now on the reader is a different tag with a different UID.

**Naming a device instead.** Every `tagData` carries the `deviceID` of the
device that scanned it, and a request may name that instead of, or alongside,
the `uid`:

```json
{ "deviceID": "dev_abc123", "uid": "04A1B2C3D4E5F6" }
```

Naming a device is decisive: the request goes to that device or fails, never
falling back to the reader, since a tag on the reader is a different tag. Giving
both holds the device to the UID too, so a `deviceID` remembered from an earlier
scan cannot act on whatever that device is holding now.

**Naming neither.** A request with no `uid` and no `deviceID` is refused with
`TAG_NOT_NAMED`. A client that genuinely cannot name its tag may opt back into
the old guess, per request:

```json
{ "allowUntargeted": true, "records": [{ "type": "text", "content": "Hello" }] }
```

It is a request field rather than an agent setting so that one such client
carries the risk itself, instead of the operator lowering the guarantee for
every client on the agent. A request that does name a tag is still checked.

> The bundled JavaScript client fills in `uid` from the last tag it saw, so
> `client.write({ records })` is already targeted and needs no change.

**The reader the operator picked.** When a reader is selected in the console or
the tray, the agent works with that one: its scans are the only ones sent, and
its tag is the only one a request can reach. A request naming another reader, or
a UID only another reader has seen, fails as though nothing were holding that
tag, and `allowUntargeted` resolves among the selected reader alone. What a
client is shown is what it can act on.

Devices that report their own scans, such as paired phones, are not affected:
the operator picked which reader to work with, not which phone.

**Idempotency.** `writeRequest` and `lockRequest` also accept an
`idempotencyKey`, passed through to the device. Reuse it when retrying after a
lost response and a device that already applied it reports the previous outcome
instead of writing again. Omitted, the request `id` is used, so reusing that on
a retry has the same effect.

### Password Protection (planned)

Password protection (NTAG `PWD`/`PACK`/`AUTH0`) is **not yet available**. The
per-tag capability is reported (`supportsPassword`, true for NTAG21x) and the
API contract below is fixed, but the destructive configuration writes are gated
off pending validation on real hardware: a wrong `AUTH0`/`ACCESS` value can
permanently lock a tag. Calls currently return a not-supported error.

Planned request shape (subject to change until enabled):

```json
{
  "id": "req_10",
  "type": "passwordRequest",
  "payload": {
    "action": "set",            // "set" or "remove"
    "password": "01020304",     // hex, 4 bytes
    "protectRead": false,        // false = write-protect only
    "startPage": 4               // first protected page (AUTH0)
  }
}
```

**Error:**

```json
{
  "id": "req_1",
  "type": "error",
  "success": false,
  "error": "Write failed: card removed",
  "payload": {
    "code": "WRITE_FAILED"
  }
}
```

### Append Pattern

To append records, use read-modify-write:

```javascript
// 1. Read current tag data
const currentData = await client.getLastTag();

// 2. Extract existing records
const existingRecords = currentData.message.records.map(r => ({
  type: r.type === 'T' ? 'text' : 'uri',
  content: r.text || r.uri,
  language: r.language || 'en'
}));

// 3. Write back with new record appended
socket.send(JSON.stringify({
  type: 'writeRequest',
  payload: {
    records: [...existingRecords, { type: 'text', content: 'New record' }]
  }
}));
```

---

## The loopback bypass

A connection from the agent's own host presents the shared API secret or a
token issued at pairing, like any other connection. Loopback identifies the
host, so admitting it without a credential also admits other accounts on that
host, local proxies, and port forwards into it.

`-allow-loopback-bypass` (or `DAVI_NFC_ALLOW_LOOPBACK_BYPASS=1`) admits loopback
with no credential, for a local client that cannot be given the secret. It
covers the shared secret only: under [Requiring pairing](#requiring-pairing) a
device connection still needs a paired credential. The console's control surface
is unaffected either way, requiring loopback, its own origin and a session
token.

The shipped console reads the secret from its session and sends it, so it needs
nothing here.

## REST API

Base URL: `http://localhost:9470/api/v1`

### Health Check

**GET `/api/v1/health`**

```bash
curl http://localhost:9470/api/v1/health
```

Response:

```json
{
  "status": "ok",
  "type": "agent",
  "timestamp": "2026-03-14T09:26:53Z",
  "clients": 2
}
```

`clients` is how many are connected right now.

Both `/health` and `/api/v1/health` are served on the agent server port and
report `"type": "agent"`. They are the agent's own routes, mounted on whatever
listener the build registers, so they are there whatever else is. A build puts
its own paths on the same port as endpoints of the server plugin, which is how
the Control Center is served from it.

---

## TLS & Certificates

The agent serves `wss://` with a self-signed certificate generated from a key it
creates once and keeps. Nothing is installed into any trust store by default.

### Native devices: pin the key

Phones, readers and other native clients **should not install a certificate
authority**. They verify the agent by pinning its public key, reported as
`serverInfo.publicKeyPin` at registration and handed out at pairing. The pin
survives certificate reissues, which happen whenever the host's addresses
change.

See [Setting up an iOS or Android device](device-setup.md) for the pairing flow
and the trust-evaluation code, including the two ways it commonly goes wrong.

### Browsers: provide a certificate, or install a CA

A browser cannot pin, so it needs a certificate it already trusts:

1. **Provide one**: point `-cert` / `-key` at a certificate for a name you
   control that resolves to the agent. Nothing is installed, and the browser
   trusts it because a public CA issued it.
2. **`-install-ca`**: creates a local certificate authority and installs it in
   the system trust store. A CA there can sign for **any** name, not just this
   agent, so prefer option 1 where you can arrange it.

With `-install-ca`, the bootstrap server on port 9472 serves the root
certificate for installation, PIN-gated.

Browsers also need their origin allowed. See
[Browser origins](control-center.md#browser-origins). A trusted certificate and
an allowed origin are separate requirements, and a failure of either looks the
same from the page.

---

## Error Codes

Errors arrive as a response with `success: false`, a human-readable `error`
string, and a structured payload:

```json
{
  "id": "req_1",
  "type": "error",
  "success": false,
  "error": "data too large: 900 bytes exceeds tag NDEF capacity of 504 bytes",
  "payload": {
    "code": "CAPACITY_EXCEEDED",
    "retryable": false,
    "op": "WriteData",
    "tagUID": "04:A1:B2:C3"
  }
}
```

`code` has always been present and its strings are stable. `retryable`, `op`,
and `tagUID` are additive: a client reading only `code` is unaffected.

`retryable` answers whether repeating the identical request could plausibly
succeed. Combined with `code` it gives three distinct outcomes:

| Condition | Meaning | What a client should do |
|-----------|---------|-------------------------|
| `retryable: true`, code ≠ `TAG_REMOVED` | Transient: I/O glitch, full queue, timeout | Retry, with backoff |
| `retryable: true`, code = `TAG_REMOVED` | The tag left the field mid-operation | Ask the user to present the tag again |
| `retryable: false` | Refused on its merits | Do not retry; surface it |

### Protocol errors

Raised by the bridge itself, before reaching a tag.

| Code | Retryable | Description |
|------|-----------|-------------|
| `PARSE_ERROR` | no | Message was not valid JSON |
| `INVALID_PAYLOAD` | no | Payload did not match the message type |
| `INVALID_REQUEST` | no | Required field missing or invalid |
| `INVALID_MESSAGE_TYPE` | no | Message type not valid at this point in the exchange |
| `UNKNOWN_TYPE` | no | Unrecognized message type |
| `INVALID_DEVICE` | no | Device ID did not match the connection |
| `TAG_MISMATCH` | no | The tag present is not the one the request named |
| `TAG_NOT_NAMED` | no | Request named no tag and did not ask for one to be guessed |
| `REGISTRATION_FAILED` | no | Device could not be registered |
| `SESSION_LOCKED` | no | Another client holds the session |
| `TAG_SEND_FAILED` | yes | Tag data could not be delivered internally |
| `READ_ERROR` | yes | Failed to read from the connection |
| `TIMEOUT` | yes | Operation timed out |
| `DEVICE_GONE` | no | Target device disconnected |
| `INTERNAL_ERROR` | yes | Unexpected agent-side failure |
| `BUSY` | yes | Earlier work has not finished: a reader completing an operation its caller abandoned, or more requests outstanding than the connection queues |
| `UNKNOWN_ERROR` | no | Unclassified: never advertised as retryable |

### NFC errors

Something happened at the tag. These mirror the agent's internal error codes.

| Code | Retryable | Description |
|------|-----------|-------------|
| `NOT_SUPPORTED` | no | Tag or device does not support the operation |
| `TAG_REMOVED` | yes | Tag left the field mid-operation |
| `AUTH_FAILED` | no | Authentication failed: the same key will fail again |
| `READ_FAILED` | yes | Read failed |
| `WRITE_FAILED` | yes | Write failed |
| `TRANSCEIVE_FAILED` | yes | Raw exchange failed |
| `RAW_SESSION_EXPIRED` | no | The raw session is unknown, ended or expired; begin a new one |
| `TAG_NOT_CONNECTED` | yes | No tag connected |
| `READ_ONLY` | no | Tag is locked, or the agent is in read-only mode |
| `RAW_CHANNEL_DISABLED` | no | The raw APDU channel is off; enable it to send raw exchanges |
| `CAPACITY_EXCEEDED` | no | Data larger than the tag's usable NDEF capacity |
| `INVALID_DATA` | no | Data was malformed |
| `MULTIPLE_TAGS` | no | More than one tag in the field; separate them and try again |
| `NO_CARD` | yes | Nothing is holding the tag the request named |
| `NO_PAYLOAD` | no | Tag read, holds no NDEF message. See [Identity-only scans](#identity-only-scans) |

---

## Related documentation

- [Custom Builds](custom-builds.md): the Go API for embedding the agent, and the plugins a build is assembled from
- [JavaScript client](javascript-client.md): the browser and Node.js client library
- [Device setup](device-setup.md): pairing a phone or a reader
- [Control Center](control-center.md): the built-in web console
