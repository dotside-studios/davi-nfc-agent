/**
 * TypeScript type definitions for NFC Agent Client
 */

/**
 * Configuration options for NFCClient constructor
 */
export interface NFCClientOptions {
  /**
   * Optional API secret for authentication
   */
  apiSecret?: string;

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

  /**
   * Maximum reconnection attempts (0 = infinite)
   * @default 10
   */
  maxReconnectAttempts?: number;
}

/**
 * NDEF Record in write request
 */
export interface NDEFRecordWrite {
  /**
   * Record type ('text' or 'uri')
   */
  type: 'text' | 'uri';

  /**
   * Text or URI content
   */
  content: string;

  /**
   * Language code for text records
   * @default 'en'
   */
  language?: string;
}

/**
 * NDEF Record in read data (from broadcast/tag data)
 * 
 * Structure is consistent with NDEFRecordWrite for easier client-side handling.
 * Includes additional technical fields for debugging and advanced use cases.
 */
export interface NDEFRecord {
  /**
   * Record type: 'text', 'uri', or other custom type (human-readable)
   */
  type: string;

  /**
   * Decoded content (text or URI)
   */
  content?: string;

  /**
   * Language code for text records (e.g., 'en')
   */
  language?: string;

  /**
   * Type Name Format (technical detail)
   */
  tnf: number;

  /**
   * Record ID (optional)
   */
  id?: string;

  /**
   * Raw payload bytes
   */
  payload: Uint8Array;
}

/**
 * Tag data event payload
 */
export interface TagData {
  /**
   * Card unique identifier (hex string)
   */
  uid: string;

  /**
   * The paired device that scanned this tag, or '' when the agent's own
   * reader did. Pass it back on a write to act on that device's tag.
   */
  deviceID: string;

  /**
   * Card type (e.g., 'MIFARE Classic 1K')
   */
  type: string;

  /**
   * NFC technology standard (e.g., 'ISO14443A')
   */
  technology: string;

  /**
   * Timestamp when card was scanned
   */
  scannedAt: Date | null;

  /**
   * Decoded text from card (convenience field)
   */
  text: string;

  /**
   * Message information (if available)
   */
  message: {
    type: 'ndef' | 'raw';
    records?: NDEFRecord[];
    data?: Uint8Array;
  } | null;

  /**
   * Error message (if any)
   */
  error: string | null;
}

/**
 * Device status event payload
 */
export interface DeviceStatus {
  /**
   * Whether NFC device is connected
   */
  connected: boolean;

  /**
   * Name of the NFC device
   */
  deviceName?: string;

  /**
   * Device message
   */
  message?: string;

  /**
   * Whether a card is currently present
   */
  cardPresent?: boolean;
}

/**
 * Health check response
 */
export interface HealthCheckResponse {
  /**
   * Status ('ok' or 'error')
   */
  status: string;

  /**
   * Timestamp of health check
   */
  timestamp: string;
}

/**
 * Error event payload
 */
export interface ErrorEvent {
  /**
   * Error object
   */
  error: Error;

  /**
   * Error code (if structured error)
   */
  code?: string;

  /**
   * Phase where error occurred (if connection error)
   */
  phase?: 'connection' | 'websocket' | 'reconnection';
}

/**
 * Write request parameters (simplified API)
 * 
 * This API always overwrites the entire NDEF message.
 * To append records, read current data first, modify it, and write back.
 */
export interface WriteRequest {
  /**
   * Array of NDEF records to write
   */
  records: NDEFRecordWrite[];

  /**
   * UID of the tag to write, from the tagData this write responds to. The write
   * is refused unless the tag about to be encoded carries it. Left unset, the
   * client fills it in from the last tag it saw.
   */
  uid?: string;

  /**
   * The paired device holding the tag, from the same tagData. Optional: the
   * UID alone identifies the tag.
   */
  deviceID?: string;

  /**
   * Guess which tag this write means when neither `uid` nor `deviceID` is
   * given, instead of refusing it.
   */
  allowUntargeted?: boolean;

  /**
   * Make the tag permanently read-only after a successful write. Irreversible.
   */
  lock?: boolean;

  /**
   * Identifies the logical write, so a retry after a lost response is not
   * applied twice.
   */
  idempotencyKey?: string;
}

/**
 * Write response payload
 */
export interface WriteResponse {
  /**
   * Success message
   */
  message: string;
}

/**
 * Event handler function types
 */
export type TagDataHandler = (data: TagData) => void;
export type DeviceStatusHandler = (status: DeviceStatus) => void;
export type ConnectedHandler = () => void;
export type DisconnectedHandler = () => void;
export type ErrorHandler = (error: ErrorEvent) => void;

/**
 * Event name types
 */
export type EventName = 'tagData' | 'deviceStatus' | 'connected' | 'disconnected' | 'error';

/**
 * Event handler type map
 */
export interface EventHandlerMap {
  tagData: TagDataHandler;
  deviceStatus: DeviceStatusHandler;
  connected: ConnectedHandler;
  disconnected: DisconnectedHandler;
  error: ErrorHandler;
}

/**
 * NFC Agent Client
 *
 * A framework-agnostic JavaScript client for connecting to the NFC Agent server.
 * Supports session management, WebSocket communication, and NFC card operations.
 *
 * Connects to the agent's client endpoint (plain /ws) on the shared agent port
 * (default 9470). NFC readers/devices use NFCDeviceClient (/ws?mode=device) on
 * the same port.
 *
 * @example
 * ```typescript
 * const client = new NFCClient('http://localhost:9470');
 *
 * client.on('tagData', (data) => {
 *   console.log('Card detected:', data.uid, data.text);
 * });
 *
 * client.on('deviceStatus', (status) => {
 *   console.log('Device connected:', status.connected);
 * });
 *
 * await client.connect();
 * ```
 */
export class NFCClient {
  /**
   * Creates a new NFC client instance
   *
   * @param serverUrl - Base URL of the NFC Agent server (e.g., 'http://localhost:9470')
   * @param options - Configuration options
   *
   * @example
   * ```typescript
   * const client = new NFCClient('http://localhost:9470', {
   *   apiSecret: 'my-secret-key',
   *   autoReconnect: true,
   *   reconnectDelay: 5000,
   *   maxReconnectAttempts: 5
   * });
   * ```
   */
  constructor(serverUrl: string, options?: NFCClientOptions);

  /**
   * Registers an event handler
   *
   * @param event - Event name
   * @param handler - Callback function
   *
   * @example
   * ```typescript
   * client.on('tagData', (data) => {
   *   console.log('Card UID:', data.uid);
   * });
   *
   * client.on('connected', () => {
   *   console.log('Connected to NFC Agent');
   * });
   * ```
   */
  on<K extends EventName>(event: K, handler: EventHandlerMap[K]): void;

  /**
   * Removes an event handler
   *
   * @param event - Event name
   * @param handler - Callback function to remove
   *
   * @example
   * ```typescript
   * const handler = (data) => console.log(data);
   * client.on('tagData', handler);
   * client.off('tagData', handler);
   * ```
   */
  off<K extends EventName>(event: K, handler: EventHandlerMap[K]): void;

  /**
   * Establishes WebSocket connection to the server
   *
   * Directly connects to WebSocket. First connection wins (session lock).
   * Throws an error if connection fails or session is already claimed.
   *
   * @returns Promise that resolves when connection is established
   * @throws {Error} If connection fails or session already claimed
   *
   * @example
   * ```typescript
   * try {
   *   await client.connect();
   *   console.log('Connected successfully');
   * } catch (err) {
   *   console.error('Connection failed:', err);
   * }
   * ```
   */
  connect(): Promise<void>;

  /**
   * Disconnects from the server
   *
   * Closes the WebSocket connection and releases the session automatically.
   *
   * @returns Promise that resolves when disconnection is complete
   *
   * @example
   * ```typescript
   * await client.disconnect();
   * console.log('Disconnected');
   * ```
   */
  disconnect(): Promise<void>;

  /**
   * Writes NDEF data to an NFC card (complete overwrite)
   *
   * Simplified API: Always overwrites the entire NDEF message.
   * To append records, read the current data first, modify it, and write back.
   *
   * @param writeRequest - Write request parameters
   * @returns Promise that resolves with response payload when write is complete
   * @throws {Error} If not connected or write fails
   *
   * @example
   * ```typescript
   * // Write single text record
   * await client.write({
   *   records: [{ type: 'text', content: 'Hello, NFC!' }]
   * });
   *
   * // Write multiple records
   * await client.write({
   *   records: [
   *     { type: 'text', content: 'Hello, NFC!' },
   *     { type: 'uri', content: 'https://example.com' }
   *   ]
   * });
   *
   * // Append records (read first, then write)
   * // Note: Record structure is consistent between read and write!
   * const lastTag = await client.getLastTag();
   * const existingRecords = lastTag.message.records.map(r => ({
   *   type: r.type,
   *   content: r.content,
   *   language: r.language
   * }));
   * await client.write({
   *   records: [...existingRecords, { type: 'text', content: 'New record' }]
   * });
   * ```
   */
  write(writeRequest: WriteRequest): Promise<WriteResponse>;

  /**
   * Gets the current connection status
   *
   * @returns True if connected to the server
   *
   * @example
   * ```typescript
   * if (client.isConnected()) {
   *   console.log('Client is connected');
   * }
   * ```
   */
  isConnected(): boolean;

  /**
   * Performs a health check
   *
   * @returns Promise that resolves with health check result
   *
   * @example
   * ```typescript
   * const health = await client.healthCheck();
   * console.log('Server status:', health.status);
   * ```
   */
  healthCheck(): Promise<HealthCheckResponse>;
}

export default NFCClient;
