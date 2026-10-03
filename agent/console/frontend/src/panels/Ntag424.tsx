import type { NTAG424Response, NTAG424SDMPlan } from '@davi/nfc-agent-client'
import { useEffect, useMemo, useState } from 'react'
import {
  ACCESS_FREE,
  DEFAULT_CHANGE_KEY_FORM,
  DEFAULT_SDM_FORM,
  RIGHT_OPTIONS,
  type CardReading,
  type ChangeKeyForm,
  type Part,
  type RegionKind,
  type SdmForm,
  changeKeyPhrase,
  checkChangeKey,
  confirmedChangeKey,
  confirmedLock,
  describeFileSettings,
  layoutRows,
  LOCK_PHRASE,
  looksLikeNtag424,
  placeholdersIn,
  previewPlan,
  readCard,
  sdmRequest,
  signatureHex,
} from '../ntag424'
import type { Tags } from '../useTags'
import { ActionLink, Copyable, Dot, InlineConfirm, KV, Notice, Panel, Row } from '../ui'

/**
 * NTAG 424 DNA tools for the tag on the reader, or in a phone's field. The
 * agent holds the keys and runs the authenticated session; this page asks for
 * operations and shows the answers, and no key the agent holds is ever sent here.
 */
export function Ntag424({ tags, writable }: { tags: Tags; writable: boolean }) {
  const { tag } = tags
  const [forced, setForced] = useState(false)

  if (!tag) return null

  if (!looksLikeNtag424(tag.type) && !forced) {
    return (
      <Panel title="NTAG 424 DNA">
        <div className="dim">
          This tag is not reported as an NTAG 424 DNA ({tag.type || 'type unknown'}).{' '}
          <button type="button" className="link" onClick={() => setForced(true)}>
            use these tools anyway
          </button>
        </div>
      </Panel>
    )
  }

  return (
    <>
      <CardInfo tags={tags} />
      <Sdm tags={tags} writable={writable} />
      <Dangerous tags={tags} writable={writable} />
    </>
  )
}

/* ---- card info ---- */

function CardInfo({ tags }: { tags: Tags }) {
  const [reading, setReading] = useState<CardReading | null>(null)
  const uid = tags.tag?.uid

  // A reading belongs to the card it was taken from.
  useEffect(() => setReading(null), [uid])

  return (
    <Panel
      title="NTAG 424 DNA"
      tools={
        <ActionLink run={async () => setReading(await readCard(tags.ntag424))}>
          {reading ? 're-read card' : 'read card'}
        </ActionLink>
      }
    >
      {!reading ? (
        <div className="dim">
          Reads the NDEF file settings, the real UID, key versions and the originality signature
          under the keys the agent holds.
        </div>
      ) : (
        <>
          <KV>
            <Row label="Card UID">
              <PartView part={reading.uid} render={(v) => <Copyable value={v} />} />
            </Row>
            <Row label="Key versions">
              <span className="row">
                {reading.keyVersions.map((p, n) => (
                  <span key={n} className="nowrap">
                    <span className="dim">key {n}</span>{' '}
                    <PartView part={p} render={(v) => <span className="mono">{v}</span>} compact />
                  </span>
                ))}
              </span>
            </Row>
            <Row label="Signature">
              <PartView
                part={reading.signature}
                render={(v) => <Copyable value={signatureHex(v)} display={shorten(signatureHex(v))} />}
              />
            </Row>
          </KV>
          <FileSettings part={reading.fileSettings} />
        </>
      )}
    </Panel>
  )
}

function FileSettings({ part }: { part: CardReading['fileSettings'] }) {
  if (!part.ok) return <Notice kind="err">File settings: {part.error}</Notice>
  return (
    <>
      <div className="dim" style={{ margin: '6px 0 2px' }}>
        NDEF file settings
      </div>
      <KV>
        {describeFileSettings(part.value).map((r) => (
          <Row key={r.label} label={r.label}>
            {r.value}
          </Row>
        ))}
      </KV>
    </>
  )
}

function PartView<T>({
  part,
  render,
  compact,
}: {
  part: Part<T>
  render: (value: T) => React.ReactNode
  compact?: boolean
}) {
  if (part.ok) return <>{render(part.value)}</>
  return (
    <span className="err" title={part.error}>
      {compact ? '?' : part.error}
    </span>
  )
}

/* ---- SDM ---- */

const KIND_LABEL: Record<RegionKind, string> = {
  picc: 'PICC data',
  uid: 'UID',
  ctr: 'counter',
  enc: 'encrypted data',
  mac: 'MAC',
}

function Sdm({ tags, writable }: { tags: Tags; writable: boolean }) {
  const [form, setForm] = useState<SdmForm>(DEFAULT_SDM_FORM)
  const [plan, setPlan] = useState<NTAG424SDMPlan | null>(null)
  const [planError, setPlanError] = useState<string | null>(null)
  const [result, setResult] = useState<NTAG424Response['sdm'] | null>(null)

  const request = useMemo(() => sdmRequest(form, 'planSDM'), [form])
  const found = placeholdersIn(form.template)
  const set = <K extends keyof SdmForm>(key: K, value: SdmForm[K]) => setForm((f) => ({ ...f, [key]: value }))

  const { ntag424 } = tags

  // The agent plans the layout, so the preview is what it would write. Held
  // back a moment while the template is being typed.
  useEffect(() => {
    setResult(null)
    if (!request.ok) {
      setPlan(null)
      setPlanError(request.error)
      return
    }
    let stale = false
    const timer = window.setTimeout(() => {
      ntag424(request.request).then(
        (res) => {
          if (stale) return
          setPlan(res.plan ?? null)
          setPlanError(res.plan ? null : 'The agent returned no plan.')
        },
        (err: unknown) => {
          if (stale) return
          setPlan(null)
          setPlanError(err instanceof Error ? err.message : String(err))
        },
      )
    }, 250)
    return () => {
      stale = true
      window.clearTimeout(timer)
    }
  }, [request, ntag424])

  const preview = useMemo(() => (plan ? previewPlan(plan) : null), [plan])
  const configure = sdmRequest(form, 'configureSDM')

  const rightSelect = (key: 'metaRead' | 'fileRead' | 'counterRet' | 'change' | 'read' | 'write' | 'readWrite', keysOnly = false) => (
    <select value={form[key]} onChange={(e) => set(key, Number(e.target.value))}>
      {RIGHT_OPTIONS.filter((o) => !keysOnly || o.value <= 4).map((o) => (
        <option key={o.value} value={o.value}>
          {o.label}
        </option>
      ))}
    </select>
  )

  return (
    <Panel title="Secure dynamic messaging (SDM)">
      <label className="stack">
        <span className="dim">URL template ({'{picc}'} or {'{uid}'} and {'{ctr}'}, optional {'{enc}'}, and {'{mac}'})</span>
        <input
          type="text"
          className="mono"
          value={form.template}
          spellCheck={false}
          onChange={(e) => set('template', e.target.value)}
        />
      </label>

      <div className="row" style={{ marginTop: 4 }}>
        {found.includes('{picc}') ? (
          <label className="row tight">
            <span className="dim">PICC data key</span>
            {rightSelect('metaRead', true)}
          </label>
        ) : null}
        <label className="row tight">
          <span className="dim">MAC key</span>
          {rightSelect('fileRead', true)}
        </label>
        <label className="row tight">
          <span className="dim">Counter read</span>
          {rightSelect('counterRet')}
        </label>
        {found.includes('{enc}') ? (
          <label className="row tight">
            <span className="dim">{'{enc}'} width</span>
            <input
              type="number"
              min={32}
              step={32}
              value={form.encLength}
              onChange={(e) => set('encLength', e.target.value)}
              style={{ width: '6em' }}
            />
          </label>
        ) : null}
      </div>

      <div className="row" style={{ marginTop: 4 }}>
        <span className="dim">File access</span>
        <label className="row tight">
          <span className="dim">read</span>
          {rightSelect('read')}
        </label>
        <label className="row tight">
          <span className="dim">write</span>
          {rightSelect('write')}
        </label>
        <label className="row tight">
          <span className="dim">read and write</span>
          {rightSelect('readWrite')}
        </label>
        <label className="row tight">
          <span className="dim">change settings</span>
          {rightSelect('change')}
        </label>
      </div>

      {form.read !== ACCESS_FREE ? (
        <Notice kind="warn">
          Reading the file needs a key, so a phone tapping the tag will not see the URL unless it
          authenticates. SDM URLs are normally free to read.
        </Notice>
      ) : null}

      <div style={{ marginTop: 6 }}>
        <div className="dim">Layout the agent would write</div>
        {planError ? <div className="err">{planError}</div> : null}
        {preview && plan ? <PlanView plan={plan} preview={preview} /> : null}
      </div>

      <div className="row" style={{ marginTop: 6 }}>
        <ActionLink
          run={async () => {
            if (!configure.ok) throw new Error(configure.error)
            setResult((await ntag424(configure.request)).sdm ?? null)
          }}
          disabled={!configure.ok || !writable || (preview !== null && !preview.fits)}
        >
          Configure SDM
        </ActionLink>
        {!writable ? <span className="dim">the reader is in read-only mode</span> : null}
      </div>

      {result ? (
        <Notice kind={result.verified ? undefined : 'warn'}>
          <div>
            Settings applied. The read-back URL is <span className="mono">{result.url}</span>
          </div>
          {result.verified ? (
            <div>
              <Dot state="ok">MAC verified</Dot> for UID <span className="mono">{result.uid}</span>, counter{' '}
              {result.counter}. The read-back counted as a tap.
            </div>
          ) : (
            <div>
              <Dot state="warn">not verified</Dot> {result.verifyError}
            </div>
          )}
        </Notice>
      ) : null}
    </Panel>
  )
}

function PlanView({
  plan,
  preview,
}: {
  plan: NTAG424SDMPlan
  preview: NonNullable<ReturnType<typeof previewPlan>>
}) {
  return (
    <div className="stack">
      <div className="secret" style={{ display: 'block' }}>
        <span className="dim">{preview.prefix}</span>
        {preview.segments.map((s, i) => (
          <span
            key={i}
            className={s.kind ? `sdm-mirror ${s.kind}` : undefined}
            style={s.macInput ? { textDecoration: 'underline' } : undefined}
            title={s.kind ? KIND_LABEL[s.kind] : undefined}
          >
            {s.text}
          </span>
        ))}
      </div>

      <div className="row">
        <span className={preview.fits ? 'dim' : 'err'}>
          {preview.length} B of 256 B
        </span>
        {preview.segments.some((s) => s.macInput) ? (
          <span className="dim">underlined: the span the MAC covers</span>
        ) : null}
      </div>

      <table className="grid">
        <thead>
          <tr>
            <th>Mirror</th>
            <th>Offset</th>
            <th>Characters</th>
          </tr>
        </thead>
        <tbody>
          {layoutRows(plan).map((r) => (
            <tr key={r.label}>
              <td>{r.label}</td>
              <td className="num">{r.offset}</td>
              <td className="num">{r.width}</td>
            </tr>
          ))}
        </tbody>
      </table>

      <details>
        <summary className="dim">NDEF file content (hex)</summary>
        <div className="mono" style={{ overflowWrap: 'anywhere' }}>
          {preview.hex}
        </div>
      </details>
    </div>
  )
}

/* ---- irreversible ---- */

function Dangerous({ tags, writable }: { tags: Tags; writable: boolean }) {
  const [form, setForm] = useState<ChangeKeyForm>(DEFAULT_CHANGE_KEY_FORM)
  const [changed, setChanged] = useState<string | null>(null)
  const [locked, setLocked] = useState(false)

  const checked = checkChangeKey(form)
  const set = <K extends keyof ChangeKeyForm>(key: K, value: ChangeKeyForm[K]) => {
    setChanged(null)
    setForm((f) => ({ ...f, [key]: value }))
  }

  return (
    <Panel title="Keys and lock (irreversible)">
      {!writable ? (
        <Notice kind="warn">The reader is in read-only mode, so these are refused.</Notice>
      ) : null}

      <div className="dim">Replace a key</div>
      <div className="row" style={{ marginTop: 2 }}>
        <label className="row tight">
          <span className="dim">key</span>
          <input type="number" min={0} max={4} value={form.keyNo} onChange={(e) => set('keyNo', e.target.value)} style={{ width: '4em' }} />
        </label>
        <label className="row tight">
          <span className="dim">authenticate with key</span>
          <input type="number" min={0} max={4} value={form.authKeyNo} onChange={(e) => set('authKeyNo', e.target.value)} style={{ width: '4em' }} />
        </label>
        <label className="row tight">
          <span className="dim">version</span>
          <input type="number" min={0} max={255} value={form.version} onChange={(e) => set('version', e.target.value)} style={{ width: '5em' }} />
        </label>
      </div>
      <div className="row" style={{ marginTop: 4 }}>
        <label className="row tight">
          <input type="radio" checked={form.source === 'configured'} onChange={() => set('source', 'configured')} />
          <span>the key the agent holds for this slot</span>
        </label>
        <label className="row tight">
          <input type="radio" checked={form.source === 'explicit'} onChange={() => set('source', 'explicit')} />
          <span>a key I enter</span>
        </label>
      </div>
      {form.source === 'explicit' ? (
        <label className="stack" style={{ marginTop: 4 }}>
          <span className="dim">New key (32 hex characters). Sent to the agent once and never shown again.</span>
          <input
            type="password"
            className="mono"
            autoComplete="off"
            spellCheck={false}
            value={form.newKey}
            onChange={(e) => set('newKey', e.target.value)}
          />
        </label>
      ) : null}
      {!checked.ok ? <div className="dim">{checked.error}</div> : null}

      <div style={{ marginTop: 4 }}>
        <InlineConfirm
          label="change key"
          phrase={changeKeyPhrase(checked.ok ? checked.keyNo : Number(form.keyNo))}
          disabled={!checked.ok || !writable}
          prompt={
            <>
              Replace key {form.keyNo} on this tag, authenticating with key {form.authKeyNo}? The old
              key cannot be recovered afterwards, and the agent's own key set is not updated: a tag
              whose new key the agent does not hold cannot be operated on again.
            </>
          }
          run={async (typed) => {
            await tags.ntag424(confirmedChangeKey(form, typed))
            setChanged(`Key ${form.keyNo} replaced.`)
            setForm((f) => ({ ...f, newKey: '' }))
          }}
        />
      </div>
      {changed ? <Notice>{changed}</Notice> : null}

      <div className="dim" style={{ marginTop: 10 }}>
        Make the NDEF file read-only
      </div>
      <div style={{ marginTop: 2 }}>
        <InlineConfirm
          label="lock NDEF file"
          phrase={LOCK_PHRASE}
          disabled={!writable || locked}
          prompt="Set the NDEF file's write access to never? This cannot be undone: the file keeps its contents and can never be written again."
          run={async (typed) => {
            await tags.ntag424(confirmedLock(typed))
            setLocked(true)
          }}
        />
      </div>
      {locked ? <Notice>The NDEF file is now read-only.</Notice> : null}
    </Panel>
  )
}

function shorten(hex: string): string {
  return hex.length > 32 ? `${hex.slice(0, 32)}…` : hex
}
