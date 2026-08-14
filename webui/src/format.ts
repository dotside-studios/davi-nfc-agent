/** Display formatting. Kept apart from ui.tsx so that file exports only
 *  components, which is what lets fast refresh work on it. */

export function fmtDuration(seconds: number): string {
  if (seconds < 60) return `${Math.floor(seconds)}s`
  const m = Math.floor(seconds / 60)
  if (m < 60) return `${m}m ${Math.floor(seconds % 60)}s`
  const h = Math.floor(m / 60)
  if (h < 24) return `${h}h ${m % 60}m`
  return `${Math.floor(h / 24)}d ${h % 24}h`
}

export function fmtTime(iso: string): string {
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  return d.toLocaleTimeString(undefined, { hour12: false })
}

export function fmtDateTime(iso?: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime())) return iso
  // A zero time from Go marshals as year 1; it means "never", not a date.
  if (d.getUTCFullYear() <= 1) return 'never'
  return d.toLocaleString(undefined, { hour12: false })
}

export function fmtBytes(n?: number): string {
  if (n === undefined || n === null) return '—'
  if (n < 1024) return `${n} B`
  return `${(n / 1024).toFixed(1)} KB`
}

export function fmtRelative(iso?: string): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (Number.isNaN(d.getTime()) || d.getUTCFullYear() <= 1) return 'never'
  const secs = (Date.now() - d.getTime()) / 1000
  if (secs < 5) return 'just now'
  if (secs < 60) return `${Math.floor(secs)}s ago`
  if (secs < 3600) return `${Math.floor(secs / 60)}m ago`
  if (secs < 86400) return `${Math.floor(secs / 3600)}h ago`
  return `${Math.floor(secs / 86400)}d ago`
}

/** Human name for a reader mode. */
export function modeLabel(mode: string): string {
  switch (mode) {
    case 'read':
      return 'Read only'
    case 'write':
      return 'Write only'
    default:
      return 'Read/Write'
  }
}
