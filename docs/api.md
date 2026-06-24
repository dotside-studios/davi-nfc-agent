# API Reference

The NFC Agent uses a two-server architecture:

| Server | Port | Purpose |
|--------|------|---------|
| **Device Server** | 9470 | Connects NFC devices (hardware readers, smartphones, browsers) |
| **Client Server** | 9471 | Serves client applications consuming NFC data |
| **CA Bootstrap** | 9472 | Serves TLS certificates for device setup |

---

## Device Server API

The Device Server accepts connections from NFC devices that provide tag data.

### Connecting

Connect via WebSocket with device mode:

```
wss://[host]:9470/ws?mode=device
```

### Device Registration

After connecting, register the device:

```json
{
  "type": "registerDevice",
  "payload": {
    "deviceName": "My Device",
    "platform": "ios",
    "appVersion": "1.0.0",
    "capabilities": {
      "canRead": true,
      "canWrite": false,
      "nfcType": "corenfc"
    },
    "metadata": {
      "userAgent": "..."
    }
  }
}
```

**Registration Response:**

```json
{
  "type": "registerDeviceResponse",
  "success": true,
  "payload": {
    "deviceID": "dev_abc123",
    "serverInfo": {
      "version": "1.0.0",
      "supportedNFC": ["ndef", "mifare"]
    }
  }
}
```

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
    }
  }
}
```

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

The Device Server advertises via mDNS/Bonjour:

- **Service Type**: `_nfc-device._tcp`
- **Domain**: `local.`

Devices can discover the agent on the local network without knowing the IP address.

---

## Client Server API

The Client Server provides NFC data to client applications.

### Connecting

Connect via WebSocket:

```javascript
const ws = new WebSocket('ws://localhost:9471/ws');
```

**With API secret:**

```javascript
const ws = new WebSocket('ws://localhost:9471/ws?secret=your-secret');
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
| `type` | string | Yes | `text` or `uri` |
| `content` | string | Yes | Text or URI content |
| `language` | string | No | ISO language code (default: `en`) |

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

A write that cannot be confirmed (verification mismatch after retries) returns an
error response rather than a success — `success: true` means the data is on the
tag. A response with `verified: false` only occurs if verification was explicitly
disabled by the agent.

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

Base URL: `http://localhost:9471/api/v1`

### Health Check

**GET `/api/v1/health`**

```bash
curl http://localhost:9471/api/v1/health
```

Response:

```json
{
  "status": "ok"
}
```

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

| Code | Description |
|------|-------------|
| `WRITE_FAILED` | Write operation failed |
| `NO_CARD` | No card present on reader |
| `READ_FAILED` | Failed to read card data |
| `SESSION_LOCKED` | Another client holds the session |
| `INVALID_REQUEST` | Malformed request |
