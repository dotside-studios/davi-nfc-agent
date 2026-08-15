import { useEffect, useMemo, useRef, useState } from 'react'
import type { LiveEvent } from '../types'
import type { TagLink } from '../useTags'
import { fmtTime } from '../format'
import { Empty, Notice, Panel } from '../ui'

const KINDS: { key: LiveEvent['kind']; label: string }[] = [
  { key: 'scan', label: 'scans' },
  { key: 'write', label: 'writes' },
  { key: 'lock', label: 'locks' },
  { key: 'apdu', label: 'apdu' },
  { key: 'status', label: 'status' },
  { key: 'error', label: 'errors' },
  { key: 'removed', label: 'removals' },
]

export function Live({
  events,
  link,
  onClear,
}: {
  events: LiveEvent[]
  link: TagLink
  onClear: () => void
}) {
  const [hidden, setHidden] = useState<Set<LiveEvent['kind']>>(new Set())
  const [paused, setPaused] = useState(false)
  const [frozen, setFrozen] = useState<LiveEvent[] | null>(null)
  const view = useRef<HTMLDivElement>(null)

  // Snapshots on pause so a line being read cannot scroll away. Not dependent
  // on `events` — re-snapshotting on each event would defeat the pause.
  useEffect(() => {
    setFrozen(paused ? events : null)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [paused])

  const source = frozen ?? events

  const filtered = useMemo(() => source.filter((e) => !hidden.has(e.kind)), [source, hidden])

  useEffect(() => {
    if (paused) return
    const el = view.current
    if (el) el.scrollTop = el.scrollHeight
  }, [filtered, paused])

  const toggle = (kind: LiveEvent['kind']) =>
    setHidden((prev) => {
      const next = new Set(prev)
      if (next.has(kind)) next.delete(kind)
      else next.add(kind)
      return next
    })

  return (
    <Panel
      title={
        <>
          Live events <span className="dim">({filtered.length} shown)</span>
        </>
      }
      tools={
        <>
          {KINDS.map((k) => (
            <label key={k.key} className="nowrap">
              <input type="checkbox" checked={!hidden.has(k.key)} onChange={() => toggle(k.key)} /> {k.label}
            </label>
          ))}
          <span className="sep">|</span>
          <button type="button" className="link" onClick={() => setPaused((p) => !p)}>
            {paused ? 'resume' : 'pause'}
          </button>
          <button type="button" className="link" onClick={() => download(filtered)}>
            export
          </button>
          <button type="button" className="link" onClick={onClear}>
            clear
          </button>
        </>
      }
      flush
      fill
    >
      {link !== 'open' ? (
        <div className="body">
          <Notice kind="warn">
            Not connected to the agent's client endpoint
            {link === 'connecting' ? ' — connecting…' : ' — reconnecting…'}. Tag activity will not
            appear until it is.
          </Notice>
        </div>
      ) : null}

      {paused ? (
        <div className="body">
          <Notice>Feed paused. New events are still being recorded and will appear on resume.</Notice>
        </div>
      ) : null}

      {filtered.length === 0 ? (
        <Empty>
          {events.length === 0
            ? 'Nothing yet. Present a tag to the reader.'
            : 'Every event type is hidden by the filters above.'}
        </Empty>
      ) : (
        <div className="logview" ref={view}>
          {filtered.map((e) => (
            <div className="logrow" key={e.id}>
              <span className="t">{fmtTime(e.at)}</span>
              <span className={`lvl ${kindClass(e)}`}>{e.kind}</span>
              <span className="msg">
                {e.summary}
                {e.detail ? <span className="dim"> · {e.detail}</span> : null}
              </span>
            </div>
          ))}
        </div>
      )}
    </Panel>
  )
}

function kindClass(e: LiveEvent): string {
  if (e.kind === 'error' || e.ok === false) return 'err'
  if (e.kind === 'lock') return 'warn'
  if (e.kind === 'apdu') return 'dim'
  if (e.kind === 'scan' || e.kind === 'write') return 'ok'
  return 'dim'
}

/** Exports as NDJSON: one event per line, greppable and paste-safe. */
function download(events: LiveEvent[]) {
  const body = events.map((e) => JSON.stringify(e)).join('\n')
  const url = URL.createObjectURL(new Blob([body], { type: 'application/x-ndjson' }))
  const a = document.createElement('a')
  a.href = url
  a.download = `nfc-events-${new Date().toISOString().replace(/[:.]/g, '-')}.ndjson`
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}
