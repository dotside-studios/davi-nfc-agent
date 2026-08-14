import { useMutation, useQuery, useQueryClient, type UseQueryResult } from '@tanstack/react-query'
import { useCallback, useEffect, useRef, useState } from 'react'
import { action, fetchState, wsURL } from './api'
import type { ControlState, LogEntry } from './types'

/**
 * Agent state is a Query cache entry kept fresh by a push rather than polling.
 * Query owns the cache and error states; the socket only writes newer snapshots
 * into it, so a failed socket still leaves the query able to populate the page.
 */

export const controlKeys = {
  state: ['control', 'state'] as const,
}

/** How many log entries the page keeps rendered. The agent's ring holds more. */
const LOG_LIMIT = 5000

export type Link = 'connecting' | 'open' | 'closed' | 'expired'

interface Envelope {
  type: 'state' | 'logs'
  state?: ControlState
  logs?: LogEntry[]
}

/** Reads agent state. Kept fresh by the stream, so it never polls on its own. */
export function useControlState(): UseQueryResult<ControlState> {
  return useQuery({
    queryKey: controlKeys.state,
    queryFn: fetchState,
    // The socket is the update mechanism; polling would only add a second way
    // for the two to disagree.
    staleTime: Infinity,
    refetchOnWindowFocus: false,
    refetchOnReconnect: false,
    retry: (count, err) => (err instanceof Error && err.name === 'SessionExpired' ? false : count < 3),
  })
}

/**
 * Runs a control action, then refreshes agent state. The agent also pushes after
 * every action; the invalidation is what keeps this correct when the socket is
 * down.
 */
export function useAction() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ name, params }: { name: string; params?: Record<string, unknown> }) =>
      action(name, params),
    onSuccess: () => qc.invalidateQueries({ queryKey: controlKeys.state }),
  })
}

export interface Stream {
  link: Link
  logs: LogEntry[]
  error: string | null
  clearLogs: () => void
}

/**
 * Opens the control socket for the lifetime of the app. State snapshots go into
 * the query cache; log lines are append-only and stay here.
 */
export function useControlStream(): Stream {
  const qc = useQueryClient()
  const [link, setLink] = useState<Link>('connecting')
  const [logs, setLogs] = useState<LogEntry[]>([])
  const [error, setError] = useState<string | null>(null)

  const socket = useRef<WebSocket | null>(null)
  const retry = useRef(0)
  const timer = useRef<number | undefined>(undefined)
  const stopped = useRef(false)

  const mergeLogs = useCallback((incoming: LogEntry[]) => {
    if (incoming.length === 0) return
    setLogs((prev) => {
      // By sequence, so a reconnect replaying the ring cannot duplicate.
      const highest = prev.length > 0 ? prev[prev.length - 1].seq : 0
      const fresh = incoming.filter((e) => e.seq > highest)
      if (fresh.length === 0) return prev
      const next = prev.concat(fresh)
      return next.length > LOG_LIMIT ? next.slice(next.length - LOG_LIMIT) : next
    })
  }, [])

  const connect = useCallback(() => {
    if (stopped.current) return

    let ws: WebSocket
    try {
      ws = new WebSocket(wsURL('/control/ws'))
    } catch {
      schedule()
      return
    }
    socket.current = ws

    ws.onopen = () => {
      retry.current = 0
      setLink('open')
      setError(null)
    }

    ws.onmessage = (ev) => {
      let env: Envelope
      try {
        env = JSON.parse(ev.data as string) as Envelope
      } catch {
        return
      }
      if (env.type === 'state' && env.state) {
        qc.setQueryData(controlKeys.state, env.state)
      } else if (env.type === 'logs' && env.logs) {
        mergeLogs(env.logs)
      }
    }

    ws.onclose = () => {
      if (stopped.current) return
      socket.current = null
      setLink('closed')

      // A refused upgrade and a dropped connection look alike, so ask the API
      // which it was: a 403 will never succeed on retry.
      void fetchState()
        .then(() => schedule())
        .catch((err: unknown) => {
          if (err instanceof Error && err.name === 'SessionExpired') {
            setLink('expired')
            setError('Control session expired. Reopen the Control Center from the tray menu.')
            return
          }
          schedule()
        })
    }

    function schedule() {
      if (stopped.current) return
      // Backs off to 5s. On loopback a drop usually means the agent is
      // rebinding its listeners, which takes about a second.
      const delay = Math.min(5000, 250 * 2 ** retry.current)
      retry.current += 1
      window.clearTimeout(timer.current)
      timer.current = window.setTimeout(connect, delay)
    }
  }, [qc, mergeLogs])

  useEffect(() => {
    stopped.current = false
    connect()
    return () => {
      stopped.current = true
      window.clearTimeout(timer.current)
      socket.current?.close()
      socket.current = null
    }
  }, [connect])

  const clearLogs = useCallback(() => setLogs([]), [])

  return { link, logs, error, clearLogs }
}
