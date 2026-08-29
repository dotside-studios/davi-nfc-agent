import { useCallback, useEffect, useRef, useState } from 'react'
import { APDU_PRESETS, parseHex, readStatusWord, toAscii, toHex } from '../apdu'
import { fmtTime } from '../format'
import type { Exchange } from '../types'
import type { Tags } from '../useTags'
import { Empty, Notice, Panel } from '../ui'

/** Raw exchanges with the present tag, over the client transceive channel. */
export function Apdu({
  tags,
  writable,
  rawAllowed,
}: {
  tags: Tags
  writable: boolean
  rawAllowed: boolean
}) {
  const [command, setCommand] = useState('FF CA 00 00 00')
  const [raw, setRaw] = useState(false)
  const [history, setHistory] = useState<Exchange[]>([])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const seq = useRef(0)
  const view = useRef<HTMLDivElement>(null)

  const parsed = parseHex(command)
  const canSend = parsed !== null && !busy && writable && rawAllowed

  useEffect(() => {
    const el = view.current
    if (el) el.scrollTop = el.scrollHeight
  }, [history])

  const send = useCallback(async () => {
    const bytes = parseHex(command)
    if (!bytes) return

    setBusy(true)
    setError(null)
    const started = performance.now()

    try {
      const data = await tags.transceive(bytes, raw)
      seq.current += 1
      setHistory((prev) => [
        ...prev,
        {
          id: seq.current,
          at: new Date().toISOString(),
          command: toHex(bytes),
          response: toHex(data),
          raw,
          ok: true,
          elapsedMs: Math.round(performance.now() - started),
        },
      ])
    } catch (err) {
      const message = err instanceof Error ? err.message : String(err)
      seq.current += 1
      setHistory((prev) => [
        ...prev,
        {
          id: seq.current,
          at: new Date().toISOString(),
          command: toHex(bytes),
          raw,
          ok: false,
          error: message,
          elapsedMs: Math.round(performance.now() - started),
        },
      ])
      setError(message)
    } finally {
      setBusy(false)
    }
  }, [command, raw, tags])

  return (
    <Panel
      title="Raw exchange (APDU)"
      tools={
        history.length > 0 ? (
          <>
            <button type="button" className="link" onClick={() => download(history)}>
              export
            </button>
            <button type="button" className="link" onClick={() => setHistory([])}>
              clear
            </button>
          </>
        ) : null
      }
    >
      <Notice kind="warn">
        These bytes reach the tag unmodified. A raw command can write to configuration pages, burn
        one-time-programmable bits or lock a tag permanently, none of which the agent can recognise
        or undo. Know what you are sending.
      </Notice>

      {!rawAllowed ? (
        <Notice kind="err">
          The <b>raw APDU channel</b> is off, so raw exchanges are refused. Turn it on under the
          reader controls on the Overview tab. It is off by default because the agent cannot vet what
          a raw command does.
        </Notice>
      ) : null}

      {!writable ? (
        <Notice kind="err">
          The reader is in <b>read-only</b> mode, so raw exchanges are refused: the agent cannot
          tell a SELECT from a write to a config page, so it treats them all as writes.
        </Notice>
      ) : null}

      {!tags.tag ? <Notice>No tag on the reader. The exchange will fail until one is present.</Notice> : null}

      <label className="stack" style={{ marginTop: 4 }}>
        <span className="dim">Command (hex)</span>
        <input
          type="text"
          className="mono"
          value={command}
          spellCheck={false}
          placeholder="00 A4 04 00 07 D2 76 00 00 85 01 01"
          onChange={(e) => setCommand(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === 'Enter' && canSend) void send()
          }}
        />
      </label>

      <div className="row" style={{ marginTop: 4 }}>
        <button type="button" onClick={() => void send()} disabled={!canSend}>
          {busy ? 'sending…' : 'Send'}
        </button>
        <label className="nowrap" title="Framing-level (NfcA.transceive) rather than APDU-level (IsoDep)">
          <input type="checkbox" checked={raw} onChange={(e) => setRaw(e.target.checked)} /> raw framing
        </label>
        <span className="dim">
          {parsed ? `${parsed.length} byte${parsed.length === 1 ? '' : 's'}` : null}
          {command.trim() && !parsed ? <span className="err">not valid hex</span> : null}
        </span>
      </div>

      <div className="row" style={{ marginTop: 4 }}>
        <span className="dim nowrap">Presets</span>
        {APDU_PRESETS.map((p) => (
          <button
            key={p.label}
            type="button"
            className="link"
            title={p.note}
            onClick={() => {
              setCommand(p.hex)
              setRaw(Boolean(p.raw))
            }}
          >
            {p.label}
          </button>
        ))}
      </div>

      {error ? <div className="err" style={{ marginTop: 4 }}>{error}</div> : null}

      <div style={{ marginTop: 6 }}>
        {history.length === 0 ? (
          <Empty>No exchanges yet.</Empty>
        ) : (
          <div className="logview short" ref={view}>
            {history.map((x) => (
              <ExchangeRow key={x.id} exchange={x} />
            ))}
          </div>
        )}
      </div>
    </Panel>
  )
}

function ExchangeRow({ exchange }: { exchange: Exchange }) {
  const bytes = exchange.response ? parseHex(exchange.response) : null
  // Only APDU-level replies carry a status word; framing-level ones do not.
  const status = bytes && !exchange.raw ? readStatusWord(bytes) : null

  return (
    <>
      <div className="logrow">
        <span className="t">{fmtTime(exchange.at)}</span>
        <span className="lvl dim">&gt;&gt;</span>
        <span className="msg">
          {exchange.command}
          {exchange.raw ? <span className="dim"> (raw)</span> : null}
        </span>
      </div>
      <div className="logrow">
        <span className="t" />
        <span className={`lvl ${exchange.ok ? 'ok' : 'err'}`}>&lt;&lt;</span>
        <span className="msg">
          {exchange.ok ? (
            <>
              {exchange.response || <span className="dim">(no data)</span>}
              {bytes && bytes.length > 0 ? (
                <span className="dim"> · {toAscii(bytes)}</span>
              ) : null}
              {status ? (
                <span className={status.ok ? 'ok' : 'warn'}>
                  {' '}
                  · {status.sw} {status.meaning}
                </span>
              ) : null}
              <span className="dim"> · {exchange.elapsedMs}ms</span>
            </>
          ) : (
            <span className="err">{exchange.error}</span>
          )}
        </span>
      </div>
    </>
  )
}

/** Exports as text, so an exchange can go straight into a bug report. */
function download(history: Exchange[]) {
  const body = history
    .map((x) =>
      [
        `${x.at} ${x.raw ? '[raw] ' : ''}>> ${x.command}`,
        `${' '.repeat(24)}<< ${x.ok ? x.response : `ERROR: ${x.error}`} (${x.elapsedMs}ms)`,
      ].join('\n'),
    )
    .join('\n')

  const url = URL.createObjectURL(new Blob([body], { type: 'text/plain' }))
  const a = document.createElement('a')
  a.href = url
  a.download = `apdu-${new Date().toISOString().replace(/[:.]/g, '-')}.txt`
  document.body.appendChild(a)
  a.click()
  document.body.removeChild(a)
  URL.revokeObjectURL(url)
}
