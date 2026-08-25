export interface NFCClientOptions {
  apiSecret?: string;
  autoReconnect?: boolean;
  /**
   * Delay before the first reconnection attempt. Subsequent attempts back off
   * exponentially from here, capped at `maxReconnectDelay`.
   */
  reconnectDelay?: number;
  /** Ceiling for the backoff, so a long outage still retries at a steady pace. */
  maxReconnectDelay?: number;
  maxReconnectAttempts?: number;
}

/**
 * Which tag an operation applies to.
 *
 * The agent refuses a request that names no tag, because routing by preference
 * -- the local reader while it reports a card, otherwise whichever device
 * scanned last -- let a card lifted between the scan and the request redirect
 * the operation to a different tag. `NFCClient` fills these in from the tag it
 * last saw, so a caller acting on the tag in front of the operator names it
 * without doing anything.
 */
export interface TagTarget {
  /** UID of the tag the request applies to. */
  uid?: string;
  /** The paired device holding it. Absent means the agent's own reader. */
  deviceID?: string;
  /**
   * Opts into the agent guessing which tag was meant when neither `uid` nor
   * `deviceID` is known. Per request rather than per agent, so one caller that
   * cannot name its tag does not weaken the guarantee for the others.
   */
  allowUntargeted?: boolean;
}

/**
 * A record to write, mirroring the record kinds `docs/api.md` documents. `type`
 * selects which of the remaining fields are read.
 */
export interface WriteRecord {
  /**
   * "text" (the default), "uri" / "url", "mailto" / "email", "tel", "sms",
   * "geo", "smartposter", "mime", "vcard", "external", "aar", "empty", or
   * "raw".
   */
  type: string;
  /** The primary value: text, URI, domain, package name. */
  content?: string;
  /** ISO language code for text and smart poster records. Defaults to "en". */
  language?: string;
  /** Media type for "mime" records. */
  mimeType?: string;
  /** Display title for "smartposter" records. */
  title?: string;
  /** Raw bytes for "mime", "vcard", "external" and "raw" records, base64. */
  payload?: string;
  /** "raw" records only. */
  tnf?: number;
  /** "raw" records only, base64. */
  typeBytes?: string;
  /** "raw" records only, base64. */
  id?: string;
}

/**
 * The two record kinds the client shipped before the rest were exposed. Every
 * value it allows is still valid, so existing callers are unaffected.
 */
export type NDEFRecordWrite = WriteRecord;

/**
 * A record the agent read off a tag. `content` carries the decoded text or URI
 * for every kind the agent can decode, which is why there is one field and not
 * one per type.
 */
export interface NDEFRecord {
  type: string;
  content?: string;
  language?: string;
  tnf: number;
  id?: string;
  /** The undecoded record payload, base64. Use `decodeBase64` for the bytes. */
  payload: string;
}

export interface TagMessage {
  type: "ndef" | "raw";
  records?: NDEFRecord[];
  /** A tag whose contents are not NDEF, base64. */
  data?: string;
}

/**
 * What a scanned tag supports.
 *
 * The field names are the agent's wire format -- `nfc.TagCapabilities` in
 * `davi-nfc-agent` -- and not a restatement of them. Absent from agents older
 * than the capability wire.
 */
export interface TagCapabilities {
  canRead?: boolean;
  canWrite?: boolean;
  canTransceive?: boolean;
  canLock?: boolean;
  isReadOnly?: boolean;
  /**
   * Reading this tag returns what was captured when it was scanned, not what
   * is on it now — so a write to it cannot be confirmed by reading it back.
   */
  readsAreSnapshot?: boolean;
  /** Total memory in bytes */
  memorySize?: number;
  /** Largest NDEF message this tag can hold */
  maxNdefSize?: number;
  technology?: string;
  /** e.g. "MIFARE Classic", "NTAG" */
  tagFamily?: string;
  supportsNdef?: boolean;
  supportsCrypto?: boolean;
  supportsAuthentication?: boolean;
  /** Simple password protection (NTAG PWD/PACK/AUTH0) */
  supportsPassword?: boolean;
}

export interface TagData {
  uid: string;
  type: string;
  technology: string;
  scannedAt: Date | null;
  text: string;
  message: TagMessage | null;
  error: string | null;
  ndefRecords?: NDEFRecord[];
  /**
   * What this tag supports. Check `canWrite` before offering a write rather
   * than inferring from `type` — a tag held by a phone is writable only while
   * that device is connected and declared the capability.
   */
  capabilities?: TagCapabilities;
  /**
   * The paired device that scanned it. Absent for the agent's own reader,
   * which is the only source `deviceStatus` describes.
   */
  deviceID?: string;
  _raw: unknown;
}

export interface DeviceStatus {
  connected: boolean;
  deviceName?: string;
  message?: string;
  /**
   * Whether the agent's own reader is holding a card. It says nothing about a
   * tag a paired device is holding, and is false the whole time one is.
   */
  cardPresent?: boolean;
}

export interface NFCErrorEvent {
  error: Error;
  code?: string;
  /**
   * Whether repeating the identical request could plausibly succeed. False
   * means it was refused on its merits — a locked tag, data too large,
   * malformed input — and retrying only wastes a round trip.
   *
   * Undefined from agents predating the error taxonomy.
   */
  retryable?: boolean;
  /** Operation that failed, e.g. "WriteData" */
  op?: string;
  /** Tag involved in the failure, when there is one */
  tagUID?: string;
  phase?: "connection" | "websocket" | "reconnection";
}

export interface WriteRequest extends TagTarget {
  records: WriteRecord[];
  /**
   * Makes the tag permanently read-only once the write lands. Only tags
   * reporting `canLock` honour it. Irreversible.
   */
  lock?: boolean;
  /**
   * Identifies the logical write. A caller retrying after a lost response
   * should reuse it, so the write is not applied twice.
   */
  idempotencyKey?: string;
}

export interface WriteResponse {
  message: string;
  /** UID of the tag actually written */
  uid?: string;
  tagType?: string;
  bytesWritten?: number;
  /** The agent read the data back and it matched */
  verified?: boolean;
  /** How many attempts the write took, including retries on transient faults */
  attempts?: number;
  /** The tag was made permanently read-only as part of this write */
  locked?: boolean;
}

export interface LockResponse {
  message: string;
  uid?: string;
  tagType?: string;
  locked?: boolean;
}

export interface TransceiveRequest extends TagTarget {
  /** The command to send. */
  data: Uint8Array;
  /**
   * Exchange at the framing level rather than wrapping the bytes as an APDU.
   * A framing-level response carries no ISO 7816 status word.
   */
  raw?: boolean;
}

export interface HealthCheckResponse {
  status: string;
  timestamp: string;
}

export type NFCEventName =
  | "tagData"
  | "tagRemoved"
  | "deviceStatus"
  | "connected"
  | "disconnected"
  | "error";

export interface NFCEventPayloadMap {
  tagData: TagData;
  /**
   * The tag left the field. The agent reports this as a `tagData` broadcast
   * with no UID; the UID here is the one the client was holding, so a consumer
   * can tell which tag went away rather than receiving a blank one.
   */
  tagRemoved: { uid: string };
  deviceStatus: DeviceStatus;
  connected: Record<string, never>;
  disconnected: Record<string, never>;
  error: NFCErrorEvent;
}

export type NFCEventHandler<E extends NFCEventName> = (
  payload: NFCEventPayloadMap[E],
) => void;
