import type { ControlState, LogEntry } from './types'

/**
 * Client for the agent's control API. Same-origin, authenticated by the session
 * cookie the agent set during the tray handoff. A 403 means the session is gone,
 * which the app reports differently from a network failure.
 */

export class SessionExpired extends Error {
  constructor() {
    super('control session expired')
    this.name = 'SessionExpired'
  }
}

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    credentials: 'same-origin',
    ...init,
  })

  if (res.status === 403) throw new SessionExpired()

  if (!res.ok) {
    // The action endpoint reports its reason as JSON; other routes send text.
    const body = await res.text()
    try {
      const parsed = JSON.parse(body) as { error?: string }
      if (parsed.error) throw new Error(parsed.error)
    } catch (err) {
      if (err instanceof Error && err.message && !(err instanceof SyntaxError)) throw err
    }
    throw new Error(body.trim() || `request failed (${res.status})`)
  }

  return (await res.json()) as T
}

export function fetchState(): Promise<ControlState> {
  return request<ControlState>('/control/state')
}

export function fetchLogs(since = 0): Promise<LogEntry[]> {
  return request<LogEntry[]>(`/control/logs?since=${since}`)
}

interface ActionResponse<R> {
  ok: boolean
  error?: string
  result?: R
}

/** Invokes a control action, rejecting with the agent's message on failure. */
export async function action<R = unknown>(
  name: string,
  params?: Record<string, unknown>,
): Promise<R | undefined> {
  const res = await request<ActionResponse<R>>('/control/action', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ action: name, params }),
  })
  if (!res.ok) throw new Error(res.error ?? 'action failed')
  return res.result
}

export async function signOut(): Promise<void> {
  await request('/control/signout', { method: 'POST' })
}

/** Absolute ws:// or wss:// URL for a path on this agent. */
export function wsURL(path: string): string {
  const proto = location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${proto}//${location.host}${path}`
}

/** Best-effort clipboard copy. Falls back for pages the API refuses. */
export async function copy(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text)
    return true
  } catch {
    // navigator.clipboard is unavailable on insecure origins.
    try {
      const ta = document.createElement('textarea')
      ta.value = text
      ta.style.position = 'fixed'
      ta.style.opacity = '0'
      document.body.appendChild(ta)
      ta.select()
      const ok = document.execCommand('copy')
      document.body.removeChild(ta)
      return ok
    } catch {
      return false
    }
  }
}
