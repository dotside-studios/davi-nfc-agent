import { fmtDuration, fmtRelative } from '../format'
import type { ControlState, LiveEvent, LogEntry } from '../types'
import { useAction } from '../useControl'
import type { TagLink } from '../useTags'
import { ActionLink, Copyable, Dot, Empty, KV, Notice, Panel, Row } from '../ui'
import { CardFilterControl, DevicePicker, ModeControl, PortEditor } from './controls'

/**
 * The landing page, and the page an operator works from: the controls that get
 * changed — mode, reader, filter, start/stop — sit here rather than behind a
 * Settings tab, alongside the state needed to decide whether to change them.
 */
export function Overview({
  state,
  tagLink,
  events,
  logs,
  onOpenTag,
}: {
  state: ControlState
  tagLink: TagLink
  events: LiveEvent[]
  logs: LogEntry[]
  onOpenTag: () => void
}) {
  const act = useAction()
  const { agent, reader, server, security, origins, devices } = state
  const cert = security.cert

  return (
    <div className="cols">
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

      <Panel title="Reader">
        <DevicePicker state={state} />
        <div style={{ marginTop: 6 }}>
          <ModeControl state={state} />
        </div>
      </Panel>

      <Panel
        title="Current tag"
        tools={
          reader.cardPresent ? (
            <button type="button" className="link" onClick={onOpenTag}>
              inspect / write
            </button>
          ) : null
        }
      >
        {reader.cardPresent ? (
          <KV>
            <Row label="UID">
              <Copyable value={reader.cardUID ?? ''} />
            </Row>
            <Row label="Type">{reader.cardType}</Row>
          </KV>
        ) : (
          <div className="stack">
            <Dot state="off">No tag on the reader.</Dot>
            {tagLink !== 'open' ? (
              <span className="warn">Not connected to the client endpoint, so scans will not appear.</span>
            ) : null}
          </div>
        )}
      </Panel>

      <Panel title="Card type filter">
        <CardFilterControl state={state} />
      </Panel>

      <Panel title="Server">
        <KV>
          <Row label="Port">
            {server.port} {server.tls ? <span className="ok">TLS</span> : <span className="warn">no TLS</span>}
          </Row>
          <Row label="Clients">
            <Dot state={tagLink === 'open' ? 'ok' : 'warn'}>{server.clients} connected</Dot>
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
          <Row label="Addresses">
            {server.localIPs.length > 0 ? (
              <span className="mono">{server.localIPs.join(', ')}</span>
            ) : (
              <span className="dim">none detected</span>
            )}
          </Row>
        </KV>
        <div style={{ marginTop: 6 }}>
          <PortEditor state={state} />
        </div>
      </Panel>

      <Panel title="Access">
        <KV>
          <Row label="Origins">
            {origins.allowAny ? (
              <span className="err">checking is OFF this session</span>
            ) : (
              `${origins.allowed.length} allowed`
            )}
            {origins.blocked.length > 0 ? (
              <span className="warn"> · {origins.blocked.length} blocked</span>
            ) : null}
          </Row>
          <Row label="Devices">
            {devices.length} paired
            {devices.filter((d) => d.online).length > 0 ? (
              <span className="ok"> · {devices.filter((d) => d.online).length} online</span>
            ) : null}
          </Row>
          <Row label="Policy">
            {security.requirePairedDevice ? (
              <span className="ok">paired devices only</span>
            ) : (
              <span className="dim">shared secret accepted</span>
            )}
          </Row>
          {security.pairingPIN ? (
            <Row label="Pairing PIN">
              <Copyable value={security.pairingPIN} />
            </Row>
          ) : null}
        </KV>
      </Panel>

      <Panel title="Certificate">
        {cert ? (
          <KV>
            <Row label="Status">
              <Dot state={cert.expired ? 'err' : cert.expiresInHr < 24 * 14 ? 'warn' : 'ok'}>
                {cert.expired
                  ? 'Expired'
                  : cert.expiresInHr < 48
                    ? `Expires in ${cert.expiresInHr}h`
                    : `Valid ${Math.floor(cert.expiresInHr / 24)} more days`}
              </Dot>
            </Row>
            <Row label="Issuer">{cert.selfSigned ? <span className="warn">self-signed</span> : cert.issuer}</Row>
            <Row label="Covers">
              {cert.hosts.length > 0 ? (
                <span className="mono">{cert.hosts.join(', ')}</span>
              ) : (
                <span className="err">no names</span>
              )}
            </Row>
            <Row label="Key pin">
              <Copyable value={security.publicKeyPin ?? ''} />
            </Row>
          </KV>
        ) : (
          <div className="stack">
            <Dot state="warn">Not serving TLS.</Dot>
            <span className="dim">
              Browsers refuse a <span className="mono">wss://</span> connection outright, with no warning to
              click through.
            </span>
          </div>
        )}
      </Panel>

      <Panel title="Recent activity">
        {events.length === 0 && logs.length === 0 ? (
          <Empty>Nothing yet.</Empty>
        ) : (
          <div className="stack">
            {events
              .slice(-6)
              .reverse()
              .map((e) => (
                <div key={e.id} className="nowrap">
                  <span className={e.ok === false ? 'err' : 'dim'}>{e.kind}</span>{' '}
                  <span>{e.summary}</span>
                </div>
              ))}
            {events.length === 0
              ? logs
                  .slice(-6)
                  .reverse()
                  .map((l) => (
                    <div key={l.seq}>
                      <span className={l.level === 'info' ? 'dim' : l.level}>{l.level}</span> {l.message}
                    </div>
                  ))
              : null}
          </div>
        )}
      </Panel>

      <Panel title="Configuration">
        <KV>
          <Row label="Config directory">
            <Copyable value={agent.configDir} />
          </Row>
          <Row label="Settings">
            <span className="mono">{agent.configDir ? `${agent.configDir}/settings.json` : '—'}</span>
          </Row>
          <Row label="Console sessions">{security.controlSessions}</Row>
        </KV>
        <div className="dim" style={{ marginTop: 4 }}>
          Reader mode, filter, device and port are stored there. Credentials are kept out of it — the API
          secret, paired devices and origin allowlist each have their own file.
        </div>
      </Panel>

      {devices.length > 0 ? (
        <Panel title="Paired devices">
          <KV>
            {devices.slice(0, 8).map((d) => (
              <Row
                key={d.id}
                label={
                  <Dot state={d.online ? 'ok' : 'off'}>
                    <span className="dim">{d.platform || 'device'}</span>
                  </Dot>
                }
              >
                {d.name} <span className="dim">· {fmtRelative(d.lastSeen)}</span>
              </Row>
            ))}
          </KV>
        </Panel>
      ) : null}

      {origins.allowAny ? (
        <Notice kind="err">
          <b>Origin checking is off for this session.</b> Any page the operator opens can drive this reader.
        </Notice>
      ) : null}
    </div>
  )
}
