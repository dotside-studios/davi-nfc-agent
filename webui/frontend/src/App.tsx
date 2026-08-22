import { useEffect, useState } from 'react'
import { signOut } from './api'
import { Activity } from './panels/Activity'
import { Overview } from './panels/Overview'
import { Security } from './panels/Security'
import { Tag } from './panels/Tag'
import { useAction, useControlState, useControlStream } from './useControl'
import { useTags } from './useTags'
import { fmtDuration, modeLabel } from './format'
import { Dot, Notice, Panel } from './ui'

type TabId = 'overview' | 'tag' | 'activity' | 'security'

const TABS: { id: TabId; label: string }[] = [
  { id: 'overview', label: 'Overview' },
  { id: 'tag', label: 'Tag' },
  { id: 'activity', label: 'Activity' },
  { id: 'security', label: 'Security' },
]

/** Tab in the hash, so a reload lands back where it was. Not worth a router. */
function useHashTab(): [TabId, (t: TabId) => void] {
  const read = (): TabId => {
    const raw = location.hash.replace(/^#\/?/, '').split('/')[0]
    return TABS.some((t) => t.id === raw) ? (raw as TabId) : 'overview'
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

        <span className="dim">{modeLabel(state.settings.mode)}</span>

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
            {t.id === 'security' && state.origins.blocked.length > 0 ? (
              <span className="warn"> ({state.origins.blocked.length} blocked)</span>
            ) : null}
            {t.id === 'activity' && tags.events.length > 0 ? (
              <span className="count"> ({tags.events.length})</span>
            ) : null}
          </button>
        ))}
      </nav>

      <main>
        {state.origins.allowAny && tab !== 'security' ? (
          <Notice kind="err">
            <b>Origin checking is off for this session.</b> Any page the operator opens can drive
            this reader.{' '}
            <button type="button" className="link" onClick={() => setTab('security')}>
              review
            </button>
          </Notice>
        ) : null}

        {tab === 'overview' ? (
          <Overview
            state={state}
            tag={tags.tag}
            tagLink={tags.link}
            history={tags.history}
            logs={stream.logs}
            onOpenTag={() => setTab('tag')}
          />
        ) : null}
        {tab === 'tag' ? <Tag tags={tags} writable={writable} /> : null}
        {tab === 'activity' ? (
          <Activity
            events={tags.events}
            logs={stream.logs}
            tagLink={tags.link}
            onClearEvents={tags.clearEvents}
            onClearLogs={stream.clearLogs}
          />
        ) : null}
        {tab === 'security' ? <Security state={state} /> : null}
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
