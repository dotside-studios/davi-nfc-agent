import { useEffect, useMemo, useRef, useState } from 'react'
import type { LogEntry, LogLevel } from '../types'
import { fmtTime } from '../format'
import { Empty, Panel } from '../ui'

/**
 * The agent's log output. Previously this went to stderr and nowhere else, so
 * for a tray app started from a launcher it was discarded as it was produced.
 */
export function Logs({ logs, onClear }: { logs: LogEntry[]; onClear: () => void }) {
  const [level, setLevel] = useState<LogLevel | 'all'>('all')
  const [query, setQuery] = useState('')
  const [follow, setFollow] = useState(true)

  const view = useRef<HTMLDivElement>(null)

  const filtered = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return logs.filter((e) => {
      if (level !== 'all' && e.level !== level) return false
      if (!needle) return true
      return e.message.toLowerCase().includes(needle) || (e.source ?? '').toLowerCase().includes(needle)
    })
  }, [logs, level, query])

  useEffect(() => {
    if (!follow) return
    const el = view.current
    if (el) el.scrollTop = el.scrollHeight
  }, [filtered, follow])

  const counts = useMemo(() => {
    let warn = 0
    let error = 0
    for (const e of logs) {
      if (e.level === 'warn') warn++
      else if (e.level === 'error') error++
    }
    return { warn, error }
  }, [logs])

  return (
    <Panel
      title={
        <>
          Log <span className="dim">({logs.length} lines</span>
          {counts.error > 0 ? <span className="err"> · {counts.error} errors</span> : null}
          {counts.warn > 0 ? <span className="warn"> · {counts.warn} warnings</span> : null}
          <span className="dim">)</span>
        </>
      }
      tools={
        <>
          <select value={level} onChange={(e) => setLevel(e.target.value as LogLevel | 'all')}>
            <option value="all">all levels</option>
            <option value="error">errors</option>
            <option value="warn">warnings</option>
            <option value="info">info</option>
          </select>
          <input
            type="search"
            value={query}
            placeholder="filter…"
            onChange={(e) => setQuery(e.target.value)}
            style={{ width: 160 }}
          />
          <label className="nowrap">
            <input type="checkbox" checked={follow} onChange={(e) => setFollow(e.target.checked)} /> follow
          </label>
          <button type="button" className="link" onClick={() => download(filtered)}>
            download
          </button>
          <button type="button" className="link" onClick={onClear}>
            clear view
          </button>
        </>
      }
      flush
      fill
    >
      {filtered.length === 0 ? (
        <Empty>
          {logs.length === 0
            ? 'Nothing captured yet. Log lines appear here as the agent produces them.'
            : 'No lines match the current filter.'}
        </Empty>
      ) : (
        <div
          className="logview"
          ref={view}
          onScroll={(e) => {
            // Scrolling up means reading history; don't yank back to the bottom.
            const el = e.currentTarget
            const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 24
            if (atBottom !== follow) setFollow(atBottom)
          }}
        >
          {filtered.map((e) => (
            <div className="logrow" key={e.seq}>
              <span className="t">{fmtTime(e.time)}</span>
              <span className={`lvl ${levelClass(e.level)}`}>{e.level}</span>
              {e.source ? <span className="src">{e.source}</span> : null}
              <span className="msg">{e.message}</span>
            </div>
          ))}
        </div>
      )}
    </Panel>
  )
}

function levelClass(level: LogLevel): string {
  if (level === 'error') return 'err'
  if (level === 'warn') return 'warn'
  return 'dim'
}

/** Writes the visible lines out as text, for a bug report. */
function download(entries: LogEntry[]) {
  const body = entries
    .map((e) => `${e.time} ${e.level.toUpperCase()} ${e.source ?? ''} ${e.message}`.replace(/\s+/g, ' '))
    .join('\n')

  const url = URL.createObjectURL(new Blob([body], { type: 'text/plain' }))
  const a = document.createElement('a')
  a.href = url
  a.download = `nfc-agent-log-${new Date().toISOString().replace(/[:.]/g, '-')}.txt`
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}
