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
 */
class NFCClient {
  /**
   * Creates a new NFC client instance
   * @param {string} serverUrl - Base URL of the NFC Agent server (e.g., 'http://localhost:9470')
   * @param {Object} options - Configuration options
   * @param {string} [options.apiSecret] - Optional API secret for authentication
   * @param {boolean} [options.autoReconnect=true] - Automatically reconnect on disconnect
   * @param {number} [options.reconnectDelay=3000] - Delay in ms before reconnecting
   * @param {number} [options.maxReconnectAttempts=10] - Maximum reconnection attempts (0 = infinite)
   */
  constructor(serverUrl, options = {}) {
    this.serverUrl = serverUrl.replace(/\/$/, ''); // Remove trailing slash
    this.apiSecret = options.apiSecret || '';
    this.autoReconnect = options.autoReconnect !== false;
    this.reconnectDelay = options.reconnectDelay || 3000;
    this.maxReconnectAttempts = options.maxReconnectAttempts || 10;

    this.ws = null;
    this.sessionToken = null;
    this.connected = false;
    this.reconnectAttempts = 0;
    this.intentionalDisconnect = false;

    // Request tracking for request/response correlation
    this._pendingRequests = {};
    this._requestIdCounter = 0;

    // Event handlers
    this.eventHandlers = {
      tagData: [],
      deviceStatus: [],
      connected: [],
      disconnected: [],
      error: []
    };
  }

  /**
   * Registers an event handler
   * @param {string} event - Event name ('tagData', 'deviceStatus', 'connected', 'disconnected', 'error')
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
   * Establishes WebSocket connection to the server
   * @returns {Promise<void>}
   * @throws {Error} If connection fails
   */
  async connect() {
    try {
      // Build WebSocket URL with optional API secret
      let wsUrl = this.serverUrl.replace(/^http/, 'ws') + '/ws';
      if (this.apiSecret) {
        wsUrl += `?secret=${encodeURIComponent(this.apiSecret)}`;
      }

      this.ws = new WebSocket(wsUrl);

      // Set up WebSocket event handlers
      this.ws.onopen = () => {
        this.connected = true;
        this.reconnectAttempts = 0;
        this._emit('connected', {});
      };

      this.ws.onmessage = (event) => {
        try {
          const message = JSON.parse(event.data);
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
        this._emit('disconnected', {});

        if (!this.intentionalDisconnect && this.autoReconnect) {
          this._attemptReconnect();
        }
      };

      // Wait for connection to establish
      await this._waitForConnection();
    } catch (err) {
      this._emit('error', { error: err, phase: 'connection' });
      throw err;
    }
  }

  /**
   * Waits for WebSocket connection to be established
   * @private
   * @returns {Promise<void>}
   */
  _waitForConnection() {
    return new Promise((resolve, reject) => {
      const timeout = setTimeout(() => {
        reject(new Error('Connection timeout'));
      }, 10000);

      const checkConnection = () => {
        if (this.ws.readyState === WebSocket.OPEN) {
          clearTimeout(timeout);
          resolve();
        } else if (this.ws.readyState === WebSocket.CLOSED || this.ws.readyState === WebSocket.CLOSING) {
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
   * Handles incoming WebSocket messages
   * @private
   */
  _handleMessage(message) {
    const { id, type, payload, success, error } = message;

    // Handle responses to requests (with ID)
    if (id && this._pendingRequests && this._pendingRequests[id]) {
      const { resolve, reject } = this._pendingRequests[id];
      delete this._pendingRequests[id];

      if (success) {
        resolve(payload);
      } else {
        reject(new Error(error || 'Request failed'));
      }
      return;
    }

    // Handle broadcast messages (without ID)
    switch (type) {
      case 'tagData':
        this._emit('tagData', this._parseTagData(payload));
        break;
      case 'deviceStatus':
        this._emit('deviceStatus', payload);
        break;
      case 'error':
        this._emit('error', { error: new Error(error), code: payload?.code });
        break;
      default:
        console.warn('Unknown message type:', type);
    }
  }

  /**
   * Parses tag data payload
   * @private
   */
  _parseTagData(payload) {
    const tagData = {
      uid: payload.uid || '',
      type: payload.type || '',
      technology: payload.technology || '',
      scannedAt: payload.scannedAt ? new Date(payload.scannedAt) : null,
      text: payload.text || '',
      message: payload.message || null,
      error: payload.err || null,
      // Preserve raw payload for debugging
      _raw: payload
    };

    // Parse NDEF message if available
    if (tagData.message && tagData.message.type === 'ndef') {
      tagData.ndefRecords = tagData.message.records || [];
    }

    return tagData;
  }

  /**
   * Generates a unique request ID
   * @private
   */
  _generateRequestId() {
    return `req_${++this._requestIdCounter}_${Date.now()}`;
  }

  /**
   * Writes NDEF data to an NFC card (complete overwrite)
   * 
   * Simplified API: Always overwrites the entire NDEF message.
   * To append records, read the current data first, modify it, and write back.
   * 
   * @param {Object} writeRequest - Write request parameters
   * @param {Array<Object>} [writeRequest.records] - Array of NDEF records to write
   * @param {string} writeRequest.records[].type - Record type ('text' or 'uri')
   * @param {string} writeRequest.records[].content - Text or URI content
   * @param {string} [writeRequest.records[].language='en'] - Language code for text records
   * @param {string} [writeRequest.text] - Deprecated: Simple text write (for backward compatibility)
   * @returns {Promise<Object>} Response payload
   * 
   * @example
   * // Write single text record
   * await client.write({ text: 'Hello, NFC!' });
   * 
   * @example
   * // Write multiple records
   * await client.write({
   *   records: [
   *     { type: 'text', content: 'Hello, NFC!' },
   *     { type: 'uri', content: 'https://example.com' }
   *   ]
   * });
   * 
   * @example
   * // Append records (read first, then write)
   * const lastTag = await client.getLastTag();
   * const existingRecords = lastTag.card.message.records.map(r => ({
   *   type: r.type === 'T' ? 'text' : 'uri',
   *   content: r.text || r.uri
   * }));
   * await client.write({
   *   records: [...existingRecords, { type: 'text', content: 'New record' }]
   * });
   */
  async write(writeRequest) {
    if (!this.connected) {
      throw new Error('Not connected to server');
    }

    return new Promise((resolve, reject) => {
      const requestId = this._generateRequestId();
      
      // Store promise handlers for response correlation
      this._pendingRequests[requestId] = { resolve, reject };

      // Set timeout for request
      const timeout = setTimeout(() => {
        delete this._pendingRequests[requestId];
        reject(new Error('Write request timeout'));
      }, 30000); // 30 second timeout

      // Override reject to clear timeout
      const originalReject = reject;
      reject = (err) => {
        clearTimeout(timeout);
        originalReject(err);
      };

      // Override resolve to clear timeout
      const originalResolve = resolve;
      resolve = (data) => {
        clearTimeout(timeout);
        originalResolve(data);
      };

      // Update stored handlers
      this._pendingRequests[requestId] = { resolve, reject };

      // Send write request with ID
      const message = {
        id: requestId,
        type: 'writeRequest',
        payload: writeRequest
      };

      this.ws.send(JSON.stringify(message));
    });
  }

  /**
   * Disconnects from the server
   * @returns {Promise<void>}
   */
  async disconnect() {
    this.intentionalDisconnect = true;

    if (this.connected && this.ws) {
      // Close WebSocket connection (releases session automatically)
      this.ws.close();
    }

    this.connected = false;
    this.ws = null;
  }

  /**
   * Gets the current connection status
   * @returns {boolean} True if connected
   */
  isConnected() {
    return this.connected;
  }

  // REST API Methods

  /**
   * Gets the last scanned tag (polling alternative to WebSocket)
   * @returns {Promise<Object|null>} Last tag data or null
   */
  async getLastTag() {
    const response = await fetch(`${this.serverUrl}/api/v1/tags/last`);
    const data = await response.json();
    if (!data.success) {
      throw new Error(data.error || 'Failed to get last tag');
    }
    return data.card ? this._parseTagData(data.card) : null;
  }

  /**
   * Gets server and device status
   * @returns {Promise<Object>} Status information
   */
  async getStatus() {
    const response = await fetch(`${this.serverUrl}/api/v1/status`);
    const data = await response.json();
    if (!data.success) {
      throw new Error(data.error || 'Failed to get status');
    }
    return data;
  }

  /**
   * Performs a health check
   * @returns {Promise<Object>} Health check result
   */
  async healthCheck() {
    const response = await fetch(`${this.serverUrl}/api/v1/health`);
    return await response.json();
  }
}

// Export for different module systems
if (typeof module !== 'undefined' && module.exports) {
  module.exports = NFCClient;
}
if (typeof window !== 'undefined') {
  window.NFCClient = NFCClient;
}
