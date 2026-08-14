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
 * const client = new NFCDeviceClient('ws://localhost:9470', {
 *   deviceName: 'My NFC Device',
 *   platform: 'web'
 * });
 *
 * @example Node.js
 * const WebSocket = require('ws');
 * const client = new NFCDeviceClient('ws://localhost:9470', {
 *   deviceName: 'My NFC Device',
 *   platform: 'node',
 *   WebSocket: WebSocket
 * });
 *
 * @example Usage
 * client.on('registered', ({ deviceID }) => {
 *   console.log('Registered as device:', deviceID);
 * });
 *
 * client.on('writeRequest', ({ requestID, ndefMessage }) => {
 *   // Handle write request from your NFC library
 *   client.respondToWrite(requestID, true);
 * });
 *
 * await client.connect();
 *
 * // When your NFC library detects a tag, call scanTag()
 * await client.scanTag({
 *   uid: '04:AB:CD:EF:12:34:56',
 *   type: 'MIFARE Classic 1K',
 *   ndefMessage: { records: [...] }
 * });
 */

/**
 * WebSocket subprotocol offered during the upgrade. An agent that echoes it back
 * supports the hello handshake; one that does not is treated as protocol v0.
 */
const DEVICE_SUBPROTOCOL_V1 = 'davi-nfc-device.v1';

class NFCDeviceClient {
  /**
   * Creates a new NFC Device client instance
   * @param {string} serverUrl - Base URL of the NFC Agent (e.g., 'ws://localhost:9470')
   * @param {Object} options - Configuration options
   * @param {Function} [options.WebSocket] - Custom WebSocket class (required in Node.js, optional in browser)
   * @param {string} [options.deviceName='NFC Device'] - Device name for registration
   * @param {string} [options.platform='unknown'] - Platform identifier (e.g., 'web', 'ios', 'android', 'node')
   * @param {string} [options.appVersion='1.0.0'] - Application version
   * @param {boolean} [options.canRead=true] - Device can read NFC tags
   * @param {boolean} [options.canWrite=false] - Device can write NFC tags
   * @param {string} [options.nfcType='custom'] - NFC library type (e.g., 'webnfc', 'react-native-nfc', 'custom')
   * @param {boolean} [options.autoHeartbeat=true] - Automatically send heartbeats
   * @param {number} [options.heartbeatInterval=30000] - Heartbeat interval in ms
   * @param {boolean} [options.autoReconnect=true] - Automatically reconnect on disconnect
   * @param {number} [options.reconnectDelay=3000] - Delay in ms before reconnecting
   */
  constructor(serverUrl, options = {}) {
    this.serverUrl = serverUrl.replace(/\/$/, '');
    this._WebSocketClass = options.WebSocket || (typeof WebSocket !== 'undefined' ? WebSocket : null);
    this.deviceName = options.deviceName || 'NFC Device';
    this.platform = options.platform || 'unknown';
    this.appVersion = options.appVersion || '1.0.0';
    this.canRead = options.canRead !== false;
    this.canWrite = options.canWrite || false;
    this.nfcType = options.nfcType || 'custom';
    this.autoHeartbeat = options.autoHeartbeat !== false;
    this.heartbeatInterval = options.heartbeatInterval || 30000;
    this.autoReconnect = options.autoReconnect !== false;
    this.reconnectDelay = options.reconnectDelay || 3000;

    this.ws = null;
    this.deviceID = null;
    this.serverInfo = null;
    this.protocolVersion = 0;
    this.connected = false;
    this.intentionalDisconnect = false;
    this.reconnectAttempts = 0;
    this.maxReconnectAttempts = 10;

    // Heartbeat timer
    this._heartbeatTimer = null;

    // Request tracking
    this._pendingRequests = {};
    this._requestIdCounter = 0;

    // Event handlers
    this.eventHandlers = {
      registered: [],
      writeRequest: [],
      connected: [],
      disconnected: [],
      error: []
    };
  }

  /**
   * Registers an event handler
   * @param {string} event - Event name: 'registered', 'writeRequest', 'connected', 'disconnected', 'error'
   * @param {Function} handler - Callback function
   */
  on(event, handler) {
    if (this.eventHandlers[event]) {
      this.eventHandlers[event].push(handler);
    } else {
      console.warn(`Unknown event type: ${event}`);
    }
  }

  /**
   * Removes an event handler
   * @param {string} event - Event name
   * @param {Function} handler - Callback function to remove
   */
  off(event, handler) {
    if (this.eventHandlers[event]) {
      this.eventHandlers[event] = this.eventHandlers[event].filter(h => h !== handler);
    }
  }

  /**
   * Emits an event to all registered handlers
   * @private
   */
  _emit(event, data) {
    if (this.eventHandlers[event]) {
      this.eventHandlers[event].forEach(handler => {
        try {
          handler(data);
        } catch (err) {
          console.error(`Error in ${event} handler:`, err);
        }
      });
    }
  }

  /**
   * Gets the WebSocket class
   * @private
   */
  _getWebSocketClass() {
    if (!this._WebSocketClass) {
      throw new Error('No WebSocket class available. In Node.js, pass a WebSocket class via options.WebSocket');
    }
    return this._WebSocketClass;
  }

  /**
   * Establishes WebSocket connection and registers as a device
   * @returns {Promise<void>}
   */
  async connect() {
    try {
      const WebSocketClass = this._getWebSocketClass();

      // Build WebSocket URL with device mode
      let wsUrl = this.serverUrl.replace(/^http/, 'ws');
      if (!wsUrl.includes('/ws')) {
        wsUrl += '/ws';
      }
      wsUrl += '?mode=device';

      this.ws = new WebSocketClass(wsUrl, [DEVICE_SUBPROTOCOL_V1]);

      this.ws.onopen = async () => {
        this.connected = true;
        this.reconnectAttempts = 0;

        // An agent that echoes the subprotocol speaks the hello handshake;
        // anything else is a pre-versioning agent that only knows registerDevice.
        this.protocolVersion = this.ws.protocol === DEVICE_SUBPROTOCOL_V1 ? 1 : 0;

        this._emit('connected', { protocolVersion: this.protocolVersion });

        // Auto-register after connection
        try {
          await this._register();
        } catch (err) {
          this._emit('error', { error: err, phase: 'registration' });
        }
      };

      this.ws.onmessage = (event) => {
        try {
          const data = event.data;
          const message = JSON.parse(typeof data === 'string' ? data : data.toString());
          this._handleMessage(message);
        } catch (err) {
          console.error('Failed to parse WebSocket message:', err);
        }
      };

      this.ws.onerror = (error) => {
        this._emit('error', { error, phase: 'websocket' });
      };

      this.ws.onclose = () => {
        this.connected = false;
        this.deviceID = null;
        this.protocolVersion = 0;
        this._stopHeartbeat();
        this._emit('disconnected', {});

        if (!this.intentionalDisconnect && this.autoReconnect) {
          this._attemptReconnect();
        }
      };

      await this._waitForConnection();
    } catch (err) {
      this._emit('error', { error: err, phase: 'connection' });
      throw err;
    }
  }

  /**
   * Waits for WebSocket connection to be established
   * @private
   */
  _waitForConnection() {
    return new Promise((resolve, reject) => {
      const timeout = setTimeout(() => {
        reject(new Error('Connection timeout'));
      }, 10000);

      // WebSocket readyState constants (same across all implementations)
      const OPEN = 1;
      const CLOSING = 2;
      const CLOSED = 3;

      const checkConnection = () => {
        if (this.ws.readyState === OPEN) {
          clearTimeout(timeout);
          resolve();
        } else if (this.ws.readyState === CLOSED || this.ws.readyState === CLOSING) {
          clearTimeout(timeout);
          reject(new Error('Connection failed'));
        } else {
          setTimeout(checkConnection, 100);
        }
      };

      checkConnection();
    });
  }

  /**
   * Attempts to reconnect to the server
   * @private
   */
  async _attemptReconnect() {
    if (this.maxReconnectAttempts > 0 && this.reconnectAttempts >= this.maxReconnectAttempts) {
      this._emit('error', {
        error: new Error('Max reconnection attempts reached'),
        phase: 'reconnection'
      });
      return;
    }

    this.reconnectAttempts++;
    console.log(`Reconnecting... (attempt ${this.reconnectAttempts})`);

    setTimeout(async () => {
      try {
        await this.connect();
      } catch (err) {
        console.error('Reconnection failed:', err);
      }
    }, this.reconnectDelay);
  }

  /**
   * Registers the device with the server
   * @private
   */
  async _register() {
    return new Promise((resolve, reject) => {
      const requestId = this._generateRequestId();

      const timeout = setTimeout(() => {
        delete this._pendingRequests[requestId];
        reject(new Error('Registration timeout'));
      }, 10000);

      this._pendingRequests[requestId] = {
        resolve: (data) => {
          clearTimeout(timeout);
          resolve(data);
        },
        reject: (err) => {
          clearTimeout(timeout);
          reject(err);
        }
      };

      const message = {
        id: requestId,
        type: this.protocolVersion >= 1 ? 'hello' : 'registerDevice',
        payload: {
          ...(this.protocolVersion >= 1 ? { protocolVersion: this.protocolVersion } : {}),
          deviceName: this.deviceName,
          platform: this.platform,
          appVersion: this.appVersion,
          capabilities: {
            canRead: this.canRead,
            canWrite: this.canWrite,
            nfcType: this.nfcType
          },
          metadata: {
            userAgent: typeof navigator !== 'undefined' ? navigator.userAgent : 'Unknown'
          }
        }
      };

      this._send(message);
    });
  }

  /**
   * Sends a message through WebSocket
   * @private
   */
  _send(message) {
    const data = JSON.stringify(message);
    this.ws.send(data);
  }

  /**
   * Handles incoming WebSocket messages
   * @private
   */
  _handleMessage(message) {
    const { id, type, payload, success, error } = message;

    // Handle registration response
    if (type === 'helloResponse' || type === 'registerDeviceResponse') {
      if (id && this._pendingRequests[id]) {
        const { resolve, reject } = this._pendingRequests[id];
        delete this._pendingRequests[id];

        if (success) {
          this.deviceID = payload.deviceID;
          this.serverInfo = payload.serverInfo;
          if (typeof payload.protocolVersion === 'number') {
            this.protocolVersion = payload.protocolVersion;
          }
          this._startHeartbeat();
          this._emit('registered', {
            deviceID: this.deviceID,
            serverInfo: this.serverInfo,
            protocolVersion: this.protocolVersion
          });
          resolve(payload);
        } else {
          reject(new Error(error || 'Registration failed'));
        }
      }
      return;
    }

    // Handle responses with ID
    if (id && this._pendingRequests[id]) {
      const { resolve, reject } = this._pendingRequests[id];
      delete this._pendingRequests[id];

      if (success) {
        resolve(payload);
      } else {
        reject(new Error(error || 'Request failed'));
      }
      return;
    }

    // Handle server-initiated messages
    switch (type) {
      case 'deviceWriteRequest':
        this._emit('writeRequest', {
          requestID: payload.requestID,
          deviceID: payload.deviceID,
          ndefMessage: payload.ndefMessage
        });
        break;
      case 'error':
        this._emit('error', { error: new Error(error), code: payload?.code });
        break;
      default:
        console.warn('Unknown message type:', type);
    }
  }

  /**
   * Generates a unique request ID
   * @private
   */
  _generateRequestId() {
    return `dev_${++this._requestIdCounter}_${Date.now()}`;
  }

  /**
   * Starts the heartbeat timer
   * @private
   */
  _startHeartbeat() {
    if (!this.autoHeartbeat) return;

    this._stopHeartbeat();
    this._heartbeatTimer = setInterval(() => {
      if (this.connected && this.deviceID) {
        this._sendHeartbeat();
      }
    }, this.heartbeatInterval);
  }

  /**
   * Stops the heartbeat timer
   * @private
   */
  _stopHeartbeat() {
    if (this._heartbeatTimer) {
      clearInterval(this._heartbeatTimer);
      this._heartbeatTimer = null;
    }
  }

  /**
   * Sends a heartbeat to the server
   * @private
   */
  _sendHeartbeat() {
    const message = {
      type: 'deviceHeartbeat',
      payload: {
        deviceID: this.deviceID,
        timestamp: new Date().toISOString()
      }
    };
    this._send(message);
  }

  /**
   * Send a tag scan event to the server. Call this when your NFC library detects a tag.
   * @param {Object} tagData - Tag data to send
   * @param {string} tagData.uid - Tag UID (e.g., '04:AB:CD:EF:12:34:56')
   * @param {string} [tagData.technology='ISO14443A'] - NFC technology
   * @param {string} [tagData.type='Unknown'] - Tag type (e.g., 'MIFARE Classic 1K')
   * @param {string} [tagData.atr] - Answer to Reset data
   * @param {Object} [tagData.ndefMessage] - NDEF message with records array
   * @param {string} [tagData.rawData] - Raw tag data (base64 encoded)
   * @returns {Promise<void>}
   */
  async scanTag(tagData) {
    if (!this.connected) {
      throw new Error('Not connected to server');
    }

    if (!this.deviceID) {
      throw new Error('Not registered with server');
    }

    const message = {
      type: 'tagScanned',
      payload: {
        deviceID: this.deviceID,
        uid: tagData.uid,
        technology: tagData.technology || 'ISO14443A',
        type: tagData.type || 'Unknown',
        atr: tagData.atr || '',
        scannedAt: tagData.scannedAt || new Date().toISOString(),
        ndefMessage: tagData.ndefMessage || null,
        rawData: tagData.rawData || null
      }
    };

    this._send(message);
  }

  /**
   * Send a tag removed event to the server. Call this when a tag leaves the reader.
   * @param {string} uid - UID of the removed tag
   * @returns {Promise<void>}
   */
  async removeTag(uid) {
    if (!this.connected) {
      throw new Error('Not connected to server');
    }

    if (!this.deviceID) {
      throw new Error('Not registered with server');
    }

    const message = {
      type: 'tagRemoved',
      payload: {
        deviceID: this.deviceID,
        uid: uid,
        removedAt: new Date().toISOString()
      }
    };

    this._send(message);
  }

  /**
   * Respond to a write request from the server.
   * @param {string} requestID - The request ID from the write request
   * @param {boolean} success - Whether the write was successful
   * @param {string} [error] - Error message if unsuccessful
   */
  async respondToWrite(requestID, success, error = '') {
    if (!this.connected) {
      throw new Error('Not connected to server');
    }

    const message = {
      type: 'deviceWriteResponse',
      payload: {
        requestID: requestID,
        success: success,
        error: error
      }
    };

    this._send(message);
  }

  /**
   * Get the assigned device ID
   * @returns {string|null}
   */
  getDeviceID() {
    return this.deviceID;
  }

  /**
   * Get server info received during registration
   * @returns {Object|null}
   */
  getServerInfo() {
    return this.serverInfo;
  }

  /**
   * Check if connected and registered with the server
   * @returns {boolean}
   */
  isConnected() {
    return this.connected && this.deviceID !== null;
  }

  /**
   * Disconnect from the server
   */
  async disconnect() {
    this.intentionalDisconnect = true;
    this._stopHeartbeat();

    if (this.ws) {
      this.ws.close();
    }

    this.connected = false;
    this.deviceID = null;
    this.ws = null;
  }
}

// Export for different module systems
if (typeof module !== 'undefined' && module.exports) {
  module.exports = NFCDeviceClient;
}
if (typeof window !== 'undefined') {
  window.NFCDeviceClient = NFCDeviceClient;
}
