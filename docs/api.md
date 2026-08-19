# API Reference

The NFC Agent serves both roles from a single server on one port:

| Server | Port | Purpose |
|--------|------|---------|
| **Agent Server** | 9470 | Serves both NFC devices (hardware readers, smartphones, browsers) via `/ws?mode=device` and client applications via `/ws` |
| **CA Bootstrap** | 9472 | Serves TLS certificates for device setup |

The agent server port is configurable via `-device-port` (default 9470).

---

## Device API

The device endpoint accepts connections from NFC devices that provide tag data.

### Pairing

A device authenticates with its own credential, obtained once by presenting the
PIN shown on the kiosk (tray, logs, and the pairing QR):

```
POST http://[host]:9472/pair?pin=123456
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

Store all three. `deviceToken` is presented on every later connection, as
`?secret=` or `Authorization: Bearer`. `publicKeyPin` is how the device
recognizes this agent again — see
[How devices trust the agent](../README.md#how-devices-trust-the-agent).

**The token is shown once.** The agent keeps only its hash, so a lost token
means pairing again rather than looking it up.

Each device holds its own credential, so one can be revoked from the tray under
**Paired Devices** without disturbing the others. The shared API secret still
works for devices configured with it, but rotating it logs out everything at
once, which is what per-device tokens exist to avoid.

Wrong PINs lock pairing after five attempts until the agent restarts.

#### Requiring pairing

By default a device may also present the shared API secret, and a device
connecting over loopback needs no credential at all. Both remain so that
upgrading strands nothing.

`-require-paired-devices` (or **Require pairing** in the tray, or
`DAVI_NFC_REQUIRE_PAIRED_DEVICES=1`) withdraws both: only a credential issued at
pairing admits a device. Turn it on once the devices you care about have paired
— with none paired, every device connection is refused.

**Browser consoles are unaffected.** A browser has no way to pair, and is gated
by the origin allowlist instead. This setting governs the device endpoint only.

The tray toggle takes effect immediately, so the policy can be tried against a
real device without restarting.

### Connecting

Connect via WebSocket with device mode:

```
wss://[host]:9470/ws?mode=device
```

Offer the `davi-nfc-device.v1` subprotocol during the upgrade. If the agent
echoes it back, it supports the `hello` handshake below. If it echoes nothing,
it predates versioning — fall back to [Legacy Registration](#legacy-registration-v0).

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
always sent. The rest are v1 additions — omit any that do not apply, and a
device declaring nothing extra sends exactly the v0 object.

| Field | Meaning |
|-------|---------|
| `canRead` / `canWrite` | Device can read / write NDEF |
| `nfcType` | Radio technology or library: `nfca`, `isodep`, `corenfc`, `webnfc`, … |
| `canTransceive` | APDU-level exchange — Android `IsoDep.transceive`, iOS `sendCommand`, PN532 `InDataExchange` |
| `canTransceiveRaw` | Framing-level exchange — Android `NfcA.transceive`, PN532 `InCommunicateThru` |
| `canLock` | Device can make a tag read-only |
| `deviceType` | Free-form kind, e.g. `smartphone`, `pn532-serial`. Defaults to `smartphone` |
| `supportedTagTypes` | Tag families this device handles, e.g. `["MIFARE Classic", "NTAG"]` |
| `maxBaudRate` | Maximum baud rate in bps, for serial-attached readers |
| `maxHoldMs` | How long a tag stays available for work after being reported. Omit for open-ended |

#### How long a tag stays available

A reader holding a tag in its field can act on it until it leaves, so it omits
`maxHoldMs` and the agent may take as long as it likes. A phone need not be so
lucky: CoreNFC connects a tag for roughly twenty seconds and cannot renew that,
so an iOS device declares `"maxHoldMs": 20000` and everything the agent wants
done must fit inside it.

The deadline for a particular tag is the arrival of its `tagScanned` plus
`maxHoldMs`. That sum is optimistic — the tag was already connected when the
message was sent — so leave margin rather than treating it as exact. A hold that
ends early, because the tag was pulled or the session was invalidated, arrives as
`tagRemoved` like any other departure.

**It is advice, not permission.** A device that declares nothing is open-ended,
which is how every device behaved before this field existed. Use it to decide
what is worth attempting; never to refuse a device that stayed silent.

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

`platform` must be `ios`, `android`, or `web`.

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
rather than a commitment — a device that offers nothing but sends `hello` is
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
tag — see [Tag Capabilities](#tag-capabilities) for the field list. Omit it and
the agent infers them from `type`, which is all a v0 device allows. Declared
values win over inference, except that operations the bridge cannot yet route
(`canWrite`, `canTransceive`, `canLock`) are reported as false whatever the
device claims.

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
going-away close is still a clean departure, and anything else — an abrupt
reset, a dead radio — is reported as a dropped device.

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

Respond to a write request from the server. Required — the agent holds the
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

`errorCode` is optional but preferred — it lets the agent classify the failure
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
| `ndefBytes` | The encoded NDEF message, base64 in transit. **Authoritative** where it and `ndefMessage` disagree — prefer it if the device can write raw NDEF |
| `ndefMessage` | The same message as records, for APIs like Web NFC that only accept records. Cannot express every record type faithfully |
| `tagUID` | UID the agent expects to be in the field. Report `TAG_REMOVED` if a different tag is present |
| `lock` | Make the tag permanently read-only after a successful write. Irreversible |
| `idempotencyKey` | Identifies the logical write |

**On `idempotencyKey`:** a device that has already applied a given key must
report the previous outcome rather than write again. The same request can arrive
twice — the agent sends a write, the device applies it, and the response is lost
to a dropped connection. Without the check, the retry writes a second time.

**Lock-only requests.** A client `lockRequest` arrives as the same frame with
`lock: true` and no `ndefBytes` or `ndefMessage` — the protocol has one
tag-modifying frame, not two. Lock the tag as it stands and write nothing.
Answer with `deviceWriteResponse` as for any other write.

#### Transceive Request

The agent asks the device to exchange raw data with the tag it is holding. Sent
only to devices that declared `canTransceive`, and only for tags that support
it — the NDEF path handles ordinary reads and writes.

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

There is no connect/disconnect pair around a transceive: a tag session is
already delimited by `tagScanned` and `tagRemoved`, and on phones the OS owns
the session.

**This costs one network round trip per command.** Reading NDEF off a MIFARE
Classic 1K is ~60 exchanges — seconds of tag-in-field time over WiFi, against a
single message on the NDEF path. Use the command channel for what genuinely
needs it (DESFire, ISO-DEP applets, capability probing), not as a general read
path. iOS also enforces its own session timeouts, so long sequences are more
likely to fail there.

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
| `uid` | Card unique identifier (hex string) |
| `type` | Card type: `MIFARE Classic 1K`, `MIFARE Classic 4K`, `MIFARE DESFire`, `MIFARE Ultralight`, `ISO14443-4 Type 4A` (experimental) |
| `technology` | NFC technology standard (`ISO14443A`, `ISO14443B`, etc.) |
| `scannedAt` | ISO 8601 timestamp |
| `deviceID` | The paired device that scanned the tag. Omitted when the agent's own hardware reader read it — which is the only reader `deviceStatus` describes, so a client holding a tag can tell whether that status has anything to say about it |
| `capabilities` | What the tag supports — see [Tag Capabilities](#tag-capabilities) |
| `message` | Structured NDEF message data |
| `text` | Quick access to first text record |
| `err` | Error message or `null` on success |

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
- `type`: Record type, human-readable — `text`, `uri`, `mime`, `smartposter`,
  `aar`, `external`, and so on. Not the raw NFC Forum type byte
- `content`: the record's decoded value, whatever its type — the text of a text
  record, the URI of a URI record. One field rather than one per type, since a
  record carries a single value and `type` beside it already says which kind.
  Omitted for a record with nothing decodable
- `language`: language code, text records only
- `id`: record ID, when the record carries one
- `payload`: the raw record payload, base64-encoded. This is the record's bytes
  as they sit on the tag, not the decoded value — a text record's payload leads
  with a status byte and the language code, which is why it does not simply
  base64-decode to `content`

The write direction uses these same names — see
[Write Request](#write-request) — so a record read from one tag can be written
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
| `smartposter` | `content` (URI), `title`, `language` | "Tap to open *title*" — URI + label |
| `mime` | `mimeType`, `payload` (or `content`) | Arbitrary MIME media record |
| `vcard` | `content` or `payload` | Contact card (`text/vcard` MIME) |
| `external` | `content` (`domain:type`), `payload` | NFC Forum external type |
| `aar` | `content` (package name) | Android Application Record (app launch) |
| `empty` / `erase` | — | Empty record — blanks/formats the tag (reversible) |
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
error response rather than a success — `success: true` means the data is on the
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
| `raw` | bool | No | Framing-level exchange (`NfcA.transceive`, `InCommunicateThru`) instead of APDU-level (`IsoDep.transceive`, `InDataExchange`) |

**Response:**

```json
{
  "id": "req_3",
  "type": "transceiveResponse",
  "success": true,
  "payload": { "data": "BKKzxNXmgJAA" }
}
```

The request is routed like a write: to the remote device holding a tag when no
hardware reader has a card present, otherwise to the reader.

A tag answering with an error status word is still `success: true` — the
exchange happened, and interpreting SW1SW2 is the caller's job. `success` is
false only when the exchange itself could not be performed.

> **Refused in read-only mode.** The agent cannot tell a `SELECT` from a write
> to a configuration page, so a raw exchange is treated as a write and refused
> with `READ_ONLY`. A raw command can also burn OTP bits or lock a tag
> permanently, and the agent can neither recognise nor undo that.

Accepts an optional `deviceID`. See [Targeting a device](#targeting-a-device).

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
| `tagFamily` | `MIFARE Classic`, `DESFire`, `NTAG`, `MIFARE Ultralight`, `Type 4`, … |
| `supportsNdef` | Tag supports NDEF |
| `supportsPassword` | Tag supports simple password protection (NTAG21x `PWD`/`PACK`) |

`canWrite`, `canLock` and `canTransceive` describe what the agent will actually
do, not just what the tag is built for: they are reported false while the agent
is in read-only mode, and — for a tag held by a remote device — false unless
that device declared the operation and is still connected. A capability the
agent would refuse is never advertised.

**Query on demand** — to fetch capabilities without waiting for the next scan,
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
[Targeting a device](#targeting-a-device).

If nothing is holding a tag, `success` is `false` with `NO_CARD`.

### Locking Tags (Make Read-Only)

Locking is **irreversible** — once a tag is made read-only it can never be
written again. Only tags that support locking (e.g. NTAG, MIFARE Ultralight)
can be locked; others return an error.

**Write and lock in one step** — add `"lock": true` to a write request:

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

**Lock an already-written tag** — send a `lockRequest`:

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

The request is routed like a write: to the remote device holding a tag when no
hardware reader has a card present, otherwise to the reader. A device receives
it as a `deviceWriteRequest` with `lock: true` and no message.

Both accept an optional `deviceID` to name the device holding the tag, and an
optional `idempotencyKey`. See [Targeting a device](#targeting-a-device).

> **Refused in read-only mode.** Locking is irreversible, so the agent's
> read-only mode refuses it with `READ_ONLY` on every route — a tag held by a
> phone included. Writes are refused the same way.

### Targeting a Device

`writeRequest`, `lockRequest`, `transceiveRequest` and `capabilitiesRequest` all
accept an optional `deviceID`:

```json
{
  "id": "req_10",
  "type": "writeRequest",
  "payload": {
    "deviceID": "dev_abc123",
    "records": [{ "type": "text", "content": "Hello" }]
  }
}
```

Every `tagData` broadcast carries the `deviceID` of the device that scanned it,
so a client watching two phones can name the one it means.

Without `deviceID` the agent routes for you: its own reader while it holds a
card, otherwise whichever device scanned most recently. That is fine for one
device and ambiguous for several, which is what naming one resolves.

Naming a device is decisive. The request goes to that device or fails with
`NO_CARD`; it never falls back to the reader, since a tag on the reader is a
different tag.

**Idempotency.** `writeRequest` and `lockRequest` also accept an
`idempotencyKey`, passed through to the device. Reuse it when retrying after a
lost response and a device that already applied it reports the previous outcome
instead of writing again. Omitted, the request `id` is used, so reusing that on
a retry has the same effect.

### Password Protection (planned)

Password protection (NTAG `PWD`/`PACK`/`AUTH0`) is **not yet available**. The
per-tag capability is reported (`supportsPassword`, true for NTAG21x) and the
API contract below is fixed, but the destructive configuration writes are gated
off pending validation on real hardware — a wrong `AUTH0`/`ACCESS` value can
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
  "type": "agent"
}
```

Both `/health` and `/api/v1/health` are served on the agent server port and
report `"type": "agent"`.

---

## TLS & Certificates

The agent serves `wss://` with a self-signed certificate generated from a key it
creates once and keeps. Nothing is installed into any trust store by default.

### Native devices — pin the key

Phones, readers and other native clients **should not install a certificate
authority**. They verify the agent by pinning its public key, reported as
`serverInfo.publicKeyPin` at registration and handed out at pairing. The pin
survives certificate reissues, which happen whenever the host's addresses
change.

See [Setting up an iOS or Android device](device-setup.md) for the pairing flow
and the trust-evaluation code, including the two ways it commonly goes wrong.

### Browsers — provide a certificate, or install a CA

A browser cannot pin, so it needs a certificate it already trusts:

1. **Provide one** — point `-cert` / `-key` at a certificate for a name you
   control that resolves to the agent. Nothing is installed, and the browser
   trusts it because a public CA issued it.
2. **`-install-ca`** — creates a local certificate authority and installs it in
   the system trust store. A CA there can sign for **any** name, not just this
   agent, so prefer option 1 where you can arrange it.

With `-install-ca`, the bootstrap server on port 9472 serves the root
certificate for installation, PIN-gated.

Browsers also need their origin allowed — see
[Connecting from a web console](../README.md#connecting-from-a-web-console). A
trusted certificate and an allowed origin are separate requirements, and a
failure of either looks the same from the page.

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
and `tagUID` are additive — a client reading only `code` is unaffected.

**`retryable` is the field worth acting on.** It answers whether repeating the
identical request could plausibly succeed. Combined with `code` it gives three
distinct outcomes:

| Condition | Meaning | What a client should do |
|-----------|---------|-------------------------|
| `retryable: true`, code ≠ `TAG_REMOVED` | Transient — I/O glitch, full queue, timeout | Retry, with backoff |
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
| `REGISTRATION_FAILED` | no | Device could not be registered |
| `SESSION_LOCKED` | no | Another client holds the session |
| `TAG_SEND_FAILED` | yes | Tag data could not be delivered internally |
| `READ_ERROR` | yes | Failed to read from the connection |
| `TIMEOUT` | yes | Operation timed out |
| `DEVICE_GONE` | no | Target device disconnected |
| `INTERNAL_ERROR` | yes | Unexpected agent-side failure |
| `UNKNOWN_ERROR` | no | Unclassified — never advertised as retryable |

### NFC errors

Something happened at the tag. These mirror the agent's internal error codes.

| Code | Retryable | Description |
|------|-----------|-------------|
| `NOT_SUPPORTED` | no | Tag or device does not support the operation |
| `TAG_REMOVED` | yes | Tag left the field mid-operation |
| `AUTH_FAILED` | no | Authentication failed — the same key will fail again |
| `READ_FAILED` | yes | Read failed |
| `WRITE_FAILED` | yes | Write failed |
| `TRANSCEIVE_FAILED` | yes | Raw exchange failed |
| `TAG_NOT_CONNECTED` | yes | No tag connected |
| `READ_ONLY` | no | Tag is locked, or the agent is in read-only mode |
| `CAPACITY_EXCEEDED` | no | Data larger than the tag's usable NDEF capacity |
| `INVALID_DATA` | no | Data was malformed |
| `NO_CARD` | yes | No card present on reader |
