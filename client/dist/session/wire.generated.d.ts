/**
 * The error codes the agent sends. Whether a retry can work is `retryable` on
 * the error itself, not a property of the code.
 */
export type NFCErrorCode = "PARSE_ERROR" | "INVALID_PAYLOAD" | "INVALID_REQUEST" | "INVALID_MESSAGE_TYPE" | "UNKNOWN_TYPE" | "INVALID_DEVICE" | "REGISTRATION_FAILED" | "TAG_SEND_FAILED" | "READ_ERROR" | "LOCK_FAILED" | "CAPABILITIES_FAILED" | "SESSION_LOCKED" | "NO_CARD"
/**
 * Reports that the tag resolved is not the one named. Re-read the tag rather
 * than retrying.
 */
 | "TAG_MISMATCH"
/**
 * Reports that a request named no tag and did not ask for one to be guessed.
 */
 | "TAG_NOT_NAMED"
/**
 * Reports that the raw APDU channel is switched off. It is a policy
 * decision, distinct from read-only mode: the operator has not opened the
 * channel that carries raw exchanges, so no raw command reaches a tag until
 * they do. Not retryable; the channel has to be enabled.
 */
 | "RAW_CHANNEL_DISABLED" | "TIMEOUT" | "DEVICE_GONE" | "INTERNAL_ERROR" | "UNKNOWN_ERROR" | "NOT_SUPPORTED" | "TAG_REMOVED" | "AUTH_FAILED" | "READ_FAILED" | "WRITE_FAILED" | "TRANSCEIVE_FAILED" | "TAG_NOT_CONNECTED" | "READ_ONLY" | "CAPACITY_EXCEEDED" | "INVALID_DATA"
/**
 * Reports more than one tag in the field where the operation needs exactly
 * one. Not retryable: the user has to separate them first.
 */
 | "MULTIPLE_TAGS"
/**
 * Reports that the agent could not start the operation because it is still
 * working on an earlier one: a reader whose previous operation was abandoned
 * but has not finished, or a connection with more requests outstanding than
 * it can queue. Retryable once the earlier work drains.
 */
 | "BUSY"
/**
 * Reports that the tag was reached but holds no NDEF message. Not retryable:
 * the tag is intact and the next read returns the same answer.
 */
 | "NO_PAYLOAD";
/**
 * The messages the client protocol carries. A type not listed here is answered
 * with UNKNOWN_TYPE.
 */
export type NFCMessageType = "tagData" | "deviceStatus" | "writeRequest" | "writeResponse" | "lockRequest" | "lockResponse" | "capabilitiesRequest" | "capabilitiesResponse" | "transceiveRequest" | "transceiveResponse" | "error";
/**
 * ErrorPayload is the payload of an error response. `code` carries the same
 * strings as before the taxonomy existed; everything else is additive, so a
 * client that only reads `code` is unaffected.
 */
export interface ErrorPayload {
    code: NFCErrorCode;
    retryable: boolean;
    op?: string;
    tagUID?: string;
}
/** NDEFMessageInput represents an NDEF message for input. */
export interface NDEFMessageInput {
    records: NDEFRecordInput[];
}
/**
 * NDEFRecordInput represents a single NDEF record for input. Supports both
 * high-level (type+content) and low-level (TNF+payload) formats.
 */
export interface NDEFRecordInput {
    /** High-level format (preferred for simple records) */
    recordType?: string;
    content?: string;
    language?: string;
    mimeType?: string;
    /** Low-level format (for advanced use cases) */
    tnf?: number;
    type?: string;
    id?: string;
    payload?: string;
}
/**
 * TagMessagePayload is what was read off a tag, inside a tagData broadcast.
 * Two shapes share it: an NDEF message carries Records, and anything else
 * carries Data, the bytes as they were read.
 */
export interface TagMessagePayload {
    /** Type is "ndef" or "raw", and says which of the two below is set. */
    type: string;
    records?: NDEFRecordPayload[];
    data?: string;
}
/**
 * TagDataPayload is the payload of a tagData broadcast reporting a tag in
 * the field. A tag leaving is reported as [TagRemovedPayload] on the same
 * message type.
 */
export interface TagDataPayload {
    uid: string;
    type: string;
    technology: string;
    /** ScannedAt is when the tag was read, RFC3339. */
    scannedAt: string;
    /**
     * Capabilities is what can be done to this tag. A client checks it rather
     * than inferring from Type: a tag a phone holds is writable only while that
     * device is connected and said so.
     */
    capabilities: TagCapabilities;
    /**
     * DeviceID names the device that scanned it. Absent for the agent's own
     * reader, which is the only source deviceStatus describes.
     */
    deviceID?: string;
    /** Message is what the tag holds, absent when it could not be read. */
    message?: TagMessagePayload;
    /** Text is the first text record, for a client that wants nothing else. */
    text: string;
    /**
     * Err is what went wrong reading the tag, null when nothing did. Always
     * present: a client reads it to tell a failed scan from a clean one.
     */
    err: string | null;
}
/**
 * TagRemovedPayload is the payload of a tagData broadcast reporting that no
 * tag is in the field. An empty UID is how a client recognises it. It also
 * carries a scan that failed before any tag was read, in Err.
 */
export interface TagRemovedPayload {
    uid: string;
    text: string;
    err: string | null;
}
/**
 * WriteResponsePayload answers a writeRequest that landed, with what reached
 * the tag: whether the agent read the data back and it matched, how many
 * attempts it took, and whether the tag ended up locked.
 */
export interface WriteResponsePayload {
    message: string;
    uid: string;
    tagType: string;
    bytesWritten: number;
    verified: boolean;
    attempts: number;
    locked: boolean;
}
/**
 * WriteAcknowledgement answers a writeRequest that landed and reported no
 * result: the write succeeded and there is nothing to say about the tag.
 * Only a build supplying its own tag operations produces it.
 */
export interface WriteAcknowledgement {
    message: string;
}
/**
 * TransceiveResponsePayload answers a transceiveRequest with the tag's
 * reply, base64 as raw bytes are in both directions.
 */
export interface TransceiveResponsePayload {
    data: string;
}
/**
 * TransceiveRequestPayload is a raw exchange with the tag it names.
 *
 * It embeds [TagTarget] for the naming, so IdempotencyKey parses here as it
 * does elsewhere, but a raw exchange is not deduplicated: a retry is a
 * second exchange with the tag.
 */
export interface TransceiveRequestPayload extends TagTarget {
    /** Data is the command, base64. */
    data: string;
    /**
     * Raw exchanges at the framing level rather than wrapping the bytes as an
     * APDU. A framing-level reply carries no ISO 7816 status word.
     */
    raw: boolean;
}
/**
 * HealthPayload is the body of /health and /api/v1/health, which is what a
 * client library polls to find the agent.
 */
export interface HealthPayload {
    status: string;
    /**
     * Type is always "agent", telling it apart from anything else on the port.
     */
    type: string;
    /** Timestamp is when the agent answered, RFC3339. */
    timestamp: string;
    /** Clients is how many are connected right now. */
    clients: number;
}
/**
 * WebSocketMessage is the generic message envelope for WebSocket
 * communication.
 */
export interface WebSocketMessage {
    id?: string;
    type: NFCMessageType;
    payload: unknown;
}
/** WebSocketRequest is for incoming requests from WebSocket clients. */
export interface WebSocketRequest {
    id?: string;
    type: NFCMessageType;
    payload?: Record<string, unknown>;
}
/** WebSocketResponse is for responses to WebSocket requests. */
export interface WebSocketResponse {
    id?: string;
    type: NFCMessageType;
    success: boolean;
    payload?: unknown;
    error?: string;
}
/**
 * TagTarget names the tag an operation applies to. Every tag request carries
 * it, so a request reaches the tag the client meant rather than whichever
 * one is in the field when it arrives.
 */
export interface TagTarget {
    /**
     * DeviceID names the device holding the tag. Empty means the tag is found by
     * UID instead. Clients learn the value from a tagData broadcast.
     */
    deviceID?: string;
    /**
     * UID names the tag itself. The operation is refused unless the tag it
     * reaches carries it.
     */
    uid?: string;
    /**
     * AllowUntargeted opts into the agent guessing which tag was meant when
     * neither UID nor DeviceID is given, instead of refusing the request.
     */
    allowUntargeted?: boolean;
    /**
     * IdempotencyKey identifies the logical operation. A client that retries
     * after a lost response reuses it, so the operation is not applied twice.
     * Read on writes and locks; a raw exchange cannot be deduplicated.
     */
    idempotencyKey?: string;
}
/**
 * WriteRecord is a single NDEF record in a write request. The Type field
 * selects how the remaining fields are interpreted.
 */
export interface WriteRecord {
    /**
     * Type selects the record kind. Supported values:
     *   "text"                              - Content (+ optional Language)
     *   "uri" / "url"                       - Content
     *   "mailto"/"email", "tel", "sms", "geo" - Content (scheme prepended if absent)
     *   "smartposter"                       - Content (URI) + optional Title/Language
     *   "mime"                              - MimeType + Payload (or Content)
     *   "vcard"                             - Content or Payload (vCard data)
     *   "external"                          - Content (domain:type) + optional Payload
     *   "aar"                               - Content (Android package name)
     *   "raw"                               - TNF + TypeBytes + optional ID + Payload
     * Empty Type defaults to "text".
     */
    type: string;
    /**
     * Content carries the primary value: text, URI, domain, package name, etc.
     */
    content?: string;
    /** Language is the ISO language code for text records (default: "en"). */
    language?: string;
    /** MimeType is the media type for "mime" records. */
    mimeType?: string;
    /** Title is the optional display title for "smartposter" records. */
    title?: string;
    /**
     * Payload holds raw bytes for "mime", "vcard", "external", and "raw" records
     * (base64-encoded in JSON).
     */
    payload?: string;
    /** TNF, TypeBytes, and ID are used only for "raw" records. */
    tnf?: number;
    typeBytes?: string;
    id?: string;
}
/**
 * WriteRequestPayload is the payload of a writeRequest: the complete NDEF
 * message to encode onto the tag it names.
 *
 * The API overwrites rather than appends. A client that wants to add a
 * record reads the tag, modifies what it read, and sends the whole message
 * back.
 */
export interface WriteRequestPayload extends TagTarget {
    /** Records is the NDEF message to write, in order. */
    records: WriteRecord[];
    /**
     * Lock, when true, makes the tag permanently read-only after a successful
     * write. Only tags that support locking honor this. WARNING: irreversible.
     */
    lock?: boolean;
}
/**
 * TagCapabilities describes what operations a tag supports.
 *
 * It is defined here, with the tags it describes, and aliased as
 * protocol.TagCapabilities for the two protocols that carry it. The json
 * tags are part of that wire format: renaming one renames a field for every
 * client.
 */
export interface TagCapabilities {
    /** Core capabilities */
    canRead: boolean;
    canWrite: boolean;
    canTransceive: boolean;
    /**
     * ReadsAreSnapshot reports that reading this tag returns what was captured
     * when it was scanned, not what is on it now.
     *
     * A write to such a tag cannot be confirmed by reading it back: the read
     * answers with data that a write cannot have changed, so the comparison says
     * nothing. The zero value is the common case, a tag read live over its own
     * connection; a tag whose contents arrive from elsewhere declares this.
     */
    readsAreSnapshot?: boolean;
    /** Locking capabilities */
    canLock: boolean;
    isReadOnly?: boolean;
    /** Memory info */
    memorySize?: number;
    maxNdefSize?: number;
    /** Technology info */
    technology?: string;
    tagFamily?: string;
    /** Optional features */
    supportsNdef: boolean;
    supportsCrypto?: boolean;
    supportsAuthentication?: boolean;
    /**
     * SupportsPassword indicates the tag supports simple password protection
     * (e.g. NTAG PWD/PACK/AUTH0). This is distinct from SupportsAuthentication,
     * which covers crypto-based mutual authentication (DESFire, Ultralight C).
     */
    supportsPassword?: boolean;
}
/**
 * Represents the status of the NFC device. This type might be used by the
 * main application to display status.
 *
 * Marshalled directly as the payload of a deviceStatus message, so the json
 * tags are the wire field names.
 */
export interface DeviceStatusPayload {
    /** Device names the reader this describes. */
    device?: string;
    connected: boolean;
    message: string;
    cardPresent: boolean;
}
/**
 * NDEFRecordPayload represents an NDEF record in JSON-friendly format. This
 * structure is used for serialization to WebSocket clients and API
 * responses.
 */
export interface NDEFRecordPayload {
    type: string;
    content?: string;
    language?: string;
    tnf: number;
    id?: string;
    payload: string;
}
/** NDEFMessagePayload represents an NDEF message in JSON-friendly format. */
export interface NDEFMessagePayload {
    type: string;
    records: NDEFRecordPayload[];
}
/** Describes the outcome of a make-read-only (lock) operation. */
export interface LockResponsePayload {
    /** UID of the tag that was locked. */
    uid: string;
    /** TagType is the human-readable tag type string. */
    tagType: string;
    /** Locked is true when the tag was made permanently read-only. */
    locked: boolean;
}
