import { useCallback, useEffect, useRef, useState } from 'react'
import { wsURL } from './api'
import type { LiveEvent, ScanRecord, TagCapabilities, TagData, WriteRecord } from './types'

/**
 * The console's connection to the ordinary client endpoint. It speaks the same
 * protocol as any other client rather than taking a private path through the
 * control API, so there is one implementation of write, lock and capabilities.
 */

const EVENT_LIMIT = 2000

/** Distinct tags remembered. A provisioning run works through many. */
const HISTORY_LIMIT = 100

/** Bounds a tag operation, so a lost response cannot leave the composer
 *  disabled with no indication of why. */
const REQUEST_TIMEOUT_MS = 30_000

export type TagLink = 'connecting' | 'open' | 'closed'

interface Pending {
  resolve: (payload: Record<string, unknown>) => void
  reject: (err: Error) => void
  timer: number
}

export interface Tags {
  link: TagLink
  tag: TagData | null
  capabilities: TagCapabilities | null
  events: LiveEvent[]
  history: ScanRecord[]
  clearEvents: () => void
  write: (records: WriteRecord[], lock?: boolean) => Promise<Record<string, unknown>>
  lock: () => Promise<Record<string, unknown>>
  refreshCapabilities: () => Promise<Record<string, unknown>>
  transceive: (data: string, raw: boolean) => Promise<Record<string, unknown>>
}

export function useTags(secret?: string): Tags {
  const [link, setLink] = useState<TagLink>('connecting')
  const [tag, setTag] = useState<TagData | null>(null)
  const [capabilities, setCapabilities] = useState<TagCapabilities | null>(null)
  const [events, setEvents] = useState<LiveEvent[]>([])
  const [history, setHistory] = useState<ScanRecord[]>([])

  const socket = useRef<WebSocket | null>(null)
  const pending = useRef(new Map<string, Pending>())
  const retry = useRef(0)
  const timer = useRef<number | undefined>(undefined)
  const stopped = useRef(false)
  const eventSeq = useRef(0)

  const push = useCallback((e: Omit<LiveEvent, 'id' | 'at'>) => {
    eventSeq.current += 1
    const event: LiveEvent = { id: eventSeq.current, at: new Date().toISOString(), ...e }
    setEvents((prev) => {
      const next = prev.concat(event)
      return next.length > EVENT_LIMIT ? next.slice(next.length - EVENT_LIMIT) : next
    })
  }, [])

  // Keyed by UID, so re-presenting the same tag bumps a counter rather than
  // adding a row — which is what makes the list readable during a run where
  // one tag is tapped repeatedly.
  const remember = useCallback((data: TagData) => {
    if (!data.uid) return
    setHistory((prev) => {
      const rest = prev.filter((r) => r.uid !== data.uid)
      const existing = prev.find((r) => r.uid === data.uid)
      const record: ScanRecord = {
        uid: data.uid,
        type: data.type || existing?.type || '',
        text: data.text || existing?.text || '',
        count: (existing?.count ?? 0) + 1,
        firstAt: existing?.firstAt ?? data.scannedAt,
        lastAt: data.scannedAt,
      }
      return [record, ...rest].slice(0, HISTORY_LIMIT)
    })
  }, [])

  const connect = useCallback(() => {
    if (stopped.current) return

    // Loopback is exempt from the secret; sent anyway in case that narrows.
    const path = secret ? `/ws?secret=${encodeURIComponent(secret)}` : '/ws'

    let ws: WebSocket
    try {
      ws = new WebSocket(wsURL(path))
    } catch {
      schedule()
      return
    }
    socket.current = ws

    ws.onopen = () => {
      retry.current = 0
      setLink('open')
    }

    ws.onmessage = (ev) => {
      let msg: {
        id?: string
        type?: string
        success?: boolean
        error?: string
        payload?: Record<string, unknown>
      }
      try {
        msg = JSON.parse(ev.data as string)
      } catch {
        return
      }

      // Settled first: a response carries an id, and treating it as a
      // broadcast would leave the caller hanging.
      if (msg.id && pending.current.has(msg.id)) {
        const p = pending.current.get(msg.id)!
        pending.current.delete(msg.id)
        window.clearTimeout(p.timer)
        if (msg.success) p.resolve(msg.payload ?? {})
        else p.reject(new Error(msg.error ?? 'operation failed'))
      }

      switch (msg.type) {
        case 'tagData': {
          const data = msg.payload as unknown as TagData
          if (!data) break
          if (data.err) {
            setTag(data)
            push({ kind: 'error', summary: `Read failed: ${data.err}`, detail: data.uid, ok: false })
            break
          }
          setTag(data)
          if (data.capabilities) setCapabilities(data.capabilities)
          remember(data)
          push({
            kind: 'scan',
            summary: `${data.type || 'tag'} ${data.uid}`,
            detail: data.text || undefined,
            ok: true,
          })
          break
        }

        case 'deviceStatus': {
          const p = (msg.payload ?? {}) as { connected?: boolean; message?: string; cardPresent?: boolean }
          if (p.cardPresent === false) {
            setTag(null)
            setCapabilities(null)
          }
          push({ kind: 'status', summary: p.message ?? 'device status', ok: p.connected })
          break
        }

        case 'writeResponse': {
          const p = (msg.payload ?? {}) as { message?: string }
          push({
            kind: 'write',
            summary: msg.success ? (p.message ?? 'Write succeeded') : `Write failed: ${msg.error ?? 'unknown'}`,
            ok: msg.success,
          })
          break
        }

        case 'lockResponse': {
          push({
            kind: 'lock',
            summary: msg.success ? 'Tag locked (permanent)' : `Lock failed: ${msg.error ?? 'unknown'}`,
            ok: msg.success,
          })
          break
        }

        case 'transceiveResponse': {
          push({
            kind: 'apdu',
            summary: msg.success ? 'Raw exchange completed' : `Raw exchange failed: ${msg.error ?? 'unknown'}`,
            ok: msg.success,
          })
          break
        }

        case 'capabilitiesResponse': {
          if (msg.success && msg.payload) {
            setCapabilities(msg.payload as TagCapabilities)
          }
          break
        }

        case 'error': {
          const p = (msg.payload ?? {}) as { message?: string; code?: string }
          push({ kind: 'error', summary: p.message ?? msg.error ?? 'agent error', detail: p.code, ok: false })
          break
        }
      }
    }

    ws.onclose = () => {
      if (stopped.current) return
      socket.current = null
      setLink('closed')

      for (const [, p] of pending.current) {
        window.clearTimeout(p.timer)
        p.reject(new Error('connection closed'))
      }
      pending.current.clear()

      schedule()
    }

    function schedule() {
      if (stopped.current) return
      const delay = Math.min(5000, 250 * 2 ** retry.current)
      retry.current += 1
      window.clearTimeout(timer.current)
      timer.current = window.setTimeout(connect, delay)
    }
  }, [secret, push, remember])

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

  /** Sends a correlated request and resolves with the matching response. */
  const send = useCallback(
    (type: string, payload: Record<string, unknown>): Promise<Record<string, unknown>> => {
      const ws = socket.current
      if (!ws || ws.readyState !== WebSocket.OPEN) {
        return Promise.reject(new Error('not connected to the agent'))
      }

      const id = `console_${Date.now()}_${Math.random().toString(36).slice(2, 8)}`

      return new Promise((resolve, reject) => {
        const t = window.setTimeout(() => {
          pending.current.delete(id)
          reject(new Error('the agent did not respond in time'))
        }, REQUEST_TIMEOUT_MS)

        pending.current.set(id, { resolve, reject, timer: t })
        ws.send(JSON.stringify({ id, type, payload }))
      })
    },
    [],
  )

  const write = useCallback(
    (records: WriteRecord[], lock = false) => send('writeRequest', lock ? { records, lock } : { records }),
    [send],
  )
  const lock = useCallback(() => send('lockRequest', {}), [send])
  const transceive = useCallback(
    (data: string, raw: boolean) => send('transceiveRequest', { data, raw }),
    [send],
  )
  const refreshCapabilities = useCallback(() => send('capabilitiesRequest', {}), [send])
  const clearEvents = useCallback(() => {
    setEvents([])
    setHistory([])
  }, [])

  return {
    link,
    tag,
    capabilities,
    events,
    history,
    clearEvents,
    write,
    lock,
    refreshCapabilities,
    transceive,
  }
}
