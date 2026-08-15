import { useState } from 'react'
import type { ControlState, Mode } from '../types'
import { useAction } from '../useControl'
import { ActionLink, Notice } from '../ui'

export function ModeControl({ state }: { state: ControlState }) {
  const act = useAction()
  const { settings } = state

  const MODES: [Mode, string, string][] = [
    ['readwrite', 'Read / write', 'Both permitted'],
    ['read', 'Read only', 'Writes and locks refused'],
    ['write', 'Write only', 'Tag data not broadcast'],
  ]

  return (
    <div className="stack">
      {MODES.map(([value, label, hint]) => (
        <label key={value} className="row">
          <input
            type="radio"
            name="mode"
            checked={settings.mode === value}
            onChange={() => act.mutate({ name: 'reader.setMode', params: { mode: value } })}
          />
          <span>
            {label} <span className="dim">— {hint}</span>
          </span>
        </label>
      ))}
      {act.error ? <div className="err">{(act.error as Error).message}</div> : null}
    </div>
  )
}

export function CardFilterControl({ state }: { state: ControlState }) {
  const act = useAction()
  const filters = state.settings.cardTypes ?? []

  const toggle = (type: string, on: boolean) => {
    const next = on ? [...filters, type] : filters.filter((t) => t !== type)
    act.mutate({ name: 'reader.setCardTypes', params: { cardTypes: next } })
  }

  return (
    <>
      <label className="row">
        <input
          type="checkbox"
          checked={filters.length === 0}
          onChange={(e) => {
            if (e.target.checked) act.mutate({ name: 'reader.setCardTypes', params: { cardTypes: [] } })
          }}
        />
        <span>
          All types <span className="dim">— no filtering</span>
        </span>
      </label>

      <div className="stack" style={{ marginTop: 2, paddingLeft: 16 }}>
        {state.reader.allCardTypes.map((type) => (
          <label key={type} className="row">
            <input type="checkbox" checked={filters.includes(type)} onChange={(e) => toggle(type, e.target.checked)} />
            <span className="mono">{type}</span>
          </label>
        ))}
      </div>
    </>
  )
}

export function DevicePicker({ state }: { state: ControlState }) {
  const act = useAction()
  const { reader, settings } = state
  const missing = settings.devicePath && !reader.available.includes(settings.devicePath)

  return (
    <>
      <div className="row">
        <select
          value={settings.devicePath}
          onChange={(e) => act.mutate({ name: 'reader.selectDevice', params: { devicePath: e.target.value } })}
          style={{ flex: '1 1 200px' }}
        >
          <option value="">Auto-detect</option>
          {reader.available.map((d) => (
            <option key={d} value={d}>
              {d}
            </option>
          ))}
          {/* A pinned but absent device must stay selectable, or editing
              anything else here would silently un-pin it. */}
          {missing ? <option value={settings.devicePath}>{settings.devicePath} (not connected)</option> : null}
        </select>
      </div>

      <div className="dim" style={{ marginTop: 2 }}>
        {reader.available.length > 0 ? (
          <>
            Detected: <span className="mono">{reader.available.join(', ')}</span>
          </>
        ) : (
          <span className="warn">No readers detected.</span>
        )}
      </div>

      {missing ? (
        <Notice kind="warn">
          The pinned reader <span className="mono">{settings.devicePath}</span> is not connected. It will be
          used again when it reappears.
        </Notice>
      ) : null}

      {act.error ? <div className="err">{(act.error as Error).message}</div> : null}
    </>
  )
}

export function PortEditor({ state }: { state: ControlState }) {
  const act = useAction()
  const { settings, server } = state
  const [port, setPort] = useState(String(settings.port || server.port))

  const parsed = Number(port)
  const changed = Number.isInteger(parsed) && parsed > 0 && parsed <= 65535 && parsed !== server.port

  return (
    <>
      <div className="row">
        <span className="dim">Agent port</span>
        <input type="number" value={port} min={1} max={65535} style={{ width: 80 }} onChange={(e) => setPort(e.target.value)} />
        <ActionLink
          disabled={!changed}
          run={() => act.mutateAsync({ name: 'settings.save', params: { ...settings, port: parsed } })}
        >
          save
        </ActionLink>
        <span className="dim">
          listening on {server.port}
          {server.bootstrapPort > 0 ? `, pairing on ${server.bootstrapPort}` : ''}
        </span>
      </div>

      {changed ? (
        <Notice kind="warn">
          Stored, but applied at startup. Restart the agent for it to take effect — the console is served
          on this port, so it moves with it.
        </Notice>
      ) : null}
    </>
  )
}
