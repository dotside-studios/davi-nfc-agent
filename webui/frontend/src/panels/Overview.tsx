import { fmtDateTime, fmtDuration, fmtRelative, fmtTime } from '../format'
import type { ControlState, LogEntry, ScanRecord, TagData } from '../types'
import { useAction } from '../useControl'
import type { TagLink } from '../useTags'
import { ActionLink, Copyable, Dot, Empty, KV, Notice, Panel, Row } from '../ui'
import { CardFilterControl, DevicePicker, ModeControl, PortEditor } from './controls'

/**
 * The landing page. The left third holds state and the knobs that get turned;
 * the right two thirds hold what the reader is actually doing — the tag on it
 * now, the log tail, and the tags it has seen.
 */
export function Overview({
  state,
  tag,
  tagLink,
  history,
  logs,
  onOpenTag,
}: {
  state: ControlState
  tag: TagData | null
  tagLink: TagLink
  history: ScanRecord[]
  logs: LogEntry[]
  onOpenTag: () => void
}) {
  return (
    <div className="aside-main">
      <div>
        <AgentPanel state={state} />
        <Panel title="Reader">
          <DevicePicker state={state} />
          <div style={{ marginTop: 6 }}>
            <ModeControl state={state} />
          </div>
        </Panel>
        <Panel title="Card type filter">
          <CardFilterControl state={state} />
        </Panel>
        <ServerPanel state={state} tagLink={tagLink} />
        <ConfigPanel state={state} />
      </div>

      <div>
        {state.origins.allowAny ? (
          <Notice kind="err">
            <b>Origin checking is off for this session.</b> Any page the operator opens can drive this
            reader.
          </Notice>
        ) : null}

        <CurrentTagPanel tag={tag} tagLink={tagLink} onOpenTag={onOpenTag} />
        <LogTailPanel logs={logs} />
        <HistoryPanel history={history} />
      </div>
    </div>
  )
}

function AgentPanel({ state }: { state: ControlState }) {
  const act = useAction()
  const { agent } = state

  return (
    <Panel
      title="Agent"
      tools={
        agent.running ? (
          <ActionLink run={() => act.mutateAsync({ name: 'agent.stop' })}>stop</ActionLink>
        ) : (
          <ActionLink run={() => act.mutateAsync({ name: 'agent.start' })}>start</ActionLink>
        )
      }
    >
      <KV>
        <Row label="Status">
          <Dot state={agent.running ? 'ok' : 'err'}>{agent.running ? 'Running' : 'Stopped'}</Dot>
        </Row>
        <Row label="Uptime">{fmtDuration(agent.uptimeSec)}</Row>
        <Row label="Version">
          {agent.version} {agent.dev ? <span className="warn">(dev)</span> : null}{' '}
          <span className="dim">· {agent.platform}</span>
        </Row>
      </KV>

      <div className="row" style={{ marginTop: 6 }}>
        <ActionLink
          run={() => act.mutateAsync({ name: 'agent.restartServers' })}
          confirm={{ prompt: 'Restart the listeners? Connected clients and devices are dropped and must reconnect.' }}
        >
          restart listeners
        </ActionLink>
        <span className="sep">|</span>
        <ActionLink
          danger
          run={() => act.mutateAsync({ name: 'agent.quit' })}
          confirm={{
            prompt: 'Quit the agent entirely?\n\nNothing will be reading tags until it is started again from the desktop.',
            phrase: 'quit',
          }}
        >
          quit
        </ActionLink>
      </div>
    </Panel>
  )
}

function ServerPanel({ state, tagLink }: { state: ControlState; tagLink: TagLink }) {
  const { server, reader, security, origins, devices } = state
  const cert = security.cert

  return (
    <Panel title="Server">
      <KV>
        <Row label="Port">
          {server.port} {server.tls ? <span className="ok">TLS</span> : <span className="warn">no TLS</span>}
        </Row>
        <Row label="Clients">
          <Dot state={tagLink === 'open' ? 'ok' : 'warn'}>{server.clients} connected</Dot>
          {state.clients.some((c) => c.writes > 0 || c.locks > 0) ? (
            <span className="warn"> · some are writing</span>
          ) : null}{' '}
          <a href="#/security/clients">list</a>
        </Row>
        <Row label="Remote devices">
          {reader.remoteActive} active <span className="dim">of {reader.remoteDevices}</span>
        </Row>
        <Row label="Client URL">
          <Copyable value={server.clientURL} />
        </Row>
        <Row label="Device URL">
          <Copyable value={server.deviceURL} />
        </Row>
        {server.pairingURL ? (
          <Row label="Pair a phone">
            <Copyable value={server.pairingURL} />
          </Row>
        ) : null}
        <Row label="Certificate">
          {cert ? (
            <Dot state={cert.expired ? 'err' : cert.expiresInHr < 24 * 14 ? 'warn' : 'ok'}>
              {cert.expired ? 'expired' : `${Math.floor(cert.expiresInHr / 24)} days left`}
              {cert.selfSigned ? <span className="dim"> · self-signed</span> : null}
            </Dot>
          ) : (
            <Dot state="warn">not serving TLS</Dot>
          )}
        </Row>
        <Row label="Access">
          {origins.allowAny ? (
            <span className="err">origin checking OFF</span>
          ) : (
            `${origins.allowed.length} origins`
          )}
          <span className="dim"> · {devices.length} devices</span>
          {origins.blocked.length > 0 ? (
            <span className="warn"> · {origins.blocked.length} blocked</span>
          ) : null}
        </Row>
      </KV>

      <div style={{ marginTop: 6 }}>
        <PortEditor state={state} />
      </div>
    </Panel>
  )
}

function ConfigPanel({ state }: { state: ControlState }) {
  const { agent, security } = state
  return (
    <Panel title="Configuration">
      <KV>
        <Row label="Config">
          <Copyable value={agent.configDir} />
        </Row>
        <Row label="Console sessions">{security.controlSessions}</Row>
      </KV>
      <div className="dim" style={{ marginTop: 4 }}>
        Mode, filter, device and port persist to <span className="mono">settings.json</span> there.
        Credentials keep their own files.
      </div>
    </Panel>
  )
}

/** What is on the reader right now, in enough detail to act on without leaving. */
function CurrentTagPanel({
  tag,
  tagLink,
  onOpenTag,
}: {
  tag: TagData | null
  tagLink: TagLink
  onOpenTag: () => void
}) {
  const records = tag?.message?.records ?? []
  const caps = tag?.capabilities

  return (
    <Panel
      title="Current tag"
      tools={
        <button type="button" className="link" onClick={onOpenTag}>
          {tag ? 'inspect / write' : 'open composer'}
        </button>
      }
    >
      {!tag ? (
        <div className="stack">
          <Dot state="off">No tag on the reader.</Dot>
          {tagLink !== 'open' ? (
            <span className="warn">
              Not connected to the client endpoint, so scans will not appear here.
            </span>
          ) : (
            <span className="dim">Present one to read it.</span>
          )}
        </div>
      ) : (
        <>
          {tag.err ? <Notice kind="err">Read failed: {tag.err}</Notice> : null}

          <div className="pair">
            <KV>
              <Row label="UID">
                <Copyable value={tag.uid} />
              </Row>
              <Row label="Type">
                {tag.type} <span className="dim">· {tag.technology}</span>
              </Row>
              <Row label="Scanned">{fmtDateTime(tag.scannedAt)}</Row>
              <Row label="Text">
                {tag.text ? <span className="mono">{tag.text}</span> : <span className="dim">none</span>}
              </Row>
            </KV>

            {caps ? (
              <KV>
                <Row label="Memory">
                  {typeof caps.usableCapacity === 'number' ? `${caps.usableCapacity} B usable` : '—'}
                  {typeof caps.memorySize === 'number' ? (
                    <span className="dim"> of {caps.memorySize} B</span>
                  ) : null}
                </Row>
                <Row label="Writable">
                  {caps.readOnly ? (
                    <Dot state="err">locked read-only</Dot>
                  ) : caps.writable === false ? (
                    <Dot state="err">no</Dot>
                  ) : (
                    <Dot state="ok">yes</Dot>
                  )}
                </Row>
                <Row label="Lockable">{caps.lockable ? <Dot state="ok">yes</Dot> : <Dot state="off">no</Dot>}</Row>
                <Row label="Password">
                  {caps.passwordProtectable ? <Dot state="ok">supported</Dot> : <Dot state="off">no</Dot>}
                </Row>
              </KV>
            ) : null}
          </div>

          {records.length > 0 ? (
            <div className="tw" style={{ marginTop: 6 }}>
              <table className="grid">
                <thead>
                  <tr>
                    <th>#</th>
                    <th>TNF</th>
                    <th>Type</th>
                    <th>Value</th>
                  </tr>
                </thead>
                <tbody>
                  {records.map((r, i) => (
                    <tr key={i}>
                      <td className="num">{i + 1}</td>
                      <td className="mono">{r.tnf ?? '—'}</td>
                      <td className="mono">{r.type ?? '—'}</td>
                      <td className="mono">{r.content || <span className="dim">—</span>}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : null}
        </>
      )}
    </Panel>
  )
}

/** The last stretch of log, so a failure is visible without changing tab. */
function LogTailPanel({ logs }: { logs: LogEntry[] }) {
  const tail = logs.slice(-12)

  return (
    <Panel title="Recent log" flush>
      {tail.length === 0 ? (
        <Empty>Nothing captured yet.</Empty>
      ) : (
        <div className="logview short">
          {tail.map((e) => (
            <div className="logrow" key={e.seq}>
              <span className="t">{fmtTime(e.time)}</span>
              <span className={`lvl ${e.level === 'info' ? 'dim' : e.level}`}>{e.level}</span>
              {e.source ? <span className="src">{e.source}</span> : null}
              <span className="msg">{e.message}</span>
            </div>
          ))}
        </div>
      )}
    </Panel>
  )
}

function HistoryPanel({ history }: { history: ScanRecord[] }) {
  return (
    <Panel
      title={
        <>
          Previously scanned <span className="dim">({history.length})</span>
        </>
      }
      flush
    >
      {history.length === 0 ? (
        <Empty>No tags scanned yet this session.</Empty>
      ) : (
        <div className="tw">
          <table className="grid">
            <thead>
              <tr>
                <th className="mono">UID</th>
                <th>Type</th>
                <th>Content</th>
                <th>Scans</th>
                <th>Last seen</th>
              </tr>
            </thead>
            <tbody>
              {history.map((r) => (
                <tr key={r.uid}>
                  <td className="mono">{r.uid}</td>
                  <td>{r.type || <span className="dim">—</span>}</td>
                  <td className="mono">{r.text || <span className="dim">empty</span>}</td>
                  <td className="num">{r.count}</td>
                  <td className="nowrap">{fmtRelative(r.lastAt)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Panel>
  )
}
