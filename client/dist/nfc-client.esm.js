var __defProp = Object.defineProperty;
var __defNormalProp = (obj, key, value) => key in obj ? __defProp(obj, key, { enumerable: true, configurable: true, writable: true, value }) : obj[key] = value;
var __publicField = (obj, key, value) => __defNormalProp(obj, typeof key !== "symbol" ? key + "" : key, value);

// src/session/protocol.ts
function parseTagData(payload) {
  const tagData = {
    uid: payload.uid || "",
    type: payload.type || "",
    technology: payload.technology || "",
    scannedAt: payload.scannedAt ? new Date(payload.scannedAt) : null,
    text: payload.text || "",
    message: payload.message || null,
    error: payload.err || null,
    capabilities: payload.capabilities,
    deviceID: payload.deviceID || void 0,
    _raw: payload
  };
  if (tagData.message && tagData.message.type === "ndef") {
    tagData.ndefRecords = tagData.message.records || [];
  }
  return tagData;
}
function encodeBase64(bytes) {
  let binary = "";
  for (const b of bytes) binary += String.fromCharCode(b);
  return btoa(binary);
}
function decodeBase64(value) {
  const binary = atob(value);
  const out = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) out[i] = binary.charCodeAt(i);
  return out;
}

// src/session/client.ts
var DEFAULT_RECONNECT_DELAY = 250;
var DEFAULT_MAX_RECONNECT_DELAY = 5e3;
var DEFAULT_MAX_RECONNECT_ATTEMPTS = 10;
var CONNECTION_TIMEOUT_MS = 1e4;
var REQUEST_TIMEOUT_MS = 3e4;
var NFCRequestError = class extends Error {
  constructor(message, detail = {}) {
    super(message);
    __publicField(this, "code");
    __publicField(this, "retryable");
    __publicField(this, "op");
    __publicField(this, "tagUID");
    this.name = "NFCRequestError";
    this.code = detail.code;
    this.retryable = detail.retryable;
    this.op = detail.op;
    this.tagUID = detail.tagUID;
  }
};
var NFCClient = class {
  constructor(serverUrl, options = {}) {
    __publicField(this, "serverUrl");
    __publicField(this, "apiSecret");
    __publicField(this, "autoReconnect");
    __publicField(this, "reconnectDelay");
    __publicField(this, "maxReconnectDelay");
    __publicField(this, "maxReconnectAttempts");
    __publicField(this, "ws", null);
    __publicField(this, "connected", false);
    __publicField(this, "reconnectAttempts", 0);
    __publicField(this, "intentionalDisconnect", false);
    __publicField(this, "pendingRequests", {});
    __publicField(this, "requestIdCounter", 0);
    __publicField(this, "tag", null);
    __publicField(this, "eventHandlers", {
      tagData: [],
      tagRemoved: [],
      deviceStatus: [],
      connected: [],
      disconnected: [],
      error: []
    });
    this.serverUrl = serverUrl.replace(/\/$/, "");
    this.apiSecret = options.apiSecret ?? "";
    this.autoReconnect = options.autoReconnect !== false;
    this.reconnectDelay = options.reconnectDelay ?? DEFAULT_RECONNECT_DELAY;
    this.maxReconnectDelay = options.maxReconnectDelay ?? DEFAULT_MAX_RECONNECT_DELAY;
    this.maxReconnectAttempts = options.maxReconnectAttempts ?? DEFAULT_MAX_RECONNECT_ATTEMPTS;
  }
  on(event, handler) {
    this.eventHandlers[event].push(handler);
  }
  off(event, handler) {
    this.eventHandlers[event] = this.eventHandlers[event].filter(
      (h) => h !== handler
    );
  }
  isConnected() {
    return this.connected;
  }
  currentTag() {
    return this.tag;
  }
  async connect() {
    try {
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
      ws.onmessage = (event) => {
        if (this.ws !== ws) return;
        try {
          const message = JSON.parse(event.data);
          this.handleMessage(message);
        } catch (err) {
          console.error("Failed to parse WebSocket message:", err);
        }
      };
      ws.onerror = (error) => {
        if (this.ws !== ws) return;
        this.emit("error", {
          error: error instanceof Error ? error : new Error("WebSocket error"),
          phase: "websocket"
        });
      };
      ws.onclose = () => {
        if (this.ws !== ws) return;
        this.connected = false;
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
        phase: "connection"
      });
      throw err;
    }
  }
  async disconnect() {
    this.intentionalDisconnect = true;
    if (this.connected && this.ws) {
      this.ws.close();
    }
    this.connected = false;
    this.ws = null;
    this.failPending(new Error("connection closed"));
  }
  waitForConnection() {
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
        } else if (this.ws.readyState === WebSocket.CLOSED || this.ws.readyState === WebSocket.CLOSING) {
          clearTimeout(timeout);
          reject(new Error("Connection failed"));
        } else {
          setTimeout(checkConnection, 100);
        }
      };
      checkConnection();
    });
  }
  attemptReconnect() {
    if (this.maxReconnectAttempts > 0 && this.reconnectAttempts >= this.maxReconnectAttempts) {
      this.emit("error", {
        error: new Error("Max reconnection attempts reached"),
        phase: "reconnection"
      });
      return;
    }
    const delay = Math.min(
      this.maxReconnectDelay,
      this.reconnectDelay * 2 ** this.reconnectAttempts
    );
    this.reconnectAttempts++;
    setTimeout(() => {
      this.connect().catch((err) => {
        console.error("Reconnection failed:", err);
      });
    }, delay);
  }
  handleMessage(message) {
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
        const tag = parseTagData(payload);
        if (!tag.uid) {
          this.clearTag();
          break;
        }
        this.tag = tag;
        this.emit("tagData", tag);
        break;
      }
      case "deviceStatus": {
        const status = payload ?? {};
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
          ...detail
        });
        break;
      }
      default:
        console.warn("Unknown message type:", type);
    }
  }
  clearTag() {
    const previous = this.tag;
    this.tag = null;
    if (previous) this.emit("tagRemoved", { uid: previous.uid });
  }
  emit(event, data) {
    for (const handler of this.eventHandlers[event]) {
      try {
        handler(data);
      } catch (err) {
        console.error(`Error in ${event} handler:`, err);
      }
    }
  }
  /** Replaces whatever the tag holds. */
  async write(writeRequest) {
    return this.sendRequest(
      "writeRequest",
      this.aimed(writeRequest, writeRequest)
    );
  }
  /** Irreversible. */
  async lock(target) {
    return this.sendRequest("lockRequest", this.aimed({}, target));
  }
  async transceive(request) {
    const { data, raw, ...target } = request;
    if (data.length === 0) {
      throw new Error("transceive requires a command");
    }
    const response = await this.sendRequest(
      "transceiveRequest",
      this.aimed({ data: encodeBase64(data), raw: raw === true }, target)
    );
    return response.data ? decodeBase64(response.data) : new Uint8Array();
  }
  /** Asks the tag, rather than reading what the scan captured. */
  async getCapabilities(target) {
    const response = await this.sendRequest(
      "capabilitiesRequest",
      this.aimed({}, target)
    );
    return response.capabilities ?? {};
  }
  async healthCheck() {
    const response = await fetch(`${this.serverUrl}/api/v1/health`);
    return await response.json();
  }
  /** The caller's target when it named one, otherwise the tag in the field. */
  aimed(payload, target) {
    if (target?.uid || target?.deviceID || target?.allowUntargeted) {
      const { uid, deviceID, allowUntargeted } = target;
      return {
        ...payload,
        ...uid ? { uid } : {},
        ...deviceID ? { deviceID } : {},
        ...allowUntargeted ? { allowUntargeted } : {}
      };
    }
    const tag = this.tag;
    if (!tag) return { ...payload };
    return {
      ...payload,
      uid: tag.uid,
      ...tag.deviceID ? { deviceID: tag.deviceID } : {}
    };
  }
  nextRequestId() {
    return `req_${++this.requestIdCounter}_${Date.now()}`;
  }
  failPending(err) {
    const pending = this.pendingRequests;
    this.pendingRequests = {};
    for (const id of Object.keys(pending)) {
      pending[id].reject(err);
    }
  }
  sendRequest(type, payload) {
    if (!this.connected || !this.ws) {
      return Promise.reject(new Error("Not connected to server"));
    }
    const ws = this.ws;
    return new Promise((resolve, reject) => {
      const requestId = this.nextRequestId();
      const timeout = setTimeout(() => {
        delete this.pendingRequests[requestId];
        reject(new Error(`${type} request timeout`));
      }, REQUEST_TIMEOUT_MS);
      this.pendingRequests[requestId] = {
        resolve: (value) => {
          clearTimeout(timeout);
          resolve(value);
        },
        reject: (err) => {
          clearTimeout(timeout);
          reject(err);
        }
      };
      ws.send(JSON.stringify({ id: requestId, type, payload }));
    });
  }
};
function errorDetail(payload) {
  const detail = payload;
  if (!detail || typeof detail !== "object") return {};
  return {
    code: detail.code,
    retryable: detail.retryable,
    op: detail.op,
    tagUID: detail.tagUID
  };
}

// src/session/diagnose.ts
var PROBE_TIMEOUT_MS = 4e3;
async function probe(url) {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), PROBE_TIMEOUT_MS);
  try {
    return await fetch(url, { signal: controller.signal });
  } catch {
    return null;
  } finally {
    clearTimeout(timer);
  }
}
async function diagnoseAgent(serverUrl) {
  const base = serverUrl.replace(/\/$/, "");
  const health = await probe(`${base}/api/v1/health`);
  if (health?.ok) {
    return {
      kind: "origin-blocked",
      title: "The agent is running but refused this page",
      detail: `The NFC agent answered at ${base}, so it is running and trusted. It rejected the connection from this site, which means this site is not on its allowed-origins list. Allow ${location.host} on the agent.`
    };
  }
  if (health) {
    return {
      kind: "origin-blocked",
      title: "The agent refused this page",
      detail: `The NFC agent answered with ${health.status}. If that is 403, this site is not on its allowed-origins list: allow ${location.host} on the agent.`
    };
  }
  if (base.startsWith("https://")) {
    const plain = await probe(`${base.replace(/^https:/, "http:")}/api/v1/health`);
    if (plain?.ok) {
      return {
        kind: "wrong-scheme",
        title: "The agent is running without encryption",
        detail: `This site is configured for ${base}, but the agent is answering over http on the same port. Point this site at the http address, or start the agent with TLS.`
      };
    }
  }
  return {
    kind: "unreachable",
    title: "Can't reach the NFC agent",
    detail: "The agent runs on this computer. Either it isn't running, or this browser doesn't trust its certificate yet, which looks identical from here. Open it directly to find out: a certificate warning means it's running and its certificate needs trusting, and a connection error means it isn't running.",
    openUrl: base
  };
}
export {
  NFCClient,
  NFCRequestError,
  decodeBase64,
  diagnoseAgent,
  encodeBase64,
  parseTagData
};
