import {
  NFCClient,
  type LockResponse,
  type NFCErrorEvent,
  type TagCapabilities,
  type TagData,
  type WriteRecord,
  type WriteResponse,
} from '@davi/nfc-agent-client'
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import type { LiveEvent, ScanRecord } from './types'

/**
 * The console's connection to the client endpoint, over the same library every
 * other consumer uses. The protocol is `NFCClient`'s; what is here is the
 * console's own — a feed of everything that happened, and the tags seen.
 */

const EVENT_LIMIT = 2000

/** Distinct tags remembered. A provisioning run works through many. */
const HISTORY_LIMIT = 100

export type TagLink = 'connecting' | 'open' | 'closed'

export interface Tags {
  link: TagLink
  tag: TagData | null
  capabilities: TagCapabilities | null
  events: LiveEvent[]
  history: ScanRecord[]
  clearEvents: () => void
  write: (records: WriteRecord[], lock?: boolean) => Promise<WriteResponse>
  lock: () => Promise<LockResponse>
  refreshCapabilities: () => Promise<TagCapabilities>
  transceive: (data: Uint8Array, raw: boolean) => Promise<Uint8Array>
}

export function useTags(secret?: string): Tags {
  const [link, setLink] = useState<TagLink>('connecting')
  const [tag, setTag] = useState<TagData | null>(null)
  const [capabilities, setCapabilities] = useState<TagCapabilities | null>(null)
  const [events, setEvents] = useState<LiveEvent[]>([])
  const [history, setHistory] = useState<ScanRecord[]>([])

  const client = useRef<NFCClient | null>(null)
  const eventSeq = useRef(0)

  const push = useCallback((e: Omit<LiveEvent, 'id' | 'at'>) => {
    eventSeq.current += 1
    const event: LiveEvent = { id: eventSeq.current, at: new Date().toISOString(), ...e }
    setEvents((prev) => {
      const next = prev.concat(event)
      return next.length > EVENT_LIMIT ? next.slice(next.length - EVENT_LIMIT) : next
    })
  }, [])

  // Keyed by UID, so a tag tapped repeatedly bumps a counter rather than
  // filling the list with its own rows.
  const remember = useCallback((data: TagData) => {
    if (!data.uid) return
    const at = (data.scannedAt ?? new Date()).toISOString()
    setHistory((prev) => {
      const rest = prev.filter((r) => r.uid !== data.uid)
      const existing = prev.find((r) => r.uid === data.uid)
      const record: ScanRecord = {
        uid: data.uid,
        type: data.type || existing?.type || '',
        text: data.text || existing?.text || '',
        count: (existing?.count ?? 0) + 1,
        firstAt: existing?.firstAt ?? at,
        lastAt: at,
      }
      return [record, ...rest].slice(0, HISTORY_LIMIT)
    })
  }, [])

  useEffect(() => {
    // Loopback is exempt from the secret; sent anyway in case that narrows.
    // The console watches its agent for as long as it is open, so it never
    // stops retrying.
    const nfc = new NFCClient(location.origin, {
      apiSecret: secret,
      maxReconnectAttempts: 0,
    })
    client.current = nfc

    const onConnected = () => setLink('open')
    const onDisconnected = () => setLink('closed')

    const onTag = (data: TagData) => {
      setTag(data)
      if (data.capabilities) setCapabilities(data.capabilities)
      if (data.error) {
        push({ kind: 'error', summary: `Read failed: ${data.error}`, detail: data.uid, ok: false })
        return
      }
      remember(data)
      push({
        kind: 'scan',
        summary: `${data.type || 'tag'} ${data.uid}`,
        detail: data.text || undefined,
        ok: true,
      })
    }

    const onRemoved = ({ uid }: { uid: string }) => {
      setTag(null)
      setCapabilities(null)
      push({ kind: 'removed', summary: 'Tag removed', detail: uid || undefined, ok: true })
    }

    const onStatus = (status: { connected: boolean; message?: string }) => {
      push({ kind: 'status', summary: status.message ?? 'device status', ok: status.connected })
    }

    const onError = (e: NFCErrorEvent) => {
      // The link indicator already reports a dropped socket; this feed is for
      // what the agent said.
      if (e.phase) return
      push({ kind: 'error', summary: e.error.message, detail: e.code, ok: false })
    }

    nfc.on('connected', onConnected)
    nfc.on('disconnected', onDisconnected)
    nfc.on('tagData', onTag)
    nfc.on('tagRemoved', onRemoved)
    nfc.on('deviceStatus', onStatus)
    nfc.on('error', onError)

    setLink('connecting')
    void nfc.connect().catch(() => {
      // The client retries on its own; the link indicator is the report.
    })

    return () => {
      nfc.off('connected', onConnected)
      nfc.off('disconnected', onDisconnected)
      nfc.off('tagData', onTag)
      nfc.off('tagRemoved', onRemoved)
      nfc.off('deviceStatus', onStatus)
      nfc.off('error', onError)
      void nfc.disconnect()
      client.current = null
    }
  }, [secret, push, remember])

  const connected = useCallback((): NFCClient => {
    const nfc = client.current
    if (!nfc?.isConnected()) throw new Error('not connected to the agent')
    return nfc
  }, [])

  const write = useCallback(
    async (records: WriteRecord[], lock = false) => {
      try {
        const res = await connected().write({ records, lock })
        push({ kind: 'write', summary: res.message || 'Write succeeded', ok: true })
        if (res.locked) push({ kind: 'lock', summary: 'Tag locked (permanent)', ok: true })
        return res
      } catch (err) {
        push({ kind: 'write', summary: `Write failed: ${describe(err)}`, ok: false })
        throw err
      }
    },
    [connected, push],
  )

  const lock = useCallback(async () => {
    try {
      const res = await connected().lock()
      push({ kind: 'lock', summary: 'Tag locked (permanent)', ok: true })
      return res
    } catch (err) {
      push({ kind: 'lock', summary: `Lock failed: ${describe(err)}`, ok: false })
      throw err
    }
  }, [connected, push])

  const transceive = useCallback(
    async (data: Uint8Array, raw: boolean) => {
      try {
        const res = await connected().transceive({ data, raw })
        push({ kind: 'apdu', summary: 'Raw exchange completed', ok: true })
        return res
      } catch (err) {
        push({ kind: 'apdu', summary: `Raw exchange failed: ${describe(err)}`, ok: false })
        throw err
      }
    },
    [connected, push],
  )

  const refreshCapabilities = useCallback(async () => {
    const caps = await connected().getCapabilities()
    setCapabilities(caps)
    return caps
  }, [connected])

  const clearEvents = useCallback(() => {
    setEvents([])
    setHistory([])
  }, [])

  return useMemo(
    () => ({
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
    }),
    [link, tag, capabilities, events, history, clearEvents, write, lock, refreshCapabilities, transceive],
  )
}

function describe(err: unknown): string {
  return err instanceof Error ? err.message : String(err)
}
