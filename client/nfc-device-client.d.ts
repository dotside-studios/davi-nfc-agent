/**
 * TypeScript type definitions for NFC Device Client
 *
 * Pair before connecting: the agent's QR carries the address, its public key
 * pin and the PIN as `davi-pair://<host>:9470/?spki=...&code=123456&name=...`.
 * Pin the TLS connection to `spki`, POST to
 * `https://<host>:9470/pair?pin=<code>`, and store the `deviceToken` from the
 * response. Pairing is served from the agent's port, not the cleartext
 * bootstrap listener, and is refused with 426 over cleartext from anything but
 * loopback.
 *
 * Present the token, or the shared API secret, as `?secret=` on the server URL
 * or as an `Authorization: Bearer` header. Loopback needs one too, unless the
 * agent runs with `-allow-loopback-bypass`.
 *
 * The device endpoint caps an inbound frame at 256 KB. Revoking a device's
 * credential closes its session with a policy-violation close (1008). Both
 * arrive here as `disconnected`.
 */

/**
 * WebSocket constructor type
 */
export type WebSocketConstructor = new (url: string) => WebSocket;

/**
 * Configuration options for NFCDeviceClient constructor
 */
export interface NFCDeviceClientOptions {
  /**
   * Custom WebSocket class. Required in Node.js, optional in browser.
   * In Node.js, pass the 'ws' package: `WebSocket: require('ws')`
   */
  WebSocket?: WebSocketConstructor;

  /**
   * Device name for registration
   * @default 'NFC Device'
   */
  deviceName?: string;

  /**
   * Platform identifier (e.g., 'web', 'ios', 'android', 'node')
   * @default 'unknown'
   */
  platform?: string;

  /**
   * Application version
   * @default '1.0.0'
   */
  appVersion?: string;

  /**
   * Device can read NFC tags
   * @default true
   */
  canRead?: boolean;

  /**
   * Device can write NFC tags
   * @default false
   */
  canWrite?: boolean;

  /**
   * NFC library type (e.g., 'webnfc', 'react-native-nfc', 'custom')
   * @default 'custom'
   */
  nfcType?: string;

  /**
   * Device supports APDU-level exchange (IsoDep.transceive, sendCommand,
   * PN532 InDataExchange). Sent only when the agent speaks protocol v1.
   * @default false
   */
  canTransceive?: boolean;

  /**
   * Device supports framing-level exchange (NfcA.transceive,
   * PN532 InCommunicateThru).
   * @default false
   */
  canTransceiveRaw?: boolean;

  /**
   * Device can make a tag read-only
   * @default false
   */
  canLock?: boolean;

  /**
   * Kind of device, e.g. 'smartphone', 'pn532-serial'
   * @default 'smartphone'
   */
  deviceType?: string;

  /**
   * Tag families this device handles, e.g. ['MIFARE Classic', 'NTAG']
   */
  supportedTagTypes?: string[];

  /**
   * Maximum baud rate in bps, for serial-attached readers
   */
  maxBaudRate?: number;

  /**
   * Automatically send heartbeats
   * @default true
   */
  autoHeartbeat?: boolean;

  /**
   * Heartbeat interval in milliseconds
   * @default 30000
   */
  heartbeatInterval?: number;

  /**
   * Automatically reconnect on disconnect
   * @default true
   */
  autoReconnect?: boolean;

  /**
   * Delay in milliseconds before reconnecting
   * @default 3000
   */
  reconnectDelay?: number;
}

/**
 * Server information received during registration
 */
export interface ServerInfo {
  /**
   * Server version
   */
  version: string;

  /**
   * Supported NFC types
   */
  supportedNFC: string[];
}

/**
 * Registration event payload
 */
export interface RegisteredEvent {
  /**
   * Assigned device ID
   */
  deviceID: string;

  /**
   * Server information
   */
  serverInfo: ServerInfo;

  /**
   * Negotiated bridge protocol version. 0 means the agent predates versioning.
   */
  protocolVersion: number;
}

/**
 * Connection event payload
 */
export interface DeviceConnectedEvent {
  /**
   * Bridge protocol version implied by the negotiated WebSocket subprotocol.
   */
  protocolVersion: number;
}

/**
 * NDEF record in protocol format
 */
export interface NDEFRecordProtocol {
  /**
   * Type Name Format
   */
  typeNameFormat?: string;

  /**
   * Record type
   */
  type: string;

  /**
   * Record ID
   */
  id?: string;

  /**
   * Text content (for text records)
   */
  text?: string;

  /**
   * Language code (for text records)
   */
  language?: string;

  /**
   * URI (for URI records)
   */
  uri?: string;

  /**
   * Raw data (base64 encoded)
   */
  rawData?: string;
}

/**
 * NDEF message in protocol format
 */
export interface NDEFMessageProtocol {
  /**
   * Message type
   */
  type: 'ndef' | 'raw';

  /**
   * NDEF records
   */
  records: NDEFRecordProtocol[];
}

/**
 * Write request received from server
 */
export interface WriteRequestEvent {
  /**
   * Unique request ID for correlation
   */
  requestID: string;

  /**
   * Target device ID
   */
  deviceID: string;

  /**
   * NDEF message to write, as records. Provided for APIs like Web NFC that
   * only accept records; prefer `ndefBytes` where the device can write raw.
   *
   * Absent, along with `ndefBytes`, when `lock` is set on its own: that is a
   * lock-only request, and the tag must be locked as it stands.
   */
  ndefMessage: NDEFMessageProtocol | null;

  /**
   * The same message already encoded, base64 in transit. Authoritative where
   * it and `ndefMessage` disagree.
   */
  ndefBytes?: string;

  /**
   * UID of the tag the agent expects to be in the field
   */
  tagUID?: string;

  /**
   * Make the tag permanently read-only after a successful write. Set without
   * any message, this is a lock-only request: the agent's `lockRequest`
   * travels as a write frame, since the protocol has one tag-modifying frame
   * rather than two.
   */
  lock?: boolean;

  /**
   * Identifies the logical write. If this key was already applied, report the
   * previous outcome instead of writing again, since the same request can arrive
   * twice when a response is lost to a dropped connection.
   */
  idempotencyKey?: string;
}

/**
 * Raw exchange requested by the agent
 */
export interface TransceiveRequestEvent {
  /**
   * Unique request ID for correlation
   */
  requestID: string;

  /**
   * Target device ID
   */
  deviceID: string;

  /**
   * UID of the tag the agent expects to be in the field
   */
  tagUID?: string;

  /**
   * Command bytes to send to the tag, base64 in transit
   */
  data: string;

  /**
   * Framing-level exchange (NfcA.transceive) rather than APDU-level
   * (IsoDep.transceive)
   */
  raw: boolean;

  /**
   * Bound for this single exchange, in milliseconds
   */
  timeoutMs?: number;
}

/**
 * Tag data for scan events
 */
export interface DeviceTagData {
  /**
   * Tag UID (hex format, e.g., '04:AB:CD:EF:12:34:56')
   */
  uid: string;

  /**
   * NFC technology (e.g., 'ISO14443A', 'ISO14443B')
   * @default 'ISO14443A'
   */
  technology?: string;

  /**
   * Tag type (e.g., 'MIFARE Classic 1K', 'NTAG215')
   * @default 'Unknown'
   */
  type?: string;

  /**
   * Answer to Reset (if applicable)
   */
  atr?: string;

  /**
   * Timestamp of scan (ISO format)
   */
  scannedAt?: string;

  /**
   * NDEF message data
   */
  ndefMessage?: NDEFMessageProtocol | null;

  /**
   * Raw tag data (base64 encoded)
   */
  rawData?: string | null;

  /**
   * What this tag supports, if the device knows. Omitted, the agent infers it
   * from `type`. Requires protocol v1.
   */
  capabilities?: TagCapabilities;
}

/**
 * Capabilities of a scanned tag
 */
export interface TagCapabilities {
  canRead?: boolean;
  canWrite?: boolean;
  canTransceive?: boolean;
  canLock?: boolean;
  isReadOnly?: boolean;

  /** Total memory in bytes */
  memorySize?: number;

  /** Maximum NDEF message size in bytes */
  maxNdefSize?: number;

  /** e.g. 'ISO14443A' */
  technology?: string;

  /** e.g. 'MIFARE Classic', 'NTAG' */
  tagFamily?: string;

  supportsNdef?: boolean;
  supportsCrypto?: boolean;
  supportsAuthentication?: boolean;

  /** Simple password protection (NTAG PWD/PACK/AUTH0) */
  supportsPassword?: boolean;
}

/**
 * Error event payload
 */
export interface DeviceErrorEvent {
  /**
   * Error object
   */
  error: Error;

  /**
   * Error code (if structured error)
   */
  code?: string;

  /**
   * Whether repeating the request could plausibly succeed. False means the
   * request was refused on its merits (malformed input, an unsupported
   * operation, a locked tag) and resending it only wastes a round trip.
   */
  retryable?: boolean;

  /**
   * Operation that failed, e.g. 'WriteData'
   */
  op?: string;

  /**
   * Tag involved in the failure, when there is one
   */
  tagUID?: string;

  /**
   * Phase where error occurred
   */
  phase?: 'connection' | 'websocket' | 'registration' | 'reconnection';
}

/**
 * Event handler function types
 */
export type RegisteredHandler = (event: RegisteredEvent) => void;
export type WriteRequestHandler = (event: WriteRequestEvent) => void;
export type TransceiveRequestHandler = (event: TransceiveRequestEvent) => void;
export type DeviceConnectedHandler = (event: DeviceConnectedEvent) => void;
export type DeviceDisconnectedHandler = () => void;
export type DeviceErrorHandler = (error: DeviceErrorEvent) => void;

/**
 * Event name types
 */
export type DeviceEventName = 'registered' | 'writeRequest' | 'transceiveRequest' | 'connected' | 'disconnected' | 'error';

/**
 * Event handler type map
 */
export interface DeviceEventHandlerMap {
  registered: RegisteredHandler;
  transceiveRequest: TransceiveRequestHandler;
  writeRequest: WriteRequestHandler;
  connected: DeviceConnectedHandler;
  disconnected: DeviceDisconnectedHandler;
  error: DeviceErrorHandler;
}

/**
 * NFC Device Client
 *
 * A universal JavaScript client for connecting to the Davi NFC Agent as a device.
 * Works in both Node.js and browser environments. NFC source agnostic - integrate
 * with any NFC library (WebNFC, React Native NFC, etc.) by calling scanTag().
 *
 * Connects to the agent's device endpoint (/ws?mode=device) on the shared agent
 * port (default 9470); web clients use NFCClient (plain /ws) on the same port.
 *
 * @example Browser
 * ```typescript
 * const client = new NFCDeviceClient('ws://localhost:9470', {
 *   deviceName: 'My NFC Device',
 *   platform: 'web'
 * });
 * await client.connect();
 * ```
 *
 * @example Node.js (pass your own WebSocket class)
 * ```typescript
 * import WebSocket from 'ws';
 * const client = new NFCDeviceClient('ws://localhost:9470', {
 *   WebSocket: WebSocket as any,
 *   deviceName: 'My NFC Device',
 *   platform: 'node'
 * });
 * await client.connect();
 * ```
 *
 * @example Sending tag data
 * ```typescript
 * client.on('registered', ({ deviceID }) => {
 *   console.log('Registered as device:', deviceID);
 * });
 *
 * // When your NFC library detects a tag, call scanTag()
 * await client.scanTag({
 *   uid: '04:AB:CD:EF:12:34:56',
 *   type: 'MIFARE Classic 1K'
 * });
 * ```
 */
export class NFCDeviceClient {
  /**
   * Creates a new NFC Device client instance
   *
   * @param serverUrl - Base URL of the NFC Agent (e.g., 'ws://localhost:9470')
   * @param options - Configuration options
   */
  constructor(serverUrl: string, options?: NFCDeviceClientOptions);

  /**
   * Registers an event handler
   *
   * @param event - Event name
   * @param handler - Callback function
   */
  on<K extends DeviceEventName>(event: K, handler: DeviceEventHandlerMap[K]): void;

  /**
   * Removes an event handler
   *
   * @param event - Event name
   * @param handler - Callback function to remove
   */
  off<K extends DeviceEventName>(event: K, handler: DeviceEventHandlerMap[K]): void;

  /**
   * Establishes WebSocket connection and registers as a device
   *
   * @returns Promise that resolves when connected and registered
   * @throws {Error} If connection or registration fails
   */
  connect(): Promise<void>;

  /**
   * Disconnect from the server. On protocol v1 this sends a `goodbye` first so
   * the agent reports a deliberate departure rather than a lost device.
   * @param reason Optional explanation recorded in the agent's logs
   */
  disconnect(reason?: string): Promise<void>;

  /**
   * Send a tag scan event to the server. Call this when your NFC library detects a tag.
   *
   * @param tagData - Tag data to send
   * @returns Promise that resolves when sent
   * @throws {Error} If not connected or registered
   */
  scanTag(tagData: DeviceTagData): Promise<void>;

  /**
   * Send a tag removed event to the server. Call this when a tag leaves the reader.
   *
   * @param uid - UID of the removed tag
   * @returns Promise that resolves when sent
   * @throws {Error} If not connected or registered
   */
  removeTag(uid: string): Promise<void>;

  /**
   * Respond to a write request from the server
   *
   * @param requestID - The request ID from the write request
   * @param success - Whether the write was successful
   * @param error - Error message if unsuccessful
   * @param errorCode - Wire error code (e.g. 'READ_ONLY', 'CAPACITY_EXCEEDED',
   *   'TAG_REMOVED') so the agent can classify the failure
   */
  respondToWrite(requestID: string, success: boolean, error?: string, errorCode?: string): Promise<void>;

  /**
   * Respond to a transceive request from the server
   *
   * @param requestID - The request ID from the transceive request
   * @param success - Whether the exchange succeeded
   * @param data - Base64 response bytes from the tag
   * @param error - Error message if unsuccessful
   * @param errorCode - Wire error code (e.g. 'TAG_REMOVED')
   */
  respondToTransceive(requestID: string, success: boolean, data?: string, error?: string, errorCode?: string): Promise<void>;

  /**
   * Get the assigned device ID
   * @returns Device ID or null if not registered
   */
  getDeviceID(): string | null;

  /**
   * Get server info received during registration
   * @returns Server info or null if not registered
   */
  getServerInfo(): ServerInfo | null;

  /**
   * Check if connected and registered with the server
   * @returns True if connected and registered
   */
  isConnected(): boolean;
}

export default NFCDeviceClient;
