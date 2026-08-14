import { useEffect, useState } from 'react'
import { signOut } from './api'
import { Dashboard } from './panels/Dashboard'
import { Devices } from './panels/Devices'
import { Live } from './panels/Live'
import { Logs } from './panels/Logs'
import { Origins } from './panels/Origins'
import { Security } from './panels/Security'
import { Settings } from './panels/Settings'
import { Tag } from './panels/Tag'
import { useAction, useControlState, useControlStream } from './useControl'
import { useTags } from './useTags'
import { fmtDuration, modeLabel } from './format'
import { Dot, Notice, Panel } from './ui'

type TabId = 'dashboard' | 'live' | 'tag' | 'devices' | 'origins' | 'security' | 'logs' | 'settings'

const TABS: { id: TabId; label: string }[] = [
  { id: 'dashboard', label: 'Dashboard' },
  { id: 'live', label: 'Live' },
  { id: 'tag', label: 'Tag' },
  { id: 'devices', label: 'Devices' },
  { id: 'origins', label: 'Origins' },
  { id: 'security', label: 'Security' },
  { id: 'logs', label: 'Log' },
  { id: 'settings', label: 'Settings' },
]

/** Tab in the hash, so a reload lands back where it was. Not worth a router. */
function useHashTab(): [TabId, (t: TabId) => void] {
  const read = (): TabId => {
    const raw = location.hash.replace(/^#\/?/, '')
    return TABS.some((t) => t.id === raw) ? (raw as TabId) : 'dashboard'
  }

  const [tab, setTab] = useState<TabId>(read)

  useEffect(() => {
    const onHash = () => setTab(read())
    window.addEventListener('hashchange', onHash)
    return () => window.removeEventListener('hashchange', onHash)
  }, [])

  return [
    tab,
    (next: TabId) => {
      location.hash = `#/${next}`
      setTab(next)
    },
  ]
}

function useTheme(): [string, () => void] {
  const [theme, setTheme] = useState(() => localStorage.getItem('theme') ?? 'auto')

  useEffect(() => {
    const root = document.documentElement
    if (theme === 'auto') root.removeAttribute('data-theme')
    else root.setAttribute('data-theme', theme)
    localStorage.setItem('theme', theme)
  }, [theme])

  return [theme, () => setTheme((t) => (t === 'auto' ? 'light' : t === 'light' ? 'dark' : 'auto'))]
}

export default function App() {
  const [tab, setTab] = useHashTab()
  const [theme, cycleTheme] = useTheme()

  const stream = useControlStream()
  const { data: state, isLoading, error } = useControlState()
  const act = useAction()

  const tags = useTags(state?.security.apiSecret)

  if (stream.link === 'expired') {
    return (
      <div style={{ padding: 16 }}>
        <Panel title="Session expired">
          <p>{stream.error}</p>
          <p className="dim">
            Open the agent's tray menu and choose <b>Open Control Center</b> to start a new session.
          </p>
        </Panel>
      </div>
    )
  }

  if (isLoading && !state) {
    return <div style={{ padding: 16 }}>Loading agent state…</div>
  }

  if (error && !state) {
    return (
      <div style={{ padding: 16 }}>
        <Notice kind="err">
          Could not reach the agent: {error instanceof Error ? error.message : String(error)}
        </Notice>
      </div>
    )
  }

  if (!state) return null

  const writable = state.settings.mode !== 'read'

  return (
    <div className="app">
      <div className="topbar">
        <span className="brand">
          {state.agent.name} <span className="ver">{state.agent.version}</span>
        </span>

        <Dot state={state.agent.running ? 'ok' : 'err'}>
          {state.agent.running ? 'running' : 'stopped'}
        </Dot>

        <span className="dim">{modeLabel(state.reader.mode)}</span>

        <span className="dim">
          {state.reader.cardPresent ? (
            <span className="mono">{state.reader.cardUID}</span>
          ) : (
            'no card'
          )}
        </span>

        <span className="spacer" />

        {state.agent.running ? (
          <button
            type="button"
            className="link"
            onClick={() => act.mutate({ name: 'agent.stop' })}
            disabled={act.isPending}
          >
            stop
          </button>
        ) : (
          <button
            type="button"
            className="link"
            onClick={() => act.mutate({ name: 'agent.start' })}
            disabled={act.isPending}
          >
            start
          </button>
        )}

        <button type="button" className="link" onClick={cycleTheme} title="Light, dark or follow the system">
          theme: {theme}
        </button>

        <button
          type="button"
          className="link"
          onClick={() => {
            void signOut().then(() => location.reload())
          }}
        >
          sign out
        </button>
      </div>

      <nav className="tabs" role="tablist">
        {TABS.map((t) => (
          <button
            key={t.id}
            role="tab"
            aria-selected={tab === t.id}
            onClick={() => setTab(t.id)}
          >
            {t.label}
            {t.id === 'devices' && state.devices.length > 0 ? (
              <span className="count"> ({state.devices.length})</span>
            ) : null}
            {t.id === 'origins' && state.origins.blocked.length > 0 ? (
              <span className="warn"> ({state.origins.blocked.length})</span>
            ) : null}
            {t.id === 'live' && tags.events.length > 0 ? (
              <span className="count"> ({tags.events.length})</span>
            ) : null}
          </button>
        ))}
      </nav>

      <main>
        {state.origins.allowAny && tab !== 'origins' ? (
          <Notice kind="err">
            <b>Origin checking is off for this session.</b> Any page the operator opens can drive
            this reader.{' '}
            <button type="button" className="link" onClick={() => setTab('origins')}>
              review
            </button>
          </Notice>
        ) : null}

        {tab === 'dashboard' ? <Dashboard state={state} tagLink={tags.link} /> : null}
        {tab === 'live' ? (
          <Live events={tags.events} link={tags.link} onClear={tags.clearEvents} />
        ) : null}
        {tab === 'tag' ? <Tag tags={tags} writable={writable} /> : null}
        {tab === 'devices' ? <Devices state={state} /> : null}
        {tab === 'origins' ? <Origins state={state} /> : null}
        {tab === 'security' ? <Security state={state} /> : null}
        {tab === 'logs' ? <Logs logs={stream.logs} onClear={stream.clearLogs} /> : null}
        {tab === 'settings' ? <Settings state={state} /> : null}
      </main>

      <div className="statusbar">
        <span>
          control <Dot state={stream.link === 'open' ? 'ok' : 'warn'}>{stream.link}</Dot>
        </span>
        <span>
          client <Dot state={tags.link === 'open' ? 'ok' : 'warn'}>{tags.link}</Dot>
        </span>
        <span>port {state.server.port}</span>
        <span>{state.server.tls ? 'TLS' : 'no TLS'}</span>
        <span>{state.server.clients} clients</span>
        <span>up {fmtDuration(state.agent.uptimeSec)}</span>
        <span className="spacer" style={{ flex: '1 1 auto' }} />
        {state.security.cert?.expired ? <span className="err">certificate expired</span> : null}
        <span>{state.capture.logEntries} log lines</span>
      </div>
    </div>
  )
}
