import { fmtDateTime, fmtRelative } from '../format'
import type { ControlState } from '../types'
import { useAction } from '../useControl'
import { ActionLink, Dot, Empty, Panel } from '../ui'

/** The applications currently driving the reader, and where each connected from. */
export function Clients({ state }: { state: ControlState }) {
  const act = useAction()
  const { clients } = state

  return (
    <Panel
      title={
        <>
          Connected clients <span className="dim">({clients.length})</span>
        </>
      }
      flush
    >
      {clients.length === 0 ? (
        <Empty>
          Nothing is connected to the client endpoint. This console connects as a client itself, so
          it normally appears here.
        </Empty>
      ) : (
        <div className="tw">
          <table className="grid">
            <thead>
              <tr>
                <th>Origin</th>
                <th>Address</th>
                <th>Connected</th>
                <th>Writes</th>
                <th>Locks</th>
                <th>Client</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {clients.map((c) => (
                <tr key={c.id}>
                  <td className="mono">
                    {c.origin ? (
                      c.origin
                    ) : (
                      <span className="dim" title="No Origin header, not a browser">
                        non-browser
                      </span>
                    )}
                  </td>
                  <td className="mono">{c.remoteAddr}</td>
                  <td className="nowrap" title={fmtDateTime(c.connectedAt)}>
                    {fmtRelative(c.connectedAt)}
                  </td>
                  <td className="num">
                    {c.writes > 0 ? <span className="warn">{c.writes}</span> : c.writes}
                  </td>
                  <td className="num">
                    {c.locks > 0 ? <span className="err">{c.locks}</span> : c.locks}
                  </td>
                  <td className="dim">{shortAgent(c.userAgent)}</td>
                  <td>
                    <ActionLink
                      danger
                      run={() => act.mutateAsync({ name: 'clients.disconnect', params: { id: c.id } })}
                      confirm={{
                        prompt: `Disconnect ${c.origin || c.remoteAddr}?\n\nIt is free to reconnect immediately: this ends the session, it does not bar it. Revoke its origin to bar it.`,
                      }}
                    >
                      disconnect
                    </ActionLink>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      <div className="body">
        <Dot state="off">
          <span className="dim">
            Writes and locks are counted per connection, so a client that is only listening is
            distinguishable from one changing tags.
          </span>
        </Dot>
      </div>
    </Panel>
  )
}

/** A user agent is far too long for a dense row; keep the part that identifies it. */
function shortAgent(ua?: string): string {
  if (!ua) return '—'
  const known = ['Firefox', 'Edg', 'Chrome', 'Safari', 'Go-http-client', 'okhttp', 'curl', 'python']
  for (const name of known) {
    const i = ua.indexOf(name)
    if (i >= 0) return ua.slice(i).split(' ')[0]
  }
  return ua.length > 24 ? `${ua.slice(0, 24)}…` : ua
}
