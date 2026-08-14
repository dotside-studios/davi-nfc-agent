import type { WriteRecord } from './types'

/**
 * Record kinds the composer can build and the fields each uses, mirroring the
 * Record Fields table in docs/api.md. Data rather than form logic, so adding a
 * kind is one entry here.
 */
export interface RecordKind {
  type: string
  label: string
  hint: string
  /** Label for the main `content` field, or null if the kind has no content. */
  content: string | null
  placeholder?: string
  language?: boolean
  title?: boolean
  mime?: boolean
  payload?: boolean
  raw?: boolean
}

export const RECORD_KINDS: RecordKind[] = [
  {
    type: 'text',
    label: 'Text',
    hint: 'Plain text with a language code.',
    content: 'Text',
    placeholder: 'Hello, NFC!',
    language: true,
  },
  {
    type: 'uri',
    label: 'URI / URL',
    hint: 'The common prefixes are abbreviated automatically to save tag space.',
    content: 'URI',
    placeholder: 'https://example.com',
  },
  {
    type: 'smartposter',
    label: 'Smart poster',
    hint: 'A URI with a display label — "tap to open <title>".',
    content: 'URI',
    placeholder: 'https://example.com',
    title: true,
    language: true,
  },
  {
    type: 'aar',
    label: 'Android app record',
    hint: 'Launches (or offers to install) an Android app when the tag is read.',
    content: 'Package name',
    placeholder: 'com.example.app',
  },
  {
    type: 'mailto',
    label: 'Email',
    hint: 'Written as a mailto: URI. The scheme is added if absent.',
    content: 'Address',
    placeholder: 'someone@example.com',
  },
  {
    type: 'tel',
    label: 'Phone',
    hint: 'Written as a tel: URI.',
    content: 'Number',
    placeholder: '+15551234567',
  },
  {
    type: 'sms',
    label: 'SMS',
    hint: 'Written as an sms: URI.',
    content: 'Number',
    placeholder: '+15551234567',
  },
  {
    type: 'geo',
    label: 'Location',
    hint: 'Written as a geo: URI.',
    content: 'Coordinates',
    placeholder: '37.7749,-122.4194',
  },
  {
    type: 'vcard',
    label: 'Contact (vCard)',
    hint: 'A text/vcard record. Paste vCard text, or supply base64 bytes.',
    content: 'vCard text',
    placeholder: 'BEGIN:VCARD\nVERSION:3.0\nFN:Jane Doe\nEND:VCARD',
    payload: true,
  },
  {
    type: 'mime',
    label: 'MIME',
    hint: 'Arbitrary media record. WiFi credentials go here as application/vnd.wfa.wsc.',
    content: 'Content',
    mime: true,
    payload: true,
  },
  {
    type: 'external',
    label: 'External type',
    hint: 'An NFC Forum external type, written as domain:type.',
    content: 'Type',
    placeholder: 'example.com:mytype',
    payload: true,
  },
  {
    type: 'raw',
    label: 'Raw record',
    hint: 'Fully custom: you supply the TNF, type bytes and payload.',
    content: null,
    raw: true,
    payload: true,
  },
  {
    type: 'empty',
    label: 'Empty (erase)',
    hint: 'Writes an empty NDEF message, blanking the tag. Reversible.',
    content: null,
  },
]

export function kindOf(type: string): RecordKind {
  return RECORD_KINDS.find((k) => k.type === type) ?? RECORD_KINDS[0]
}

/**
 * Estimates a record's encoded size, so the composer can show the cost while it
 * is being typed. The agent does the authoritative check; this deliberately
 * over-estimates at the margins.
 */
export function estimateSize(r: WriteRecord): number {
  const bytes = (s?: string) => (s ? new TextEncoder().encode(s).length : 0)
  const b64 = (s?: string) => (s ? Math.floor((s.length * 3) / 4) : 0)

  // Flags + type length + payload length + type bytes, assuming the 4-byte form.
  const HEADER = 6

  switch (r.type) {
    case 'text':
      return HEADER + 1 + bytes(r.language || 'en') + bytes(r.content)

    case 'uri':
    case 'mailto':
    case 'tel':
    case 'sms':
    case 'geo':
      return HEADER + 1 + Math.max(0, bytes(r.content) - abbreviationSaving(r.content))

    case 'smartposter': {
      // Nests a URI record and a text record inside its payload.
      const uri = HEADER + 1 + Math.max(0, bytes(r.content) - abbreviationSaving(r.content))
      const title = r.title ? HEADER + 1 + bytes(r.language || 'en') + bytes(r.title) : 0
      return HEADER + uri + title
    }

    case 'aar':
      return HEADER + bytes('android.com:pkg') + bytes(r.content)

    case 'vcard':
      return HEADER + bytes('text/vcard') + (r.payload ? b64(r.payload) : bytes(r.content))

    case 'mime':
      return HEADER + bytes(r.mimeType) + (r.payload ? b64(r.payload) : bytes(r.content))

    case 'external':
      return HEADER + bytes(r.content) + b64(r.payload)

    case 'raw':
      return HEADER + b64(r.typeBytes) + b64(r.id) + b64(r.payload)

    case 'empty':
      return 3

    default:
      return HEADER + bytes(r.content)
  }
}

/** Bytes saved by the NDEF URI prefix abbreviation, if one applies. */
function abbreviationSaving(uri?: string): number {
  if (!uri) return 0
  const prefixes = [
    'https://www.',
    'http://www.',
    'https://',
    'http://',
    'tel:',
    'mailto:',
    'ftp://',
    'sftp://',
    'file://',
  ]
  for (const p of prefixes) {
    if (uri.toLowerCase().startsWith(p)) return p.length - 1
  }
  return 0
}

/** Total encoded size of a message, including its terminator. */
export function estimateMessageSize(records: WriteRecord[]): number {
  if (records.length === 0) return 0
  return records.reduce((sum, r) => sum + estimateSize(r), 0)
}

/** Strips fields the kind does not use, so switching kind leaves no leftovers. */
export function cleanRecord(r: WriteRecord): WriteRecord {
  const kind = kindOf(r.type)
  const out: WriteRecord = { type: r.type }

  if (kind.content !== null && r.content) out.content = r.content
  if (kind.language && r.language) out.language = r.language
  if (kind.title && r.title) out.title = r.title
  if (kind.mime && r.mimeType) out.mimeType = r.mimeType
  if (kind.payload && r.payload) out.payload = r.payload
  if (kind.raw) {
    if (r.tnf !== undefined) out.tnf = r.tnf
    if (r.typeBytes) out.typeBytes = r.typeBytes
    if (r.id) out.id = r.id
  }
  return out
}

/** A one-line description of a record, for the summary list. */
export function describeRecord(r: WriteRecord): string {
  const kind = kindOf(r.type)
  const value = r.content || r.title || r.mimeType || (r.payload ? '(payload)' : '')
  return value ? `${kind.label}: ${value}` : kind.label
}
