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

Respond to a write request from the server:

```json
{
  "type": "deviceWriteResponse",
  "payload": {
    "requestID": "req_xyz789",
    "success": true,
    "error": ""
  }
}
```

### Messages to Device

#### Write Request

Server requests the device to write data to a tag:

```json
{
  "type": "deviceWriteRequest",
  "payload": {
    "requestID": "req_xyz789",
    "deviceID": "dev_abc123",
    "ndefMessage": {
      "records": [
        {
          "type": "text",
          "content": "Hello!",
          "language": "en"
        }
      ]
    }
  }
}
```

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
          "type": "T",
          "text": "Hello, NFC!",
          "payload": [72, 101, 108, 108, 111]
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
      "type": "T",
      "text": "Decoded text",
      "language": "en",
      "payload": [...]
    }
  ]
}
```

- `tnf`: Type Name Format (0x01 = Well Known)
- `type`: Record type (`T` = Text, `U` = URI)
- `text`: Decoded text (for Text records)
- `uri`: Decoded URI (for URI records)

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

The query requires exactly one tag to be present; if none (or several) are
present, `success` is `false` with an error.

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

The agent uses auto-generated TLS certificates for secure WebSocket connections.

### CA Bootstrap Server

A bootstrap server runs on port 9472 to help devices trust the agent's certificate:

1. Open `http://[agent-ip]:9472` in a browser
2. Download the CA certificate
3. Install on your device

### Installing the CA Certificate

**iOS:**
- Settings > Profile Downloaded > Install

**Android:**
- Settings > Security > Install certificate

**Browsers:**
- Import into browser's certificate store, or
- Use the JavaScript client which handles this automatically

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
| `READ_ONLY` | no | Tag is locked |
| `CAPACITY_EXCEEDED` | no | Data larger than the tag's usable NDEF capacity |
| `INVALID_DATA` | no | Data was malformed |
| `NO_CARD` | yes | No card present on reader |
