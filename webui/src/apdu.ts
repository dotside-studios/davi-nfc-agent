/** Hex, base64 and ISO 7816 status words for the APDU console. */

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

export function toBase64(bytes: Uint8Array): string {
  let binary = ''
  for (const b of bytes) binary += String.fromCharCode(b)
  return btoa(binary)
}

export function fromBase64(value: string): Uint8Array {
  const binary = atob(value)
  const out = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) out[i] = binary.charCodeAt(i)
  return out
}

export interface StatusWord {
  sw: string
  ok: boolean
  meaning: string
}

/**
 * Interprets the last two bytes as an ISO 7816 status word.
 *
 * Only meaningful for APDU-level exchanges — a framing-level response has no
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
    0x6982: 'Security status not satisfied — authenticate first',
    0x6983: 'Authentication method blocked',
    0x6984: 'Referenced data invalidated',
    0x6985: 'Conditions of use not satisfied',
    0x6986: 'Command not allowed — no current file selected',
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
  if (sw1 === 0x61) return `Success — ${sw2} more byte(s) available (GET RESPONSE)`
  if (sw1 === 0x6c) return `Wrong Le — retry with Le = ${sw2}`
  if (sw1 === 0x63 && (sw2 & 0xf0) === 0xc0) return `Authentication failed — ${sw2 & 0x0f} attempt(s) left`

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
