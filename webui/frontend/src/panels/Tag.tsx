import { useEffect, useMemo, useState } from 'react'
import { RECORD_KINDS, cleanRecord, describeRecord, estimateMessageSize, kindOf } from '../ndef'
import type { NdefRecord, WriteRecord } from '../types'
import type { Tags } from '../useTags'
import { fmtBytes, fmtDateTime } from '../format'
import { ActionLink, Copyable, Dot, Empty, KV, Notice, Panel, Row } from '../ui'
import { Apdu } from './Apdu'

/** Tag inspector and NDEF composer. */
export function Tag({ tags, writable }: { tags: Tags; writable: boolean }) {
  const { tag, capabilities } = tags

  return (
    <div className="aside-main even">
      <div>
        <Inspector tags={tags} />
        {capabilities ? <Capabilities tags={tags} /> : null}
      </div>
      <div>
        {tag ? <Records records={tag.message?.records} text={tag.text} /> : null}
        <Composer tags={tags} writable={writable} />
        <Apdu tags={tags} writable={writable} />
      </div>
    </div>
  )
}

function Inspector({ tags }: { tags: Tags }) {
  const { tag } = tags

  if (!tag) {
    return (
      <Panel title="Tag">
        <Empty>No tag on the reader. Present one to inspect it.</Empty>
      </Panel>
    )
  }

  return (
    <Panel
      title="Tag"
      tools={<ActionLink run={() => tags.refreshCapabilities()}>re-read</ActionLink>}
    >
      {tag.err ? <Notice kind="err">Read failed: {tag.err}</Notice> : null}
      <KV>
        <Row label="UID">
          <Copyable value={tag.uid} />
        </Row>
        <Row label="Type">{tag.type || <span className="dim">unknown</span>}</Row>
        <Row label="Technology">{tag.technology || <span className="dim">—</span>}</Row>
        <Row label="Scanned">{fmtDateTime(tag.scannedAt)}</Row>
        <Row label="Text">
          {tag.text ? <span className="mono">{tag.text}</span> : <span className="dim">no text content</span>}
        </Row>
      </KV>
    </Panel>
  )
}

function Capabilities({ tags }: { tags: Tags }) {
  const caps = tags.capabilities
  if (!caps) return null

  const memory = num(caps.memorySize)
  const usable = num(caps.usableCapacity)

  return (
    <Panel title="Capabilities">
      <KV>
        <Row label="Memory">{fmtBytes(memory)}</Row>
        <Row label="Usable">
          {fmtBytes(usable)}
          {memory && usable ? <span className="dim"> of {fmtBytes(memory)}</span> : null}
        </Row>
        <Row label="Writable">
          {caps.readOnly ? (
            <Dot state="err">no — tag is locked read-only</Dot>
          ) : caps.writable === false ? (
            <Dot state="err">no</Dot>
          ) : (
            <Dot state="ok">yes</Dot>
          )}
        </Row>
        <Row label="Lockable">
          {caps.lockable ? <Dot state="ok">yes</Dot> : <Dot state="off">no</Dot>}
        </Row>
        <Row label="Password">
          {caps.passwordProtectable ? <Dot state="ok">supported</Dot> : <Dot state="off">not supported</Dot>}
        </Row>
      </KV>
    </Panel>
  )
}

function Records({ records, text }: { records?: NdefRecord[]; text?: string }) {
  if (!records || records.length === 0) {
    return (
      <Panel title="NDEF message">
        {text ? (
          <div className="mono">{text}</div>
        ) : (
          <Empty>No NDEF records — the tag is empty or unformatted.</Empty>
        )}
      </Panel>
    )
  }

  return (
    <Panel title={`NDEF message (${records.length} record${records.length === 1 ? '' : 's'})`} flush>
      <div className="tw">
        <table className="grid">
          <thead>
            <tr>
              <th>#</th>
              <th>TNF</th>
              <th>Type</th>
              <th>Value</th>
              <th>Payload</th>
            </tr>
          </thead>
          <tbody>
            {records.map((r, i) => (
              <tr key={i}>
                <td className="num">{i + 1}</td>
                <td className="mono">{r.tnf ?? '—'}</td>
                <td className="mono">{r.type ?? '—'}</td>
                <td className="mono">{r.text ?? r.uri ?? <span className="dim">—</span>}</td>
                <td className="mono dim">{truncate(r.payload)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Panel>
  )
}

/* ---- composer ---- */

const BLANK: WriteRecord = { type: 'text', content: '', language: 'en' }

function Composer({ tags, writable }: { tags: Tags; writable: boolean }) {
  const [records, setRecords] = useState<WriteRecord[]>([{ ...BLANK }])
  const [verifyLock, setVerifyLock] = useState(false)
  const [result, setResult] = useState<{ ok: boolean; text: string } | null>(null)

  const caps = tags.capabilities
  const capacity = num(caps?.usableCapacity) ?? num(caps?.memorySize)
  const size = useMemo(() => estimateMessageSize(records.map(cleanRecord)), [records])
  const over = capacity !== undefined && size > capacity
  const readOnly = caps?.readOnly === true || caps?.writable === false

  // A result refers to the message that produced it.
  useEffect(() => setResult(null), [records])

  const update = (i: number, patch: Partial<WriteRecord>) =>
    setRecords((prev) => prev.map((r, n) => (n === i ? { ...r, ...patch } : r)))

  const write = async (lock: boolean) => {
    const payload = records.map(cleanRecord)
    try {
      const res = await tags.write(payload, lock)
      setResult({ ok: true, text: String(res.message ?? 'Write succeeded.') })
    } catch (err) {
      setResult({ ok: false, text: err instanceof Error ? err.message : String(err) })
      throw err
    }
  }

  return (
    <Panel
      title="Write"
      tools={
        <>
          <button
            type="button"
            className="link"
            onClick={() => setRecords((r) => [...r, { ...BLANK }])}
          >
            add record
          </button>
          {records.length > 1 ? (
            <button type="button" className="link" onClick={() => setRecords([{ ...BLANK }])}>
              reset
            </button>
          ) : null}
        </>
      }
    >
      {!writable ? (
        <Notice kind="warn">
          The reader is in <b>read-only</b> mode, so writes will be refused. Change it under Settings.
        </Notice>
      ) : null}

      {readOnly ? (
        <Notice kind="err">This tag is locked read-only and cannot be written.</Notice>
      ) : null}

      {!tags.tag ? (
        <Notice>
          No tag present. Compose the message now — it is written to the next tag you present.
        </Notice>
      ) : null}

      <div className="stack">
        {records.map((record, i) => (
          <RecordEditor
            key={i}
            index={i}
            record={record}
            onChange={(patch) => update(i, patch)}
            onRemove={
              records.length > 1
                ? () => setRecords((prev) => prev.filter((_, n) => n !== i))
                : undefined
            }
          />
        ))}
      </div>

      <div className="row" style={{ marginTop: 6 }}>
        <span className="nowrap dim">
          {size} B{capacity !== undefined ? ` of ${capacity} B` : ''}
        </span>
        {capacity !== undefined ? (
          <div className={over ? 'meter over' : 'meter'}>
            <span style={{ width: `${Math.min(100, (size / capacity) * 100)}%` }} />
          </div>
        ) : (
          <span className="dim">present a tag to see its capacity</span>
        )}
      </div>

      {over ? (
        <Notice kind="err">
          This message is larger than the tag's usable capacity. The agent checks before writing, so
          it will be refused rather than truncated.
        </Notice>
      ) : null}

      <div className="row" style={{ marginTop: 6 }}>
        <ActionLink run={() => write(false)} disabled={over || readOnly || !writable}>
          Write to tag
        </ActionLink>
        <span className="sep">|</span>
        <label className="nowrap">
          <input type="checkbox" checked={verifyLock} onChange={(e) => setVerifyLock(e.target.checked)} />{' '}
          lock after writing
        </label>
        {verifyLock ? (
          <ActionLink
            danger
            run={() => write(true)}
            disabled={over || readOnly || !writable}
            confirm={{
              prompt:
                'Write and then permanently lock this tag?\n\nLocking is irreversible. The tag can never be written again by anyone.',
              phrase: 'lock',
            }}
          >
            Write and lock (permanent)
          </ActionLink>
        ) : null}
      </div>

      <div className="row" style={{ marginTop: 6 }}>
        <ActionLink
          run={() => tags.write([{ type: 'empty' }], false)}
          disabled={readOnly || !writable}
          confirm={{ prompt: "Erase this tag? Its NDEF message is blanked. This can be written again afterwards." }}
        >
          erase tag
        </ActionLink>
        <span className="sep">|</span>
        <ActionLink
          danger
          run={() => tags.lock()}
          disabled={readOnly || !writable}
          confirm={{
            prompt:
              'Permanently lock this tag without writing?\n\nThis is irreversible. The tag keeps its current contents and can never be written again.',
            phrase: 'lock',
          }}
        >
          lock tag (permanent)
        </ActionLink>
      </div>

      {result ? (
        <Notice kind={result.ok ? undefined : 'err'}>{result.text}</Notice>
      ) : null}

      <div className="dim" style={{ marginTop: 4 }}>
        A write replaces the whole message. Size is an estimate for guidance; the agent performs the
        authoritative capacity check and verifies by reading back what it wrote.
      </div>
    </Panel>
  )
}

function RecordEditor({
  index,
  record,
  onChange,
  onRemove,
}: {
  index: number
  record: WriteRecord
  onChange: (patch: Partial<WriteRecord>) => void
  onRemove?: () => void
}) {
  const kind = kindOf(record.type)

  return (
    <fieldset style={{ border: '1px solid var(--border-soft)', padding: 6, margin: 0 }}>
      <legend className="dim">
        #{index + 1} {describeRecord(record)}
      </legend>

      <div className="row">
        <select value={record.type} onChange={(e) => onChange({ type: e.target.value })}>
          {RECORD_KINDS.map((k) => (
            <option key={k.type} value={k.type}>
              {k.label}
            </option>
          ))}
        </select>
        {onRemove ? (
          <button type="button" className="link danger" onClick={onRemove}>
            remove
          </button>
        ) : null}
      </div>

      <div className="dim" style={{ margin: '2px 0 4px' }}>
        {kind.hint}
      </div>

      {kind.content !== null ? (
        <label className="stack" style={{ marginBottom: 4 }}>
          <span className="dim">{kind.content}</span>
          {kind.type === 'vcard' ? (
            <textarea
              rows={4}
              value={record.content ?? ''}
              placeholder={kind.placeholder}
              onChange={(e) => onChange({ content: e.target.value })}
            />
          ) : (
            <input
              type="text"
              value={record.content ?? ''}
              placeholder={kind.placeholder}
              onChange={(e) => onChange({ content: e.target.value })}
            />
          )}
        </label>
      ) : null}

      <div className="row">
        {kind.title ? (
          <label className="row tight">
            <span className="dim">Title</span>
            <input
              type="text"
              value={record.title ?? ''}
              placeholder="Example site"
              onChange={(e) => onChange({ title: e.target.value })}
            />
          </label>
        ) : null}

        {kind.language ? (
          <label className="row tight">
            <span className="dim">Language</span>
            <input
              type="text"
              value={record.language ?? 'en'}
              size={4}
              onChange={(e) => onChange({ language: e.target.value })}
            />
          </label>
        ) : null}

        {kind.mime ? (
          <label className="row tight">
            <span className="dim">MIME type</span>
            <input
              type="text"
              value={record.mimeType ?? ''}
              placeholder="application/vnd.wfa.wsc"
              onChange={(e) => onChange({ mimeType: e.target.value })}
            />
          </label>
        ) : null}
      </div>

      {kind.raw ? (
        <div className="row" style={{ marginTop: 4 }}>
          <label className="row tight">
            <span className="dim">TNF</span>
            <input
              type="number"
              min={0}
              max={7}
              value={record.tnf ?? 0}
              size={2}
              onChange={(e) => onChange({ tnf: Number(e.target.value) })}
            />
          </label>
          <label className="row tight">
            <span className="dim">Type bytes (base64)</span>
            <input
              type="text"
              className="mono"
              value={record.typeBytes ?? ''}
              onChange={(e) => onChange({ typeBytes: e.target.value })}
            />
          </label>
          <label className="row tight">
            <span className="dim">ID (base64)</span>
            <input
              type="text"
              className="mono"
              value={record.id ?? ''}
              onChange={(e) => onChange({ id: e.target.value })}
            />
          </label>
        </div>
      ) : null}

      {kind.payload ? (
        <label className="stack" style={{ marginTop: 4 }}>
          <span className="dim">Payload (base64) — overrides the text above when set</span>
          <input
            type="text"
            className="mono"
            value={record.payload ?? ''}
            onChange={(e) => onChange({ payload: e.target.value })}
          />
        </label>
      ) : null}
    </fieldset>
  )
}

function num(v: unknown): number | undefined {
  return typeof v === 'number' && Number.isFinite(v) ? v : undefined
}

function truncate(s?: string): string {
  if (!s) return '—'
  return s.length > 48 ? `${s.slice(0, 48)}…` : s
}
