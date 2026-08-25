import type { HealthCheckResponse, LockResponse, NFCClientOptions, NFCEventHandler, NFCEventName, TagCapabilities, TagData, TagTarget, TransceiveRequest, WriteRequest, WriteResponse } from "./types";
/**
 * A request the agent refused, carrying why.
 *
 * The agent answers a failed operation with an error code from a fixed
 * vocabulary -- `NO_CARD`, `TAG_MISMATCH`, `READ_ONLY`, `CAPACITY_EXCEEDED` and
 * the rest -- and says whether repeating the request could plausibly succeed.
 * Flattening that to a message string is what leaves a caller retrying a write
 * to a locked tag.
 */
export declare class NFCRequestError extends Error {
    readonly code?: string;
    readonly retryable?: boolean;
    readonly op?: string;
    readonly tagUID?: string;
    constructor(message: string, detail?: {
        code?: string;
        retryable?: boolean;
        op?: string;
        tagUID?: string;
    });
}
/**
 * Framework-agnostic client for the Davi NFC Agent.
 *
 * Connects to the agent's client endpoint (plain `/ws`) on the shared agent
 * port (default 9470). NFC readers/devices use the device endpoint
 * (`/ws?mode=device`) on the same port.
 *
 * Every tag operation names the tag it applies to. The client remembers the
 * tag it last saw and names that one, so a caller acting on what is in front of
 * the operator does not have to thread a UID through itself; pass a
 * `TagTarget` to act on a different one, or `allowUntargeted` to let the agent
 * guess.
 *
 * @example
 * const client = new NFCClient("https://localhost:9470");
 */
export declare class NFCClient {
    readonly serverUrl: string;
    readonly apiSecret: string;
    readonly autoReconnect: boolean;
    readonly reconnectDelay: number;
    readonly maxReconnectDelay: number;
    readonly maxReconnectAttempts: number;
    private ws;
    private connected;
    private reconnectAttempts;
    private intentionalDisconnect;
    private pendingRequests;
    private requestIdCounter;
    /**
     * The tag the agent last reported, or null once it left the field. Requests
     * name it unless the caller names another.
     */
    private tag;
    private eventHandlers;
    constructor(serverUrl: string, options?: NFCClientOptions);
    on<E extends NFCEventName>(event: E, handler: NFCEventHandler<E>): void;
    off<E extends NFCEventName>(event: E, handler: NFCEventHandler<E>): void;
    isConnected(): boolean;
    /** The tag currently in the field, as the agent last reported it. */
    currentTag(): TagData | null;
    connect(): Promise<void>;
    disconnect(): Promise<void>;
    private waitForConnection;
    private attemptReconnect;
    private handleMessage;
    private clearTag;
    private emit;
    /**
     * Writes NDEF records to a tag, replacing whatever it holds.
     *
     * Names the tag in the field unless the request names another. Set `lock` to
     * make the tag permanently read-only once the write lands.
     */
    write(writeRequest: WriteRequest): Promise<WriteResponse>;
    /** Makes a tag permanently read-only. Irreversible. */
    lock(target?: TagTarget): Promise<LockResponse>;
    /**
     * Exchanges raw bytes with a tag: an APDU, or a framing-level command when
     * `raw` is set. Resolves with the tag's response.
     */
    transceive(request: TransceiveRequest): Promise<Uint8Array>;
    /**
     * Asks the agent what a tag supports, rather than reading the capabilities
     * captured when it was scanned.
     */
    getCapabilities(target?: TagTarget): Promise<TagCapabilities>;
    healthCheck(): Promise<HealthCheckResponse>;
    /**
     * Names the tag a request applies to: the caller's choice when it made one,
     * otherwise the tag in the field.
     */
    private aimed;
    private nextRequestId;
    private failPending;
    private sendRequest;
}
