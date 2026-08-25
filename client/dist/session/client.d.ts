import type { HealthCheckResponse, LockResponse, NFCClientOptions, NFCEventHandler, NFCEventName, TagCapabilities, TagData, TagTarget, TransceiveRequest, WriteRequest, WriteResponse } from "./types";
/** A refused request, with the agent's code and whether a retry could work. */
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
 * Client for the Davi NFC Agent, over its client endpoint (plain `/ws`).
 *
 * Every tag operation names the tag it applies to, taken from the tag last
 * seen unless the caller names another.
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
    private tag;
    private eventHandlers;
    constructor(serverUrl: string, options?: NFCClientOptions);
    on<E extends NFCEventName>(event: E, handler: NFCEventHandler<E>): void;
    off<E extends NFCEventName>(event: E, handler: NFCEventHandler<E>): void;
    isConnected(): boolean;
    currentTag(): TagData | null;
    connect(): Promise<void>;
    disconnect(): Promise<void>;
    private waitForConnection;
    private attemptReconnect;
    private handleMessage;
    private clearTag;
    private emit;
    /** Replaces whatever the tag holds. */
    write(writeRequest: WriteRequest): Promise<WriteResponse>;
    /** Irreversible. */
    lock(target?: TagTarget): Promise<LockResponse>;
    transceive(request: TransceiveRequest): Promise<Uint8Array>;
    /** Asks the tag, rather than reading what the scan captured. */
    getCapabilities(target?: TagTarget): Promise<TagCapabilities>;
    healthCheck(): Promise<HealthCheckResponse>;
    /** The caller's target when it named one, otherwise the tag in the field. */
    private aimed;
    private nextRequestId;
    private failPending;
    private sendRequest;
}
