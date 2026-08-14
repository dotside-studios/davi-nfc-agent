import { useState } from 'react'
import type { ControlState } from '../types'
import { useAction } from '../useControl'
import { ActionLink, Empty, Notice, Panel } from '../ui'

/** The browser allowlist. The tray offers a blocked origin only while it is on
 *  screen; here the refusals accumulate and stay clickable. */
export function Origins({ state }: { state: ControlState }) {
  const act = useAction()
  const { origins } = state
  const [draft, setDraft] = useState('')

  const add = () => {
    const value = draft.trim()
    if (!value) return
    act.mutate({ name: 'origins.allow', params: { origin: value } }, { onSuccess: () => setDraft('') })
  }

  return (
    <>
      {origins.allowAny ? (
        <Notice kind="err">
          <b>Origin checking is off for this session.</b> Any page the operator opens can read, write
          and permanently lock cards through this agent. It reverts when the agent restarts, and is
          never written to disk.{' '}
          <ActionLink run={() => act.mutateAsync({ name: 'origins.setAllowAny', params: { enabled: false } })}>
            turn checking back on
          </ActionLink>
        </Notice>
      ) : null}

        <Panel title={`Allowed origins (${origins.allowed.length})`} flush>
          <div className="body">
            <div className="row">
              <input
                type="text"
                value={draft}
                placeholder="app.example.com or localhost:3002"
                onChange={(e) => setDraft(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === 'Enter') add()
                }}
                style={{ flex: '1 1 240px' }}
              />
              <button type="button" onClick={add} disabled={!draft.trim() || act.isPending}>
                Allow
              </button>
            </div>
            <div className="dim" style={{ marginTop: 4 }}>
              Matched on host:port. A full URL is accepted and reduced, so
              <span className="mono"> https://console.example.com</span> and
              <span className="mono"> console.example.com</span> are the same entry.
            </div>
            {act.error ? <div className="err">{(act.error as Error).message}</div> : null}
          </div>

          {origins.allowed.length === 0 ? (
            <Empty>Nothing is allowed. Only pages served by this agent can connect.</Empty>
          ) : (
            <div className="tw">
              <table className="grid">
                <thead>
                  <tr>
                    <th>Origin</th>
                    <th />
                  </tr>
                </thead>
                <tbody>
                  {origins.allowed.map((origin) => (
                    <tr key={origin}>
                      <td className="mono">{origin}</td>
                      <td>
                        <ActionLink
                          danger
                          run={() => act.mutateAsync({ name: 'origins.revoke', params: { origin } })}
                          confirm={{ prompt: `Revoke ${origin}? Pages served from it will stop connecting.` }}
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

        <Panel title={`Recently blocked (${origins.blocked.length})`} flush>
          {origins.blocked.length === 0 ? (
            <Empty>Nothing has been refused since the agent started.</Empty>
          ) : (
            <div className="tw">
              <table className="grid">
                <thead>
                  <tr>
                    <th>Origin</th>
                    <th />
                  </tr>
                </thead>
                <tbody>
                  {origins.blocked.map((origin) => (
                    <tr key={origin}>
                      <td className="mono">{origin}</td>
                      <td>
                        <ActionLink run={() => act.mutateAsync({ name: 'origins.allow', params: { origin } })}>
                          allow
                        </ActionLink>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </Panel>

      <Panel title="Session override">
        <label className="row">
          <input
            type="checkbox"
            checked={origins.allowAny}
            onChange={(e) => {
              if (
                e.target.checked &&
                !window.confirm(
                  'Turn off origin checking for this session?\n\nWhile it is off, any website the operator visits can read, write and permanently lock cards through this reader.',
                )
              ) {
                return
              }
              act.mutate({ name: 'origins.setAllowAny', params: { enabled: e.target.checked } })
            }}
          />
          <span>
            <b>Allow any origin (this session)</b>
          </span>
        </label>
        <div className="dim" style={{ marginTop: 4 }}>
          Intended for tracking down a connection problem, not for running with. It is deliberately
          never persisted. To let a console connect permanently, add its origin above instead.
        </div>
      </Panel>
    </>
  )
}
