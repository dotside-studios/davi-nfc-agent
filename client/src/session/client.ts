import type {
  DeviceStatus,
  HealthCheckResponse,
  LockResponse,
  NFCClientOptions,
  NFCErrorEvent,
  NFCEventHandler,
  NFCEventName,
  NFCEventPayloadMap,
  TagCapabilities,
  TagData,
  TagTarget,
  TransceiveRequest,
  WriteRequest,
  WriteResponse,
} from "./types";
import {
  decodeBase64,
  encodeBase64,
  parseTagData,
  type RawTagPayload,
  type WireMessage,
} from "./protocol";

type EventHandlers = {
  [E in NFCEventName]: Array<NFCEventHandler<E>>;
};

interface PendingRequest {
  resolve: (value: unknown) => void;
  reject: (reason: Error) => void;
}

const DEFAULT_RECONNECT_DELAY = 250;
const DEFAULT_MAX_RECONNECT_DELAY = 5000;
const DEFAULT_MAX_RECONNECT_ATTEMPTS = 10;
const CONNECTION_TIMEOUT_MS = 10_000;
const REQUEST_TIMEOUT_MS = 30_000;

/**
 * A request the agent refused, carrying why.
 *
 * The agent answers a failed operation with an error code from a fixed
 * vocabulary -- `NO_CARD`, `TAG_MISMATCH`, `READ_ONLY`, `CAPACITY_EXCEEDED` and
 * the rest -- and says whether repeating the request could plausibly succeed.
 * Flattening that to a message string is what leaves a caller retrying a write
 * to a locked tag.
 */
export class NFCRequestError extends Error {
  readonly code?: string;
  readonly retryable?: boolean;
  readonly op?: string;
  readonly tagUID?: string;

  constructor(
    message: string,
    detail: {
      code?: string;
      retryable?: boolean;
      op?: string;
      tagUID?: string;
    } = {},
  ) {
    super(message);
    this.name = "NFCRequestError";
    this.code = detail.code;
    this.retryable = detail.retryable;
    this.op = detail.op;
    this.tagUID = detail.tagUID;
  }
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
export class NFCClient {
  readonly serverUrl: string;
  readonly apiSecret: string;
  readonly autoReconnect: boolean;
  readonly reconnectDelay: number;
  readonly maxReconnectDelay: number;
  readonly maxReconnectAttempts: number;

  private ws: WebSocket | null = null;
  private connected = false;
  private reconnectAttempts = 0;
  private intentionalDisconnect = false;

  private pendingRequests: Record<string, PendingRequest> = {};
  private requestIdCounter = 0;

  /**
   * The tag the agent last reported, or null once it left the field. Requests
   * name it unless the caller names another.
   */
  private tag: TagData | null = null;

  private eventHandlers: EventHandlers = {
    tagData: [],
    tagRemoved: [],
    deviceStatus: [],
    connected: [],
    disconnected: [],
    error: [],
  };

  constructor(serverUrl: string, options: NFCClientOptions = {}) {
    this.serverUrl = serverUrl.replace(/\/$/, "");
    this.apiSecret = options.apiSecret ?? "";
    this.autoReconnect = options.autoReconnect !== false;
    this.reconnectDelay = options.reconnectDelay ?? DEFAULT_RECONNECT_DELAY;
    this.maxReconnectDelay =
      options.maxReconnectDelay ?? DEFAULT_MAX_RECONNECT_DELAY;
    this.maxReconnectAttempts =
      options.maxReconnectAttempts ?? DEFAULT_MAX_RECONNECT_ATTEMPTS;
  }

  on<E extends NFCEventName>(event: E, handler: NFCEventHandler<E>): void {
    this.eventHandlers[event].push(handler as never);
  }

  off<E extends NFCEventName>(event: E, handler: NFCEventHandler<E>): void {
    this.eventHandlers[event] = this.eventHandlers[event].filter(
      (h) => h !== handler,
    ) as never;
  }

  isConnected(): boolean {
    return this.connected;
  }

  /** The tag currently in the field, as the agent last reported it. */
  currentTag(): TagData | null {
    return this.tag;
  }

  async connect(): Promise<void> {
    try {
      // Cleared here rather than only in the constructor, so a client that was
      // deliberately disconnected reconnects on its own again afterwards.
      this.intentionalDisconnect = false;

      let wsUrl = this.serverUrl.replace(/^http/, "ws") + "/ws";
      if (this.apiSecret) {
        wsUrl += `?secret=${encodeURIComponent(this.apiSecret)}`;
      }

      const ws = new WebSocket(wsUrl);
      this.ws = ws;

      ws.onopen = () => {
        if (this.ws !== ws) return;
        this.connected = true;
        this.reconnectAttempts = 0;
        this.emit("connected", {});
      };

      ws.onmessage = (event: MessageEvent) => {
        if (this.ws !== ws) return;
        try {
          const message = JSON.parse(event.data as string) as WireMessage;
          this.handleMessage(message);
        } catch (err) {
          console.error("Failed to parse WebSocket message:", err);
        }
      };

      ws.onerror = (error) => {
        if (this.ws !== ws) return;
        this.emit("error", {
          error: error instanceof Error ? error : new Error("WebSocket error"),
          phase: "websocket",
        });
      };

      ws.onclose = () => {
        if (this.ws !== ws) return;
        this.connected = false;
        // A request in flight cannot be answered on a socket that is gone.
        // Left pending it would sit until the 30s timeout with nothing coming.
        this.failPending(new Error("connection closed"));
        this.emit("disconnected", {});
        if (!this.intentionalDisconnect && this.autoReconnect) {
          this.attemptReconnect();
        }
      };

      await this.waitForConnection();
    } catch (err) {
      this.emit("error", {
        error: err instanceof Error ? err : new Error(String(err)),
        phase: "connection",
      });
      throw err;
    }
  }

  async disconnect(): Promise<void> {
    this.intentionalDisconnect = true;
    if (this.connected && this.ws) {
      this.ws.close();
    }
    this.connected = false;
    this.ws = null;
    this.failPending(new Error("connection closed"));
  }

  private waitForConnection(): Promise<void> {
    return new Promise((resolve, reject) => {
      const timeout = setTimeout(() => {
        reject(new Error("Connection timeout"));
      }, CONNECTION_TIMEOUT_MS);

      const checkConnection = () => {
        if (!this.ws) {
          clearTimeout(timeout);
          reject(new Error("Connection closed"));
          return;
        }
        if (this.ws.readyState === WebSocket.OPEN) {
          clearTimeout(timeout);
          resolve();
        } else if (
          this.ws.readyState === WebSocket.CLOSED ||
          this.ws.readyState === WebSocket.CLOSING
        ) {
          clearTimeout(timeout);
          reject(new Error("Connection failed"));
        } else {
          setTimeout(checkConnection, 100);
        }
      };

      checkConnection();
    });
  }

  private attemptReconnect(): void {
    if (
      this.maxReconnectAttempts > 0 &&
      this.reconnectAttempts >= this.maxReconnectAttempts
    ) {
      this.emit("error", {
        error: new Error("Max reconnection attempts reached"),
        phase: "reconnection",
      });
      return;
    }

    // Backs off rather than retrying at a fixed interval: on loopback a drop
    // usually means the agent is rebinding its listeners, which takes about a
    // second, so a short first delay reconnects almost immediately -- while a
    // genuinely absent agent is not polled every few seconds forever.
    const delay = Math.min(
      this.maxReconnectDelay,
      this.reconnectDelay * 2 ** this.reconnectAttempts,
    );
    this.reconnectAttempts++;
    setTimeout(() => {
      this.connect().catch((err) => {
        console.error("Reconnection failed:", err);
      });
    }, delay);
  }

  private handleMessage(message: WireMessage): void {
    const { id, type, payload, success, error } = message;

    if (id && this.pendingRequests[id]) {
      const { resolve, reject } = this.pendingRequests[id];
      delete this.pendingRequests[id];
      if (success) {
        resolve(payload);
      } else {
        reject(new NFCRequestError(error || "Request failed", errorDetail(payload)));
      }
      return;
    }

    switch (type) {
      case "tagData": {
        const tag = parseTagData(payload as RawTagPayload);
        // A tag with no UID is how the agent reports that the one being held
        // has left the field. Passing it on as a scan would show a blank tag
        // over the real one.
        if (!tag.uid) {
          this.clearTag();
          break;
        }
        this.tag = tag;
        this.emit("tagData", tag);
        break;
      }
      case "deviceStatus": {
        const status = (payload ?? {}) as DeviceStatus;
        // deviceStatus describes the agent's own reader and nothing else, so
        // its cardPresent says nothing about a tag a phone is holding -- and is
        // false the whole time one is.
        if (status.cardPresent === false && !this.tag?.deviceID) {
          this.clearTag();
        }
        this.emit("deviceStatus", status);
        break;
      }
      case "error": {
        const detail = errorDetail(payload);
        this.emit("error", {
          error: new NFCRequestError(error || "agent error", detail),
          ...detail,
        } satisfies NFCErrorEvent);
        break;
      }
      default:
        console.warn("Unknown message type:", type);
    }
  }

  private clearTag(): void {
    const previous = this.tag;
    this.tag = null;
    if (previous) this.emit("tagRemoved", { uid: previous.uid });
  }

  private emit<E extends NFCEventName>(event: E, data: NFCEventPayloadMap[E]): void {
    for (const handler of this.eventHandlers[event]) {
      try {
        (handler as NFCEventHandler<E>)(data);
      } catch (err) {
        console.error(`Error in ${event} handler:`, err);
      }
    }
  }

  /**
   * Writes NDEF records to a tag, replacing whatever it holds.
   *
   * Names the tag in the field unless the request names another. Set `lock` to
   * make the tag permanently read-only once the write lands.
   */
  async write(writeRequest: WriteRequest): Promise<WriteResponse> {
    return this.sendRequest<WriteResponse>(
      "writeRequest",
      this.aimed(writeRequest, writeRequest),
    );
  }

  /** Makes a tag permanently read-only. Irreversible. */
  async lock(target?: TagTarget): Promise<LockResponse> {
    return this.sendRequest<LockResponse>("lockRequest", this.aimed({}, target));
  }

  /**
   * Exchanges raw bytes with a tag: an APDU, or a framing-level command when
   * `raw` is set. Resolves with the tag's response.
   */
  async transceive(request: TransceiveRequest): Promise<Uint8Array> {
    const { data, raw, ...target } = request;
    if (data.length === 0) {
      throw new Error("transceive requires a command");
    }
    const response = await this.sendRequest<{ data?: string }>(
      "transceiveRequest",
      this.aimed({ data: encodeBase64(data), raw: raw === true }, target),
    );
    return response.data ? decodeBase64(response.data) : new Uint8Array();
  }

  /**
   * Asks the agent what a tag supports, rather than reading the capabilities
   * captured when it was scanned.
   */
  async getCapabilities(target?: TagTarget): Promise<TagCapabilities> {
    const response = await this.sendRequest<{ capabilities?: TagCapabilities }>(
      "capabilitiesRequest",
      this.aimed({}, target),
    );
    return response.capabilities ?? {};
  }

  async healthCheck(): Promise<HealthCheckResponse> {
    const response = await fetch(`${this.serverUrl}/api/v1/health`);
    return (await response.json()) as HealthCheckResponse;
  }

  /**
   * Names the tag a request applies to: the caller's choice when it made one,
   * otherwise the tag in the field.
   */
  private aimed<P extends object>(
    payload: P,
    target: TagTarget | undefined,
  ): P & TagTarget {
    if (target?.uid || target?.deviceID || target?.allowUntargeted) {
      const { uid, deviceID, allowUntargeted } = target;
      return {
        ...payload,
        ...(uid ? { uid } : {}),
        ...(deviceID ? { deviceID } : {}),
        ...(allowUntargeted ? { allowUntargeted } : {}),
      };
    }

    const tag = this.tag;
    if (!tag) return { ...payload };
    return {
      ...payload,
      uid: tag.uid,
      ...(tag.deviceID ? { deviceID: tag.deviceID } : {}),
    };
  }

  private nextRequestId(): string {
    return `req_${++this.requestIdCounter}_${Date.now()}`;
  }

  private failPending(err: Error): void {
    const pending = this.pendingRequests;
    this.pendingRequests = {};
    for (const id of Object.keys(pending)) {
      pending[id].reject(err);
    }
  }

  private sendRequest<R>(type: string, payload: unknown): Promise<R> {
    if (!this.connected || !this.ws) {
      return Promise.reject(new Error("Not connected to server"));
    }
    const ws = this.ws;
    return new Promise<R>((resolve, reject) => {
      const requestId = this.nextRequestId();

      const timeout = setTimeout(() => {
        delete this.pendingRequests[requestId];
        reject(new Error(`${type} request timeout`));
      }, REQUEST_TIMEOUT_MS);

      this.pendingRequests[requestId] = {
        resolve: (value) => {
          clearTimeout(timeout);
          resolve(value as R);
        },
        reject: (err) => {
          clearTimeout(timeout);
          reject(err);
        },
      };

      ws.send(JSON.stringify({ id: requestId, type, payload }));
    });
  }
}

/** The error taxonomy the agent attaches to a refusal, when it sent one. */
function errorDetail(payload: unknown): {
  code?: string;
  retryable?: boolean;
  op?: string;
  tagUID?: string;
} {
  const detail = payload as
    | { code?: string; retryable?: boolean; op?: string; tagUID?: string }
    | undefined;
  if (!detail || typeof detail !== "object") return {};
  return {
    code: detail.code,
    retryable: detail.retryable,
    op: detail.op,
    tagUID: detail.tagUID,
  };
}
