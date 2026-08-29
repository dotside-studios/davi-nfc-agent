/** Control-surface types, mirroring the Go types in agent/console/state.go. */

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

/** What the reader is doing. What it is set to is in Settings. */
export interface ReaderInfo {
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
  caInstalled: boolean
  caFingerprint?: string
  cert?: CertInfo
  controlSessions: number
}

/** What the agent is set to, as the agent reports it. The console's only
 *  source for a preference, and what every control here is bound to. */
export interface Settings {
  mode: Mode
  cardTypes: string[] | null
  devicePath: string
  port: number
  requirePairedDevice: boolean
  readerFeedback: boolean
  allowRawApdu: boolean
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

/* ---- tag traffic ---- */

/**
 * The wire types belong to the client library. Re-exported so a panel imports
 * one thing, and so the console is held to the definitions every other
 * consumer is.
 */
export type {
  NDEFRecord,
  TagCapabilities,
  TagData,
  WriteRecord,
} from '@davi/nfc-agent-client'

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
