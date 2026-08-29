/** Hex and ISO 7816 status words for the APDU console. Base64 is the wire's
 *  business and lives in the client library. */

/** Parses hex, ignoring the separators people actually paste: spaces, colons,
 *  dashes, newlines and a leading 0x. Returns null on anything unparseable. */
export function parseHex(input: string): Uint8Array | null {
  const cleaned = input
    .replace(/0[xX]/g, '')
    .replace(/[\s:,-]/g, '')
    .trim()

  if (cleaned.length === 0) return null
  if (cleaned.length % 2 !== 0) return null
  if (!/^[0-9a-fA-F]+$/.test(cleaned)) return null

  const out = new Uint8Array(cleaned.length / 2)
  for (let i = 0; i < out.length; i++) {
    out[i] = Number.parseInt(cleaned.slice(i * 2, i * 2 + 2), 16)
  }
  return out
}

export function toHex(bytes: Uint8Array, separator = ' '): string {
  return Array.from(bytes)
    .map((b) => b.toString(16).toUpperCase().padStart(2, '0'))
    .join(separator)
}

/** Printable ASCII beside the hex, since a lot of tag payloads are text. */
export function toAscii(bytes: Uint8Array): string {
  return Array.from(bytes)
    .map((b) => (b >= 0x20 && b <= 0x7e ? String.fromCharCode(b) : '.'))
    .join('')
}

export interface StatusWord {
  sw: string
  ok: boolean
  meaning: string
}

/**
 * Interprets the last two bytes as an ISO 7816 status word.
 *
 * Only meaningful for APDU-level exchanges: a framing-level response has no
 * status word, and the caller decides whether to ask. Returns null for a
 * response too short to carry one.
 */
export function readStatusWord(bytes: Uint8Array): StatusWord | null {
  if (bytes.length < 2) return null

  const sw1 = bytes[bytes.length - 2]
  const sw2 = bytes[bytes.length - 1]
  const sw = ((sw1 << 8) | sw2) >>> 0
  const hex = sw.toString(16).toUpperCase().padStart(4, '0')

  return { sw: hex, ok: sw === 0x9000, meaning: describeStatus(sw1, sw2) }
}

function describeStatus(sw1: number, sw2: number): string {
  const sw = ((sw1 << 8) | sw2) >>> 0

  const exact: Record<number, string> = {
    0x9000: 'Success',
    0x6200: 'Warning: no information given',
    0x6281: 'Warning: returned data may be corrupted',
    0x6282: 'Warning: end of file reached before Le bytes',
    0x6283: 'Warning: selected file is invalidated',
    0x6300: 'Warning: authentication failed',
    0x6400: 'Execution error: state unchanged',
    0x6500: 'Execution error: memory unchanged',
    0x6581: 'Memory failure',
    0x6700: 'Wrong length',
    0x6800: 'Function in CLA not supported',
    0x6881: 'Logical channel not supported',
    0x6882: 'Secure messaging not supported',
    0x6900: 'Command not allowed',
    0x6981: 'Command incompatible with file structure',
    0x6982: 'Security status not satisfied: authenticate first',
    0x6983: 'Authentication method blocked',
    0x6984: 'Referenced data invalidated',
    0x6985: 'Conditions of use not satisfied',
    0x6986: 'Command not allowed: no current file selected',
    0x6987: 'Expected secure messaging object missing',
    0x6a80: 'Incorrect parameters in the data field',
    0x6a81: 'Function not supported',
    0x6a82: 'File or application not found',
    0x6a83: 'Record not found',
    0x6a84: 'Not enough memory space in the file',
    0x6a86: 'Incorrect parameters P1-P2',
    0x6a88: 'Referenced data not found',
    0x6b00: 'Wrong parameters P1-P2',
    0x6d00: 'Instruction code not supported or invalid',
    0x6e00: 'Class not supported',
    0x6f00: 'No precise diagnosis',
  }
  if (exact[sw]) return exact[sw]

  // Two families carry their detail in SW2 rather than the pair.
  if (sw1 === 0x61) return `Success: ${sw2} more byte(s) available (GET RESPONSE)`
  if (sw1 === 0x6c) return `Wrong Le: retry with Le = ${sw2}`
  if (sw1 === 0x63 && (sw2 & 0xf0) === 0xc0) return `Authentication failed: ${sw2 & 0x0f} attempt(s) left`

  return 'Unknown status'
}

/**
 * Commands worth having a click away. Built from what nfc/apdu.go already
 * constructs, so what the console sends matches what the agent itself would.
 */
export interface ApduPreset {
  label: string
  hex: string
  note: string
  /** Framing-level rather than APDU-level. */
  raw?: boolean
}

export const APDU_PRESETS: ApduPreset[] = [
  { label: 'Get UID', hex: 'FF CA 00 00 00', note: 'PC/SC pseudo-APDU. Returns the UID and 9000.' },
  { label: 'Select NDEF app', hex: '00 A4 04 00 07 D2 76 00 00 85 01 01 00', note: 'Type 4 NDEF application AID.' },
  { label: 'Select CC file', hex: '00 A4 00 0C 02 E1 03', note: 'Capability Container, after selecting the app.' },
  { label: 'Select NDEF file', hex: '00 A4 00 0C 02 E1 04', note: 'The NDEF file itself.' },
  { label: 'Read binary (16 B)', hex: '00 B0 00 00 10', note: 'Reads 16 bytes from the selected file at offset 0.' },
  { label: 'DESFire GetVersion', hex: '90 60 00 00 00', note: 'Wrapped DESFire command. Continues with 91AF.' },
  { label: 'DESFire app IDs', hex: '90 6A 00 00 00', note: 'Lists application IDs on a DESFire card.' },
  { label: 'Ultralight read page 0', hex: '30 00', note: 'Framing-level. Returns 16 bytes: pages 0-3.', raw: true },
  { label: 'NTAG GET_VERSION', hex: '60', note: 'Framing-level. Identifies the exact NTAG variant.', raw: true },
]

/* -------------------------------------------------------------------------- */
/* Command explainer                                                          */
/*                                                                            */
/* Decodes a command into what it does, so the operator sees the meaning and  */
/* the danger before sending. This mirrors nfc.Explain in nfc/apdu_explain.go */
/* — the Go side is the source of truth and is round-trip tested against every */
/* builder in nfc/apdu.go; keep the two in step when either changes.          */
/* -------------------------------------------------------------------------- */

/** What an exchange does, coarse enough to gate or warn on. */
export type ApduClass =
  | 'read'
  | 'write'
  | 'lock'
  | 'auth'
  | 'select'
  | 'info'
  | 'reader-control'
  | 'unknown'

export interface ApduExplanation {
  /** One-line, human-readable account of the command. */
  summary: string
  /** What the command does. */
  cls: ApduClass
  /** Whether it can change or permanently alter the tag. Errs toward true. */
  mutating: boolean
  /** Reasons to look twice: irreversible effect, lock/OTP page, malformed, unknown. */
  warnings: string[]
  /** Whether the decoder recognised the command. */
  recognized: boolean
}

interface Fields {
  cla: number
  ins: number
  p1: number
  p2: number
  hasLc: boolean
  lc: number
  data: Uint8Array
  hasLe: boolean
  le: number
}

/**
 * Decodes a command into what it does. `raw` selects framing-level (native)
 * over APDU-level (ISO 7816-4), matching the flag on the exchange. It never
 * throws: an unparseable or unknown command is reported as unrecognised, which
 * is the answer a safety net needs.
 */
export function explain(cmd: Uint8Array, raw: boolean): ApduExplanation {
  if (cmd.length === 0) {
    return { summary: 'empty command', cls: 'unknown', mutating: false, warnings: ['no bytes to send'], recognized: false }
  }
  const e = raw ? explainFraming(cmd) : explainApdu(cmd)
  e.warnings = dedupe(e.warnings)
  return e
}

function explainApdu(cmd: Uint8Array): ApduExplanation {
  const e: ApduExplanation = { summary: '', cls: 'unknown', mutating: false, warnings: [], recognized: false }
  if (cmd.length < 4) {
    e.summary = `truncated APDU (${toHex(cmd)})`
    e.mutating = true
    e.warnings.push('an APDU has at least CLA INS P1 P2 (4 bytes)')
    return e
  }

  const [f, lenWarn] = parseBody(cmd)
  if (lenWarn) e.warnings.push(lenWarn)

  switch (f.cla) {
    case 0xff: // PC/SC pseudo-APDU
      describePcsc(e, f)
      break
    case 0x90: // DESFire-wrapped native
      describeDesfire(e, f)
      break
    case 0x00: // ISO 7816-4
      describeIso(e, f)
      break
    case 0x80:
      e.summary = `proprietary command (CLA 80, INS ${hb(f.ins)})`
      break
    default:
      e.summary = `unrecognised command (CLA ${hb(f.cla)} INS ${hb(f.ins)})`
  }

  finalize(e)
  return e
}

/** Splits an APDU across the ISO 7816-4 short cases; extended form is flagged. */
function parseBody(cmd: Uint8Array): [Fields, string] {
  const f: Fields = {
    cla: cmd[0],
    ins: cmd[1],
    p1: cmd[2],
    p2: cmd[3],
    hasLc: false,
    lc: 0,
    data: new Uint8Array(0),
    hasLe: false,
    le: 0,
  }
  const body = cmd.subarray(4)

  if (body.length === 0) return [f, ''] // case 1
  if (body.length === 1) {
    f.hasLe = true
    f.le = leValue(body[0])
    return [f, ''] // case 2S
  }
  if (body[0] === 0x00 && body.length >= 3) {
    return [f, 'extended-length APDU; structural fields not fully decoded']
  }

  const lc = body[0]
  const rest = body.subarray(1)
  f.hasLc = true
  f.lc = lc

  if (rest.length === lc) {
    f.data = rest // case 3S
    return [f, '']
  }
  if (rest.length === lc + 1) {
    f.data = rest.subarray(0, lc) // case 4S
    f.hasLe = true
    f.le = leValue(rest[lc])
    return [f, '']
  }
  f.data = rest.length < lc ? rest : rest.subarray(0, lc)
  return [f, `declared length Lc=${lc} does not match ${rest.length} byte(s) of data present`]
}

function leValue(b: number): number {
  return b === 0x00 ? 256 : b
}

function describePcsc(e: ApduExplanation, f: Fields): void {
  e.recognized = true
  switch (f.ins) {
    case 0xca: // GET UID
      e.summary = 'GET UID — read the card serial number'
      e.cls = 'info'
      break
    case 0xb0: // READ BINARY
      e.summary = `READ BINARY — read ${f.hasLe ? f.le : 0} byte(s) from page/block ${f.p2}`
      e.cls = 'read'
      break
    case 0xd6: // UPDATE BINARY
      e.summary = `UPDATE BINARY — write ${f.data.length} byte(s) to page/block ${f.p2}`
      e.cls = 'write'
      flagPageWrite(e, f.p2)
      break
    case 0x82: // LOAD KEY
      e.summary = `LOAD KEY — store a key in reader slot ${f.p2}`
      e.cls = 'reader-control'
      break
    case 0x86: // GENERAL AUTHENTICATE
      e.summary = 'GENERAL AUTHENTICATE — reader-side MIFARE authentication'
      e.cls = 'auth'
      break
    case 0x00: // direct transmit / reader escape
      describePcscDirect(e, f)
      break
    default:
      e.summary = `unrecognised PC/SC command (INS ${hb(f.ins)})`
      e.recognized = false
  }
}

function describePcscDirect(e: ApduExplanation, f: Fields): void {
  if (f.p1 === 0x40) {
    e.summary = 'reader LED/buzzer control (ACR122 escape)'
    e.cls = 'reader-control'
    return
  }
  if (f.p1 === 0x00) {
    if (f.data.length === 0) {
      e.summary = 'direct transmit — empty payload'
      e.recognized = false
      return
    }
    const inner = explain(f.data, true)
    e.summary = `direct transmit → ${inner.summary}`
    e.cls = inner.cls
    e.recognized = inner.recognized
    e.warnings.push(...inner.warnings)
    return
  }
  e.summary = `PC/SC direct command (P1 ${hb(f.p1)})`
  e.cls = 'reader-control'
}

function describeDesfire(e: ApduExplanation, f: Fields): void {
  e.recognized = true
  switch (f.ins) {
    case 0x60:
      e.summary = 'DESFire GetVersion — read chip version (continues with 91 AF)'
      e.cls = 'info'
      break
    case 0x6a:
      e.summary = 'DESFire GetApplicationIDs — list applications'
      e.cls = 'read'
      break
    case 0x6f:
      e.summary = 'DESFire GetFileIDs — list files in the selected application'
      e.cls = 'read'
      break
    case 0x5a:
      e.summary = `DESFire SelectApplication — select AID ${toHex(f.data, '')}`
      e.cls = 'select'
      break
    case 0xbd:
      e.summary = 'DESFire ReadData — read from a file'
      e.cls = 'read'
      break
    case 0x3d:
      e.summary = 'DESFire WriteData — write to a file'
      e.cls = 'write'
      break
    case 0x0a:
    case 0x1a:
    case 0xaa:
      e.summary = `DESFire Authenticate (0x${hb(f.ins)})`
      e.cls = 'auth'
      break
    case 0xaf:
      e.summary = 'DESFire AdditionalFrame — continue a chained command'
      e.cls = 'read'
      break
    default:
      e.summary = `unrecognised DESFire command (0x${hb(f.ins)})`
      e.recognized = false
  }
}

function describeIso(e: ApduExplanation, f: Fields): void {
  e.recognized = true
  switch (f.ins) {
    case 0xa4: // SELECT
      e.summary = `SELECT — ${describeSelect(f)}`
      e.cls = 'select'
      break
    case 0xb0: // READ BINARY
      e.summary = `READ BINARY — read ${f.hasLe ? f.le : 0} byte(s) at offset ${isoOffset(f)}`
      e.cls = 'read'
      break
    case 0xd6: // UPDATE BINARY
      e.summary = `UPDATE BINARY — write ${f.data.length} byte(s) at offset ${isoOffset(f)}`
      e.cls = 'write'
      break
    default:
      e.summary = `ISO 7816 command (INS ${hb(f.ins)})`
      e.recognized = false
  }
}

function describeSelect(f: Fields): string {
  let by: string
  switch (f.p1) {
    case 0x04:
      by = 'by name/AID'
      break
    case 0x00:
      by = 'by file identifier'
      break
    case 0x01:
      by = 'child DF'
      break
    case 0x02:
      by = 'child EF'
      break
    case 0x08:
      by = 'by path from MF'
      break
    case 0x09:
      by = 'by path from current DF'
      break
    default:
      by = `P1=${hb(f.p1)}`
  }
  const named = knownSelectTarget(f.data)
  if (named) return `${by}, ${named}`
  if (f.data.length > 0) return `${by} (${toHex(f.data, '')})`
  return by
}

function knownSelectTarget(data: Uint8Array): string {
  switch (toHex(data, '')) {
    case 'D2760000850101':
      return 'the NDEF Type 4 application'
    case 'E103':
      return 'the Capability Container file'
    case 'E104':
      return 'the NDEF file'
  }
  return ''
}

function isoOffset(f: Fields): number {
  return ((f.p1 & 0x7f) << 8) | f.p2
}

function flagPageWrite(e: ApduExplanation, page: number): void {
  if (page === 2 || page === 3) {
    e.warnings.push(
      `page ${page} holds lock/OTP bytes on NTAG/Ultralight; a write here can set permanent lock bits`,
    )
  }
}

function explainFraming(cmd: Uint8Array): ApduExplanation {
  const e: ApduExplanation = { summary: '', cls: 'unknown', mutating: false, warnings: [], recognized: true }
  const op = cmd[0]
  const arg = cmd.length >= 2 ? String(cmd[1]) : '?'
  switch (op) {
    case 0x30:
      e.summary = `native READ — Ultralight/NTAG page ${arg} (16 bytes), or MIFARE Classic block read`
      e.cls = 'read'
      break
    case 0x3a:
      e.summary = 'native FAST_READ — NTAG page range'
      e.cls = 'read'
      break
    case 0xa2:
      e.summary = `native WRITE — Ultralight/NTAG page ${arg} (4 bytes)`
      e.cls = 'write'
      if (cmd.length >= 2) flagPageWrite(e, cmd[1])
      break
    case 0xa0:
      e.summary = 'native WRITE — MIFARE Classic block, or Ultralight compatibility write'
      e.cls = 'write'
      break
    case 0x60:
      e.summary = 'native GET_VERSION (NTAG/Ultralight), or MIFARE Classic AUTH key A — ambiguous without the tag type'
      e.cls = 'info'
      break
    case 0x61:
      e.summary = 'MIFARE Classic AUTH key B'
      e.cls = 'auth'
      break
    case 0x1b:
      e.summary = 'native PWD_AUTH — NTAG password authentication'
      e.cls = 'auth'
      break
    case 0x3c:
      e.summary = 'native READ_SIG — NTAG originality signature'
      e.cls = 'info'
      break
    case 0x39:
      e.summary = 'native READ_CNT — NTAG counter'
      e.cls = 'read'
      break
    case 0x50:
      e.summary = 'native HALT'
      e.cls = 'info'
      break
    default:
      e.summary = `unrecognised framing command (opcode ${hb(op)})`
      e.recognized = false
  }
  finalize(e)
  return e
}

/** Settles the derived fields once the class is known: what mutates, and why. */
function finalize(e: ApduExplanation): void {
  if (e.cls === 'write') {
    e.mutating = true
    e.warnings.push('writes to the tag; a write to a configuration or OTP page is not reversible')
  } else if (e.cls === 'lock') {
    e.mutating = true
    e.warnings.push('makes part of the tag permanently read-only; not reversible')
  } else if (e.cls === 'unknown') {
    e.mutating = true
    if (!e.recognized) {
      e.warnings.push('unrecognised command; the agent cannot tell whether it writes or locks')
    }
  }
}

function dedupe(list: string[]): string[] {
  return list.filter((s, i) => list.indexOf(s) === i)
}

function hb(b: number): string {
  return b.toString(16).toUpperCase().padStart(2, '0')
}
