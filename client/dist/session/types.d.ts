export interface NFCClientOptions {
    /**
     * Sent as `?secret=` on the upgrade. Required from loopback too, unless the
     * agent runs with `-allow-loopback-bypass`. A missing or wrong secret fails
     * the upgrade with 401, which surfaces as a failed connection, not a close.
     */
    apiSecret?: string;
    autoReconnect?: boolean;
    /** First retry delay. Later attempts double it, up to `maxReconnectDelay`. */
    reconnectDelay?: number;
    maxReconnectDelay?: number;
    /** 0 retries forever. */
    maxReconnectAttempts?: number;
}
/**
 * Which tag an operation applies to. `NFCClient` fills these in from the tag it
 * last saw, so a caller acting on the tag in front of the operator names it
 * without doing anything.
 */
export interface TagTarget {
    uid?: string;
    /** The paired device holding it. Absent means the agent's own reader. */
    deviceID?: string;
    /**
     * Opts into the agent guessing which tag was meant. Per request rather than
     * per agent, so one caller that cannot name its tag does not weaken the
     * guarantee for the others.
     */
    allowUntargeted?: boolean;
}
/** A record to write. `type` selects which of the other fields are read. */
export interface WriteRecord {
    /** "text", "uri", "smartposter", "mime", "raw". See the reference. */
    type: string;
    content?: string;
    language?: string;
    mimeType?: string;
    title?: string;
    /** Base64. */
    payload?: string;
    tnf?: number;
    /** Base64. */
    typeBytes?: string;
    /** Base64. */
    id?: string;
}
/** Deprecated alias for `WriteRecord`. */
export type NDEFRecordWrite = WriteRecord;
export interface NDEFRecord {
    type: string;
    /** The decoded text or URI, for every kind the agent can decode. */
    content?: string;
    language?: string;
    tnf: number;
    id?: string;
    /** The undecoded payload, base64. `decodeBase64` gives the bytes. */
    payload: string;
}
export interface TagMessage {
    type: "ndef" | "raw";
    records?: NDEFRecord[];
    /** Contents that are not NDEF, base64. */
    data?: string;
}
/**
 * What a scanned tag supports. An undefined field means the agent did not say,
 * which is not the same as "cannot".
 */
export interface TagCapabilities {
    canRead?: boolean;
    canWrite?: boolean;
    /** Can be true for a tag a paired device holds, if that device declares it. */
    canTransceive?: boolean;
    canLock?: boolean;
    isReadOnly?: boolean;
    /**
     * Reading this tag returns what was captured when it was scanned, so a write
     * to it cannot be confirmed by reading it back.
     */
    readsAreSnapshot?: boolean;
    memorySize?: number;
    /** Largest NDEF message this tag can hold. */
    maxNdefSize?: number;
    technology?: string;
    /** e.g. "MIFARE Classic", "NTAG". */
    tagFamily?: string;
    supportsNdef?: boolean;
    supportsCrypto?: boolean;
    supportsAuthentication?: boolean;
    /** NTAG PWD/PACK/AUTH0, as distinct from `supportsAuthentication`. */
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
     * Check `canWrite` before offering a write rather than inferring from `type`:
     * a tag held by a phone is writable only while that device is connected and
     * declared the capability.
     */
    capabilities?: TagCapabilities;
    /** The paired device that scanned it. Absent for the agent's own reader. */
    deviceID?: string;
    _raw: unknown;
}
export interface DeviceStatus {
    connected: boolean;
    /** The reader this describes. Absent on agents that do not send it. */
    device?: string;
    message?: string;
    /**
     * Whether the agent's own reader holds a card. It says nothing about a tag a
     * paired device is holding, and is false the whole time one is.
     */
    cardPresent?: boolean;
}
/**
 * The error codes the agent sends, as of this release. Whether a retry can
 * work is `retryable` on the error, not a property of the code here.
 */
export type NFCErrorCode = "PARSE_ERROR" | "INVALID_PAYLOAD" | "INVALID_REQUEST" | "INVALID_MESSAGE_TYPE" | "UNKNOWN_TYPE" | "INVALID_DEVICE" | "REGISTRATION_FAILED" | "TAG_SEND_FAILED" | "READ_ERROR" | "LOCK_FAILED" | "CAPABILITIES_FAILED" | "SESSION_LOCKED" | "NO_CARD" | "TAG_MISMATCH" | "TAG_NOT_NAMED" | "TIMEOUT" | "DEVICE_GONE" | "INTERNAL_ERROR" | "UNKNOWN_ERROR" | "NOT_SUPPORTED" | "TAG_REMOVED" | "AUTH_FAILED" | "READ_FAILED" | "WRITE_FAILED" | "TRANSCEIVE_FAILED" | "TAG_NOT_CONNECTED" | "READ_ONLY" | "CAPACITY_EXCEEDED" | "INVALID_DATA"
/** More than one tag in the field. Separate them and try again. */
 | "MULTIPLE_TAGS"
/**
 * Earlier work has not finished: a reader completing an operation its caller
 * abandoned, or more requests outstanding than the connection queues.
 */
 | "BUSY";
/**
 * What `code` is declared as: a known code, or any string, so a code added by a
 * newer agent is carried rather than rejected. Switch on `NFCErrorCode` to have
 * the compiler check the arms.
 */
export type NFCErrorCodeValue = NFCErrorCode | (string & {});
export interface NFCErrorEvent {
    error: Error;
    code?: NFCErrorCodeValue;
    /** Whether repeating the identical request could plausibly succeed. */
    retryable?: boolean;
    op?: string;
    tagUID?: string;
    phase?: "connection" | "websocket" | "reconnection";
}
export interface WriteRequest extends TagTarget {
    records: WriteRecord[];
    /** Make the tag permanently read-only once the write lands. Irreversible. */
    lock?: boolean;
    /**
     * Identifies the logical write. A caller retrying after a lost response
     * should reuse it, so the write is not applied twice.
     */
    idempotencyKey?: string;
}
export interface WriteResponse {
    message: string;
    uid?: string;
    tagType?: string;
    bytesWritten?: number;
    /** The agent read the data back and it matched. */
    verified?: boolean;
    /** Attempts taken, including retries on transient faults. */
    attempts?: number;
    locked?: boolean;
}
export interface LockResponse {
    /** Not sent by the agent; a lock answers with the result alone. */
    message?: string;
    uid?: string;
    tagType?: string;
    locked?: boolean;
}
/** No `idempotencyKey`: the agent reads one on writes and locks only. */
export interface TransceiveRequest extends TagTarget {
    data: Uint8Array;
    /**
     * Exchange at the framing level rather than wrapping the bytes as an APDU. A
     * framing-level response carries no ISO 7816 status word.
     */
    raw?: boolean;
}
export interface HealthCheckResponse {
    status: string;
    timestamp: string;
}
export type NFCEventName = "tagData" | "tagRemoved" | "deviceStatus" | "connected" | "disconnected" | "error";
export interface NFCEventPayloadMap {
    tagData: TagData;
    /** The UID is the tag that went away, not an empty one. */
    tagRemoved: {
        uid: string;
    };
    deviceStatus: DeviceStatus;
    connected: Record<string, never>;
    disconnected: Record<string, never>;
    error: NFCErrorEvent;
}
export type NFCEventHandler<E extends NFCEventName> = (payload: NFCEventPayloadMap[E]) => void;
