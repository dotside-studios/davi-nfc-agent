import type { ControlState } from '../types'
import { useAction } from '../useControl'
import { fmtDateTime, fmtRelative } from '../format'
import { ActionLink, Dot, Empty, Notice, Panel } from '../ui'

/** Paired devices, one row each, revocable individually or all at once. */
export function Devices({ state }: { state: ControlState }) {
  const act = useAction()
  const { devices, security, settings } = state

  return (
    <>
      <Panel
        title={`Paired devices (${devices.length})`}
        tools={
          devices.length > 0 ? (
            <ActionLink
              danger
              run={() => act.mutateAsync({ name: 'devices.revokeAll' })}
              confirm={{
                prompt: `Revoke all ${devices.length} paired devices? Every phone and reader will have to pair again.`,
                phrase: 'revoke all',
              }}
            >
              revoke all
            </ActionLink>
          ) : null
        }
        flush
      >
        {devices.length === 0 ? (
          <Empty>
            No devices have paired. Use <b>Pair a phone</b> on the Dashboard to add one.
          </Empty>
        ) : (
          <div className="tw">
            <table className="grid">
              <thead>
                <tr>
                  <th>State</th>
                  <th>Name</th>
                  <th>Platform</th>
                  <th>Paired</th>
                  <th>Last seen</th>
                  <th className="mono">ID</th>
                  <th />
                </tr>
              </thead>
              <tbody>
                {devices.map((d) => (
                  <tr key={d.id}>
                    <td>
                      <Dot state={d.online ? 'ok' : 'off'}>{d.online ? 'online' : 'offline'}</Dot>
                    </td>
                    <td>{d.name}</td>
                    <td className="dim">{d.platform || '—'}</td>
                    <td className="nowrap">{fmtDateTime(d.pairedAt)}</td>
                    <td className="nowrap">{fmtRelative(d.lastSeen)}</td>
                    <td className="mono dim">{d.id.slice(0, 8)}</td>
                    <td>
                      <ActionLink
                        danger
                        run={() => act.mutateAsync({ name: 'devices.revoke', params: { id: d.id } })}
                        confirm={{ prompt: `Revoke "${d.name}"? It will have to pair again to reconnect.` }}
                      >
                        revoke
                      </ActionLink>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>

      <Panel title="Pairing policy">
        <div className="stack">
          <label className="row">
            <input
              type="checkbox"
              checked={settings.requirePairedDevice}
              disabled={security.requirePairedDeviceLocked}
              onChange={(e) =>
                act.mutate({
                  name: 'devices.setRequirePaired',
                  params: { enabled: e.target.checked },
                })
              }
            />
            <span>
              <b>Require paired devices</b> — admit only devices holding their own credential.
            </span>
          </label>

          <div className="dim">
            With this off, any device that knows the shared API secret can connect, and a device on
            this machine can connect without one. With it on, both of those stop working and only a
            device that has completed pairing is admitted. Browser clients are governed by the origin
            allowlist instead, and are unaffected either way.
          </div>

          {security.requirePairedDeviceLocked ? (
            <Notice kind="warn">
              This agent was started with <span className="mono">-require-paired-devices</span>, so
              the requirement stays on for this run and cannot be turned off from here.
            </Notice>
          ) : null}

          {settings.requirePairedDevice && devices.length === 0 ? (
            <Notice kind="err">
              No devices are paired, so every device connection will be refused until one pairs.
              Pair a device first, or turn this off.
            </Notice>
          ) : null}

          <div className="dim">
            Unlike the tray's version of this switch, the setting here is written to
            <span className="mono"> settings.json</span> and survives a restart.
          </div>
        </div>
      </Panel>
    </>
  )
}
