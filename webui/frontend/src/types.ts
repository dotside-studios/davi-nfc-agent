/** Mirrors the Go types in webui/state.go. */

export type Mode = 'readwrite' | 'read' | 'write'

export interface AgentInfo {
  name: string
  version: string
  dev: boolean
  running: boolean
  startedAt: string
  uptimeSec: number
  configDir: string
  platform: string
}

export interface ReaderInfo {
  mode: Mode
  devicePath: string
  available: string[]
  cardPresent: boolean
  cardUID?: string
  cardType?: string
  allCardTypes: string[]
  remoteDevices: number
  remoteActive: number
}

export interface ServerInfo {
  port: number
  bootstrapPort: number
  tls: boolean
  clientURL: string
  deviceURL: string
  pairingURL?: string
  localIPs: string[]
  clients: number
}

export interface CertInfo {
  subject: string
  issuer: string
  notBefore: string
  notAfter: string
  expiresInHr: number
  expired: boolean
  selfSigned: boolean
  hosts: string[]
  fingerprint: string
}

export interface SecurityInfo {
  apiSecret: string
  pairingPIN?: string
  publicKeyPin?: string
  requirePairedDevice: boolean
  caInstalled: boolean
  caFingerprint?: string
  cert?: CertInfo
  controlSessions: number
}

export interface Settings {
  mode: Mode
  cardTypes: string[] | null
  devicePath: string
  port: number
  requirePairedDevice: boolean
}

export interface DeviceInfo {
  id: string
  name: string
  platform: string
  pairedAt: string
  lastSeen?: string
  online: boolean
}

/** An application currently connected to the client endpoint. */
export interface ClientInfo {
  id: string
  origin?: string
  remoteAddr: string
  userAgent?: string
  connectedAt: string
  writes: number
  locks: number
}

export interface OriginsInfo {
  allowed: string[]
  blocked: string[]
  allowAny: boolean
}

export interface CaptureInfo {
  logEntries: number
  logSeq: number
}

export interface ControlState {
  agent: AgentInfo
  reader: ReaderInfo
  server: ServerInfo
  security: SecurityInfo
  settings: Settings
  devices: DeviceInfo[]
  clients: ClientInfo[]
  origins: OriginsInfo
  capture: CaptureInfo
}

export type LogLevel = 'info' | 'warn' | 'error'

export interface LogEntry {
  seq: number
  time: string
  level: LogLevel
  source?: string
  message: string
}

/* ---- tag traffic, over the ordinary client endpoint ---- */

export interface TagCapabilities {
  memorySize?: number
  usableCapacity?: number
  writable?: boolean
  lockable?: boolean
  passwordProtectable?: boolean
  readOnly?: boolean
  [k: string]: unknown
}

export interface NdefRecord {
  tnf?: number
  type?: string
  id?: string
  payload?: string
  text?: string
  uri?: string
  language?: string
  [k: string]: unknown
}

export interface TagData {
  uid: string
  type: string
  technology: string
  scannedAt: string
  text: string
  message?: {
    records?: NdefRecord[]
    [k: string]: unknown
  }
  capabilities?: TagCapabilities
  err?: string | null

  /** The paired device that scanned it. Absent for the agent's own reader. */
  deviceID?: string
}

/** A record as the composer submits it. Field use varies by type — see the
 *  Record Fields table in docs/api.md. */
export interface WriteRecord {
  type: string
  content?: string
  language?: string
  mimeType?: string
  title?: string
  payload?: string
  tnf?: number
  typeBytes?: string
  id?: string
}

/** A distinct tag seen by the reader, with how often it has come back. */
export interface ScanRecord {
  uid: string
  type: string
  text: string
  count: number
  firstAt: string
  lastAt: string
}

/** One raw exchange with a tag, as the APDU console records it. */
export interface Exchange {
  id: number
  at: string
  command: string
  response?: string
  raw: boolean
  ok: boolean
  error?: string
  elapsedMs: number
}

/** One line in the live event feed. */
export interface LiveEvent {
  id: number
  at: string
  kind: 'scan' | 'removed' | 'write' | 'lock' | 'apdu' | 'status' | 'error'
  summary: string
  detail?: string
  ok?: boolean
}
