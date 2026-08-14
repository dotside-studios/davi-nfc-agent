import { useAction } from '../useControl'
import type { ControlState } from '../types'
import type { TagLink } from '../useTags'
import { fmtDuration, fmtRelative, modeLabel } from '../format'
import { ActionLink, Copyable, Dot, KV, Panel, Row } from '../ui'

/** Everything the tray shows, at once and selectable, plus what it has no room
 *  for: certificate expiry, the names it covers, and where the config lives. */
export function Dashboard({ state, tagLink }: { state: ControlState; tagLink: TagLink }) {
  const { agent, reader, server, security, origins, devices } = state
  const act = useAction()

  const cert = security.cert
  const certState = !cert ? 'off' : cert.expired ? 'err' : cert.expiresInHr < 24 * 14 ? 'warn' : 'ok'

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
          <Row label="Version">
            {agent.version} {agent.dev ? <span className="warn">(dev build)</span> : null}
          </Row>
          <Row label="Platform">{agent.platform}</Row>
          <Row label="Uptime">{fmtDuration(agent.uptimeSec)}</Row>
          <Row label="Config">
            <span className="mono">{agent.configDir || '—'}</span>
          </Row>
        </KV>
      </Panel>

      <Panel title="Reader">
        <KV>
          <Row label="Device">{reader.devicePath || <span className="dim">auto-detect</span>}</Row>
          <Row label="Mode">{modeLabel(reader.mode)}</Row>
          <Row label="Card">
            {reader.cardPresent ? (
              <Dot state="ok">
                <span className="mono">{reader.cardUID}</span> · {reader.cardType}
              </Dot>
            ) : (
              <Dot state="off">none present</Dot>
            )}
          </Row>
          <Row label="Filter">
            {state.settings.cardTypes && state.settings.cardTypes.length > 0 ? (
              <span className="warn">{state.settings.cardTypes.join(', ')}</span>
            ) : (
              <span className="dim">all types</span>
            )}
          </Row>
          <Row label="Remote devices">
            {reader.remoteActive} active <span className="dim">of {reader.remoteDevices} connected</span>
          </Row>
        </KV>
      </Panel>

      <Panel
        title="Server"
        tools={
          <ActionLink
            run={() => act.mutateAsync({ name: 'agent.restartServers' })}
            confirm={{ prompt: 'Restart the listeners? Connected clients and devices will be dropped and must reconnect.' }}
          >
            restart
          </ActionLink>
        }
      >
        <KV>
          <Row label="Port">
            {server.port} {server.tls ? <span className="ok">TLS</span> : <span className="warn">no TLS</span>}
          </Row>
          <Row label="Clients">
            <Dot state={tagLink === 'open' ? 'ok' : 'warn'}>
              {server.clients} connected
              {tagLink === 'open' ? <span className="dim"> (including this console)</span> : null}
            </Dot>
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
      </Panel>

      <Panel title="Certificate">
        {cert ? (
          <KV>
            <Row label="Status">
              <Dot state={certState}>
                {cert.expired
                  ? 'Expired'
                  : cert.expiresInHr < 48
                    ? `Expires in ${cert.expiresInHr}h`
                    : `Valid for ${Math.floor(cert.expiresInHr / 24)} days`}
              </Dot>
            </Row>
            <Row label="Subject">{cert.subject || <span className="dim">—</span>}</Row>
            <Row label="Issuer">
              {cert.selfSigned ? <span className="warn">self-signed</span> : cert.issuer}
            </Row>
            <Row label="Covers">
              {cert.hosts.length > 0 ? (
                <span className="mono">{cert.hosts.join(', ')}</span>
              ) : (
                <span className="err">no names — clients cannot verify this certificate</span>
              )}
            </Row>
            <Row label="Key pin">
              <Copyable value={security.publicKeyPin ?? ''} />
            </Row>
          </KV>
        ) : (
          <span className="dim">Not serving TLS.</span>
        )}
      </Panel>

      <Panel title="Access">
        <KV>
          <Row label="Allowed origins">
            {origins.allowAny ? (
              <span className="err">origin checking is OFF for this session</span>
            ) : (
              `${origins.allowed.length} allowed`
            )}
            {origins.blocked.length > 0 ? (
              <span className="warn"> · {origins.blocked.length} recently blocked</span>
            ) : null}
          </Row>
          <Row label="Paired devices">
            {devices.length} paired
            {devices.filter((d) => d.online).length > 0 ? (
              <span className="ok"> · {devices.filter((d) => d.online).length} online</span>
            ) : null}
          </Row>
          <Row label="Pairing policy">
            {security.requirePairedDevice ? (
              <span className="ok">paired devices only</span>
            ) : (
              <span className="dim">shared secret accepted</span>
            )}
          </Row>
          <Row label="Pairing PIN">
            <Copyable value={security.pairingPIN ?? ''} />
          </Row>
          <Row label="Console sessions">{security.controlSessions}</Row>
        </KV>
      </Panel>

      <Panel title="Recent devices">
        {devices.length === 0 ? (
          <span className="dim">No devices have paired with this agent.</span>
        ) : (
          <KV>
            {devices.slice(0, 6).map((d) => (
              <Row
                key={d.id}
                label={
                  <Dot state={d.online ? 'ok' : 'off'}>
                    <span className="dim">{d.platform || 'device'}</span>
                  </Dot>
                }
              >
                {d.name} <span className="dim">· seen {fmtRelative(d.lastSeen)}</span>
              </Row>
            ))}
          </KV>
        )}
      </Panel>
    </div>
  )
}
