# Errors reference

How the agent reports a refused request, and every code it sends.

## Shape

A refused request on either endpoint is answered with `type: "error"`:

```json
{
  "id": "req_1",
  "type": "error",
  "success": false,
  "error": "data too large: 900 bytes exceeds tag NDEF capacity of 504 bytes",
  "payload": { "code": "CAPACITY_EXCEEDED", "retryable": false }
}
```

| Field | Description |
|-------|-------------|
| `id` | The request's `id`, when it had one |
| `error` | A message for a person. Not stable; branch on `code` |
| `payload.code` | Stable string, from the tables below |
| `payload.retryable` | Whether sending the identical request again could succeed |
| `payload.op`, `payload.tagUID` | The operation and tag concerned. Sent on some errors only, so do not rely on them |

Treat a code you do not recognise as not retryable. A newer agent may send
codes this page does not list.

## Deciding what to do

| `retryable` | `code` | Meaning | Do |
|-------------|--------|---------|----|
| `true` | `TAG_REMOVED`, `NO_CARD` | The tag is not in the field | Ask for the tag, then send the same request |
| `true` | anything else | Transient: an I/O fault, a busy reader, a timeout | Retry with backoff |
| `false` | | Refused on its merits | Show it; retrying changes nothing |

## Codes

### The request

| Code | Retryable | Meaning |
|------|-----------|---------|
| `PARSE_ERROR` | no | The message was not valid JSON |
| `INVALID_PAYLOAD` | no | The payload does not fit the message type. Device endpoint |
| `INVALID_REQUEST` | no | A field is missing or invalid, or an irreversible NTAG 424 operation lacks `confirm: true` |
| `INVALID_MESSAGE_TYPE` | no | The message is not valid at this point, such as a device sending anything before `hello` |
| `UNKNOWN_TYPE` | no | The agent does not know this message type |
| `INVALID_DEVICE` | no | A device message named a `deviceID` other than the connection's own |
| `REGISTRATION_FAILED` | no | The device could not be registered |
| `TAG_NOT_NAMED` | no | The request named no tag. See [Naming the tag](client-api.md#naming-the-tag) |
| `TAG_MISMATCH` | no | A different tag is present from the one named. Read the tag again |
| `NO_CARD` | yes | Nothing is holding the tag named |
| `BUSY` | yes | Earlier work has not finished: a reader completing an operation its caller gave up on, a raw session holding the reader, or more requests queued than the connection allows |
| `TIMEOUT` | yes | An NTAG 424 operation on a phone would not finish in time. Nothing was written |
| `INTERNAL_ERROR` | yes | The agent is stopping, or failed unexpectedly |
| `TAG_SEND_FAILED` | yes | A device's scan could not be passed on. Device endpoint |
| `READ_ERROR` | yes | The agent could not read from the device connection |
| `UNKNOWN_ERROR` | no | Unclassified |

### The tag

| Code | Retryable | Meaning |
|------|-----------|---------|
| `NOT_SUPPORTED` | no | The tag, reader or device cannot do this |
| `READ_ONLY` | no | The tag is locked, or the agent is in read-only mode |
| `CAPACITY_EXCEEDED` | no | The message is larger than the tag's usable NDEF capacity |
| `NO_PAYLOAD` | no | The tag holds no NDEF message. See [Identity-only scans](client-api.md#identity-only-scans) |
| `MULTIPLE_TAGS` | no | More than one tag is in the field. Separate them |
| `INVALID_DATA` | no | Data was malformed |
| `AUTH_FAILED` | no | Authentication failed or no key is held. The same key fails again |
| `RAW_CHANNEL_DISABLED` | no | The raw APDU channel is closed. See [Configuration](configuration.md#flags) |
| `RAW_SESSION_EXPIRED` | no | The raw session ended. Begin a new one |
| `TAG_REMOVED` | yes | The tag left the field during the operation |
| `TAG_NOT_CONNECTED` | yes | The device holding the tag disconnected during the operation |
| `WRITE_FAILED` | yes | The write failed, including a device that did not answer within 20 seconds |
| `LOCK_FAILED` | no | The lock failed for a reason with no more specific code |
| `TRANSCEIVE_FAILED` | yes | The exchange failed, or the card answered an NTAG 424 operation with an unexpected status |
| `CAPABILITIES_FAILED` | no | The capabilities could not be read for a reason with no more specific code |
| `READ_FAILED` | yes | A device reported that reading failed |
