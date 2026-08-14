import { useState } from 'react'
import type { ControlState, Mode } from '../types'
import { useAction } from '../useControl'
import { ActionLink, Copyable, KV, Notice, Panel, Row } from '../ui'

/**
 * The preferences that now survive a restart. Mode and card filter previously
 * lived only in tray menu state; device and port could only be set with a flag.
 */
export function Settings({ state }: { state: ControlState }) {
  const act = useAction()
  const { settings, reader, agent } = state

  const filters = settings.cardTypes ?? []
  const allTypes = reader.allCardTypes

  const setMode = (mode: Mode) => act.mutate({ name: 'reader.setMode', params: { mode } })

  const toggleType = (type: string, on: boolean) => {
    const next = on ? [...filters, type] : filters.filter((t) => t !== type)
    act.mutate({ name: 'reader.setCardTypes', params: { cardTypes: next } })
  }

  return (
    <div className="cols wide">
      <Panel title="Reader mode">
        <div className="stack">
          {(
            [
              ['readwrite', 'Read / write', 'Both reading and writing are permitted.'],
              ['read', 'Read only', 'Write and lock requests are refused.'],
              ['write', 'Write only', 'Tag data is not broadcast to clients.'],
            ] as [Mode, string, string][]
          ).map(([value, label, hint]) => (
            <label key={value} className="row">
              <input
                type="radio"
                name="mode"
                checked={settings.mode === value}
                onChange={() => setMode(value)}
              />
              <span>
                <b>{label}</b> <span className="dim">— {hint}</span>
              </span>
            </label>
          ))}
        </div>
        {act.error ? <div className="err">{(act.error as Error).message}</div> : null}
      </Panel>

      <Panel title="Card type filter">
        <label className="row">
          <input
            type="checkbox"
            checked={filters.length === 0}
            onChange={(e) => {
              if (e.target.checked) act.mutate({ name: 'reader.setCardTypes', params: { cardTypes: [] } })
            }}
          />
          <span>
            <b>All types</b> <span className="dim">— no filtering</span>
          </span>
        </label>

        <div className="stack" style={{ marginTop: 4, paddingLeft: 16 }}>
          {allTypes.map((type) => (
            <label key={type} className="row">
              <input
                type="checkbox"
                checked={filters.includes(type)}
                onChange={(e) => toggleType(type, e.target.checked)}
              />
              <span className="mono">{type}</span>
            </label>
          ))}
        </div>

        <div className="dim" style={{ marginTop: 4 }}>
          With nothing selected every type is accepted. Selecting every type is the same as selecting
          none, and is stored that way.
        </div>
      </Panel>

      <Panel title="Reader device">
        <DevicePicker state={state} />
      </Panel>

      <Panel title="Network">
        <PortEditor state={state} />
      </Panel>

      <Panel title="Configuration">
        <KV>
          <Row label="Config directory">
            <Copyable value={agent.configDir} />
          </Row>
          <Row label="Settings file">
            <span className="mono">{agent.configDir ? `${agent.configDir}/settings.json` : '—'}</span>
          </Row>
        </KV>
        <div className="dim" style={{ marginTop: 4 }}>
          Credentials are kept out of that file deliberately. The API secret, the paired devices and
          the origin allowlist each have their own file with their own handling.
        </div>
      </Panel>

      <Panel title="Agent">
        <div className="row">
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
            quit agent
          </ActionLink>
        </div>
      </Panel>
    </div>
  )
}

function DevicePicker({ state }: { state: ControlState }) {
  const act = useAction()
  const { reader, settings } = state

  return (
    <>
      <div className="row">
        <select
          value={settings.devicePath}
          onChange={(e) => act.mutate({ name: 'reader.selectDevice', params: { devicePath: e.target.value } })}
          style={{ flex: '1 1 220px' }}
        >
          <option value="">Auto-detect</option>
          {reader.available.map((d) => (
            <option key={d} value={d}>
              {d}
            </option>
          ))}
          {/* A pinned but absent device must stay selectable, or editing
              anything else here would silently un-pin it. */}
          {settings.devicePath && !reader.available.includes(settings.devicePath) ? (
            <option value={settings.devicePath}>{settings.devicePath} (not connected)</option>
          ) : null}
        </select>
      </div>

      <KV>
        <Row label="In use">{reader.devicePath || <span className="dim">none</span>}</Row>
        <Row label="Detected">
          {reader.available.length > 0 ? (
            <span className="mono">{reader.available.join(', ')}</span>
          ) : (
            <span className="warn">no readers detected</span>
          )}
        </Row>
      </KV>

      {settings.devicePath && !reader.available.includes(settings.devicePath) ? (
        <Notice kind="warn">
          The pinned reader <span className="mono">{settings.devicePath}</span> is not connected. The
          agent will use it again when it reappears.
        </Notice>
      ) : null}

      {act.error ? <div className="err">{(act.error as Error).message}</div> : null}
    </>
  )
}

function PortEditor({ state }: { state: ControlState }) {
  const act = useAction()
  const { settings, server } = state
  const [port, setPort] = useState(String(settings.port || server.port))

  const parsed = Number(port)
  const valid = Number.isInteger(parsed) && parsed > 0 && parsed <= 65535
  const changed = valid && parsed !== server.port

  return (
    <>
      <div className="row">
        <label className="row tight">
          <span className="dim">Agent port</span>
          <input type="number" value={port} min={1} max={65535} onChange={(e) => setPort(e.target.value)} />
        </label>
        <ActionLink
          disabled={!changed}
          run={() =>
            act.mutateAsync({
              name: 'settings.save',
              params: { ...settings, port: parsed },
            })
          }
        >
          save
        </ActionLink>
      </div>

      <KV>
        <Row label="Listening on">{server.port}</Row>
        <Row label="Pairing server">
          {server.bootstrapPort > 0 ? server.bootstrapPort : <span className="dim">disabled</span>}
        </Row>
      </KV>

      {changed ? (
        <Notice kind="warn">
          A port change is stored but applied at startup. Restart the agent for it to take effect —
          and note that the console itself is served on this port, so it will move with it.
        </Notice>
      ) : null}
    </>
  )
}
