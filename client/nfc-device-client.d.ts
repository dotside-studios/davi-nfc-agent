/**
 * TypeScript type definitions for NFC Device Client
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
   * NDEF message to write
   */
  ndefMessage: NDEFMessageProtocol | null;
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
   * Phase where error occurred
   */
  phase?: 'connection' | 'websocket' | 'registration' | 'reconnection';
}

/**
 * Event handler function types
 */
export type RegisteredHandler = (event: RegisteredEvent) => void;
export type WriteRequestHandler = (event: WriteRequestEvent) => void;
export type DeviceConnectedHandler = () => void;
export type DeviceDisconnectedHandler = () => void;
export type DeviceErrorHandler = (error: DeviceErrorEvent) => void;

/**
 * Event name types
 */
export type DeviceEventName = 'registered' | 'writeRequest' | 'connected' | 'disconnected' | 'error';

/**
 * Event handler type map
 */
export interface DeviceEventHandlerMap {
  registered: RegisteredHandler;
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
   * Disconnect from the server
   */
  disconnect(): Promise<void>;

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
   */
  respondToWrite(requestID: string, success: boolean, error?: string): Promise<void>;

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
