import type {
  NTAG424FileSettings,
  NTAG424Request,
  NTAG424Response,
  NTAG424SDMOptions,
  NTAG424SDMPlan,
} from '@davi/nfc-agent-client'

/**
 * The logic behind the NTAG 424 panel, kept out of the component so it can be
 * tested without a browser. No key material passes through here except the one
 * a person types into the explicit change-key field, which is validated and
 * sent as it is; the keys the agent holds never reach the page.
 */

export const ACCESS_FREE = 14
export const ACCESS_NEVER = 15

/** A key number 0 to 4, or 14 for free access, or 15 for never. */
export const RIGHT_OPTIONS: { value: number; label: string }[] = [
  { value: 0, label: 'Key 0' },
  { value: 1, label: 'Key 1' },
  { value: 2, label: 'Key 2' },
  { value: 3, label: 'Key 3' },
  { value: 4, label: 'Key 4' },
  { value: ACCESS_FREE, label: 'Free' },
  { value: ACCESS_NEVER, label: 'Never' },
]

export function rightLabel(n: number): string {
  if (n === ACCESS_FREE) return 'free'
  if (n === ACCESS_NEVER) return 'never'
  return `key ${n}`
}

/** NDEF file size, NLEN included, which bounds a plan. */
export const NDEF_FILE_SIZE = 256

/** Whether a reported tag type names an NTAG 424 DNA. */
export function looksLikeNtag424(type: string | undefined): boolean {
  return /ntag\s*-?\s*424/i.test(type ?? '')
}

/* ---- the SDM form ---- */

export interface SdmForm {
  template: string
  /** Key that encrypts {picc}. */
  metaRead: number
  /** Key the MAC and {enc} derive from. 0 to 4. */
  fileRead: number
  counterRet: number
  change: number
  read: number
  write: number
  readWrite: number
  /** Width of {enc} in mirrored characters, as typed. */
  encLength: string
}

export const DEFAULT_SDM_FORM: SdmForm = {
  template: 'https://example.com/t?p={picc}&m={mac}',
  metaRead: 0,
  fileRead: 0,
  counterRet: ACCESS_NEVER,
  change: 0,
  read: ACCESS_FREE,
  write: 0,
  readWrite: 0,
  encLength: '32',
}

const PLACEHOLDERS = ['{picc}', '{uid}', '{ctr}', '{enc}', '{mac}'] as const
export type Placeholder = (typeof PLACEHOLDERS)[number]

export function placeholdersIn(template: string): Placeholder[] {
  return PLACEHOLDERS.filter((p) => template.includes(p))
}

export type Built<T> = { ok: true; request: T } | { ok: false; error: string }

type SdmOp = 'planSDM' | 'configureSDM'

/**
 * Turns the form into the options and template the agent plans from, or says
 * what is wrong with it. The agent checks everything again; this only spares a
 * round trip for the mistakes that are obvious from the template alone.
 */
export function sdmRequest<Op extends SdmOp>(
  form: SdmForm,
  op: Op,
): Built<Extract<NTAG424Request, { op: Op }>> {
  const template = form.template.trim()
  if (template === '') return { ok: false, error: 'Enter a URL template.' }

  const found = placeholdersIn(template)
  const has = (p: Placeholder) => found.includes(p)

  if (!has('{mac}')) return { ok: false, error: 'The template needs {mac}.' }
  if (has('{picc}') && (has('{uid}') || has('{ctr}'))) {
    return { ok: false, error: '{picc} cannot be combined with {uid} or {ctr}.' }
  }
  if (has('{uid}') !== has('{ctr}')) {
    return { ok: false, error: '{uid} and {ctr} go together.' }
  }
  if (!has('{picc}') && !has('{uid}')) {
    return { ok: false, error: 'The template needs {picc}, or {uid} and {ctr}.' }
  }
  if (form.fileRead < 0 || form.fileRead > 4) {
    return { ok: false, error: 'The file read key must be key 0 to 4.' }
  }

  const sdm: NTAG424SDMOptions = {
    metaRead: form.metaRead,
    fileRead: form.fileRead,
    counterRet: form.counterRet,
    change: form.change,
    read: form.read,
    write: form.write,
    readWrite: form.readWrite,
  }

  if (has('{enc}')) {
    const width = Number(form.encLength)
    if (!Number.isInteger(width) || width <= 0 || width % 32 !== 0) {
      return { ok: false, error: 'The {enc} width must be a multiple of 32.' }
    }
    sdm.encLength = width
  }

  const request = { op, urlTemplate: template, sdm } as unknown as Extract<NTAG424Request, { op: Op }>
  return { ok: true, request }
}

/* ---- the layout preview ---- */

export type RegionKind = 'picc' | 'uid' | 'ctr' | 'enc' | 'mac'

export interface Region {
  kind: RegionKind
  /** Byte offset into the NDEF file content, NLEN included. */
  start: number
  /** One past the last byte. */
  end: number
}

/** Mirror widths in ASCII characters. */
const WIDTH = { picc: 32, uid: 14, ctr: 6, mac: 16 } as const

export function hexToBytes(hex: string): Uint8Array | null {
  const cleaned = hex.replace(/\s+/g, '')
  if (cleaned.length % 2 !== 0 || !/^[0-9a-fA-F]*$/.test(cleaned)) return null
  const out = new Uint8Array(cleaned.length / 2)
  for (let i = 0; i < out.length; i++) out[i] = Number.parseInt(cleaned.slice(i * 2, i * 2 + 2), 16)
  return out
}

/** Where each mirror lands, from the settings the agent would write. */
export function planRegions(plan: NTAG424SDMPlan): Region[] {
  const s = plan.settings
  if (!s) return []

  const regions: Region[] = []
  const add = (kind: RegionKind, start: number | undefined, width: number) => {
    if (start !== undefined && start > 0) regions.push({ kind, start, end: start + width })
  }
  add('picc', s.piccDataOffset, WIDTH.picc)
  add('uid', s.uidOffset, WIDTH.uid)
  add('ctr', s.readCounterOffset, WIDTH.ctr)
  add('enc', s.encOffset, s.encLength ?? 0)
  add('mac', s.macOffset, WIDTH.mac)
  return regions.sort((a, b) => a.start - b.start)
}

/** The span the MAC covers: from the start of {enc} to the start of {mac}. Empty without {enc}. */
export function macInput(plan: NTAG424SDMPlan): { start: number; end: number } | null {
  const s = plan.settings
  if (!s || !s.macInputOffset || !s.macOffset || s.macInputOffset >= s.macOffset) return null
  return { start: s.macInputOffset, end: s.macOffset }
}

export interface Segment {
  text: string
  /** Absent for the URL's own characters. */
  kind?: RegionKind
  /** Inside the span the MAC covers. */
  macInput: boolean
}

/** The abbreviations a URI record's first payload byte stands for. */
const URI_PREFIXES: Record<number, string> = {
  0x00: '',
  0x01: 'http://www.',
  0x02: 'https://www.',
  0x03: 'http://',
  0x04: 'https://',
}

/** NLEN (2) plus the record header: flags, type length, payload length, 'U', prefix code. */
const URL_START = 7

export interface Preview {
  /** The NDEF content as uppercase hex. */
  hex: string
  length: number
  /** The first payload byte's abbreviation, shown in front of the URL's own text. */
  prefix: string
  segments: Segment[]
  /** What the card would hold before any tap, mirrors as zeros. */
  url: string
  fits: boolean
}

/**
 * Lays the plan's bytes out for display: the URL with each mirror marked, and
 * the span the MAC covers. Returns null for content that is not the one URI
 * record a plan always is, rather than guessing at it.
 */
export function previewPlan(plan: NTAG424SDMPlan): Preview | null {
  const bytes = hexToBytes(plan.ndefHex)
  if (!bytes || bytes.length < URL_START) return null
  if (bytes[2] !== 0xd1 || bytes[5] !== 0x55) return null

  const prefix = URI_PREFIXES[bytes[6]]
  if (prefix === undefined) return null

  const regions = planRegions(plan)
  const covered = macInput(plan)

  const kindAt = (i: number): RegionKind | undefined =>
    regions.find((r) => i >= r.start && i < r.end)?.kind
  const inMac = (i: number) => covered !== null && i >= covered.start && i < covered.end

  const segments: Segment[] = []
  for (let i = URL_START; i < bytes.length; i++) {
    const kind = kindAt(i)
    const mac = inMac(i)
    const ch = String.fromCharCode(bytes[i])
    const last = segments[segments.length - 1]
    if (last && last.kind === kind && last.macInput === mac) last.text += ch
    else segments.push({ text: ch, kind, macInput: mac })
  }

  return {
    hex: plan.ndefHex.toUpperCase(),
    length: plan.length,
    prefix,
    segments,
    url: prefix + segments.map((s) => s.text).join(''),
    fits: plan.length <= NDEF_FILE_SIZE,
  }
}

/** Rows describing where each mirror lands, for the offsets table. */
export function layoutRows(plan: NTAG424SDMPlan): { label: string; offset: number; width: number }[] {
  const names: Record<RegionKind, string> = {
    picc: 'PICC data',
    uid: 'UID',
    ctr: 'Read counter',
    enc: 'Encrypted file data',
    mac: 'MAC',
  }
  return planRegions(plan).map((r) => ({ label: names[r.kind], offset: r.start, width: r.end - r.start }))
}

/* ---- the card ---- */

export type Part<T> = { ok: true; value: T } | { ok: false; error: string }

export interface CardReading {
  fileSettings: Part<NTAG424FileSettings>
  uid: Part<string>
  /** Key versions by key number 0 to 4. */
  keyVersions: Part<number>[]
  signature: Part<string>
}

export type Run = (request: NTAG424Request) => Promise<NTAG424Response>

async function part<T>(get: () => Promise<T | undefined>, what: string): Promise<Part<T>> {
  try {
    const value = await get()
    if (value === undefined) return { ok: false, error: `the agent returned no ${what}` }
    return { ok: true, value }
  } catch (err) {
    return { ok: false, error: err instanceof Error ? err.message : String(err) }
  }
}

/**
 * Reads everything the panel shows about the card. Each part is asked on its
 * own, so one refused (the card wants a key the agent does not hold) does not
 * hide the rest.
 */
export async function readCard(run: Run): Promise<CardReading> {
  const fileSettings = await part(
    async () => (await run({ op: 'getFileSettings' })).fileSettings ?? undefined,
    'file settings',
  )
  const uid = await part(async () => (await run({ op: 'getCardUID' })).uid, 'UID')

  const keyVersions: Part<number>[] = []
  for (let keyNo = 0; keyNo <= 4; keyNo++) {
    keyVersions.push(
      await part(async () => (await run({ op: 'getKeyVersion', keyNo })).keyVersion, 'key version'),
    )
  }

  const signature = await part(async () => (await run({ op: 'readSig' })).signature, 'signature')
  return { fileSettings, uid, keyVersions, signature }
}

/** Settings as label and value pairs, in the order a person reads them. */
export function describeFileSettings(fs: NTAG424FileSettings): { label: string; value: string }[] {
  const rows = [
    { label: 'Size', value: `${fs.fileSize} B` },
    { label: 'Communication', value: fs.commMode },
    { label: 'Read', value: rightLabel(fs.read) },
    { label: 'Write', value: rightLabel(fs.write) },
    { label: 'Read and write', value: rightLabel(fs.readWrite) },
    { label: 'Change settings', value: rightLabel(fs.change) },
    { label: 'SDM', value: fs.sdmEnabled ? 'enabled' : 'off' },
  ]
  if (fs.sdmEnabled) {
    rows.push(
      { label: 'Mirrors', value: mirrors(fs) },
      { label: 'Metadata read', value: rightLabel(fs.sdmMetaRead ?? 0) },
      { label: 'File read (MAC)', value: rightLabel(fs.sdmFileRead ?? 0) },
      { label: 'Counter read', value: rightLabel(fs.sdmCounterRet ?? 0) },
    )
  }
  return rows
}

function mirrors(fs: NTAG424FileSettings): string {
  return [fs.mirrorUID && 'UID', fs.mirrorReadCounter && 'counter', fs.encryptFileData && 'encrypted data', 'MAC']
    .filter(Boolean)
    .join(', ')
}

/** The signature as hex for display, from the base64 the agent returns. */
export function signatureHex(b64: string): string {
  try {
    return Array.from(atob(b64), (c) => c.charCodeAt(0).toString(16).toUpperCase().padStart(2, '0')).join('')
  } catch {
    return ''
  }
}

/* ---- irreversible operations ---- */

export interface ChangeKeyForm {
  keyNo: string
  authKeyNo: string
  version: string
  source: 'configured' | 'explicit'
  /** 32 hex characters, for the explicit source. */
  newKey: string
}

export const DEFAULT_CHANGE_KEY_FORM: ChangeKeyForm = {
  keyNo: '1',
  authKeyNo: '0',
  version: '0',
  source: 'configured',
  newKey: '',
}

type ChangeKeyRequest = Extract<NTAG424Request, { op: 'changeKey' }>
type LockRequest = Extract<NTAG424Request, { op: 'lock' }>

function keyNumber(s: string, name: string): number | string {
  const n = Number(s)
  if (s.trim() === '' || !Number.isInteger(n) || n < 0 || n > 4) return `${name} must be 0 to 4.`
  return n
}

export type ChangeKeyChecked =
  | { ok: true; keyNo: number; authKeyNo: number; version: number; newKey?: string }
  | { ok: false; error: string }

/** The form checked and turned into numbers, without yet being a request. */
export function checkChangeKey(form: ChangeKeyForm): ChangeKeyChecked {
  const keyNo = keyNumber(form.keyNo, 'Key number')
  if (typeof keyNo === 'string') return { ok: false, error: keyNo }
  const authKeyNo = keyNumber(form.authKeyNo, 'Authenticating key')
  if (typeof authKeyNo === 'string') return { ok: false, error: authKeyNo }

  const version = Number(form.version)
  if (form.version.trim() === '' || !Number.isInteger(version) || version < 0 || version > 255) {
    return { ok: false, error: 'Version must be 0 to 255.' }
  }

  if (form.source === 'configured') return { ok: true, keyNo, authKeyNo, version }

  const newKey = form.newKey.replace(/[\s:-]/g, '')
  if (!/^[0-9a-fA-F]{32}$/.test(newKey)) return { ok: false, error: 'The new key is 32 hex characters.' }
  return { ok: true, keyNo, authKeyNo, version, newKey }
}

/** What the person types to confirm replacing a key. */
export function changeKeyPhrase(keyNo: number): string {
  return `change key ${keyNo}`
}

export const LOCK_PHRASE = 'lock'

export function phraseMatches(typed: string, phrase: string): boolean {
  return typed.trim().toLowerCase() === phrase.toLowerCase()
}

/**
 * The request that replaces a key. It carries `confirm: true`, so it is built
 * only from a confirmation: with the wrong phrase it throws and no request
 * exists to send.
 */
export function confirmedChangeKey(form: ChangeKeyForm, typed: string): ChangeKeyRequest {
  const checked = checkChangeKey(form)
  if (!checked.ok) throw new Error(checked.error)
  if (!phraseMatches(typed, changeKeyPhrase(checked.keyNo))) {
    throw new Error('Confirmation phrase does not match.')
  }
  return {
    op: 'changeKey',
    keyNo: checked.keyNo,
    authKeyNo: checked.authKeyNo,
    version: checked.version,
    newKeySource: form.source,
    ...(checked.newKey ? { newKey: checked.newKey } : {}),
    confirm: true,
  }
}

/** The request that makes the NDEF file read-only, built only from a confirmation. */
export function confirmedLock(typed: string): LockRequest {
  if (!phraseMatches(typed, LOCK_PHRASE)) throw new Error('Confirmation phrase does not match.')
  return { op: 'lock', confirm: true }
}
