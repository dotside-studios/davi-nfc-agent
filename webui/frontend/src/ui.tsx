import { useMutation } from '@tanstack/react-query'
import { useCallback, useEffect, useState, type ReactNode } from 'react'
import { copy } from './api'

/** Panel: a bordered box with the pale blue title bar everything sits in. */
export function Panel({
  title,
  tools,
  flush,
  fill,
  children,
}: {
  title: ReactNode
  tools?: ReactNode
  flush?: boolean
  /** Claim the leftover height instead of shrink-wrapping. */
  fill?: boolean
  children: ReactNode
}) {
  return (
    <section className={fill ? 'panel fill' : 'panel'}>
      <h2>
        <span>{title}</span>
        <span className="spacer" />
        {tools ? <span className="tools">{tools}</span> : null}
      </h2>
      <div className={flush ? 'body flush' : 'body'}>{children}</div>
    </section>
  )
}

/** A key/value list. Children are alternating <dt>/<dd> via the Row helper. */
export function KV({ children }: { children: ReactNode }) {
  return <dl className="kv">{children}</dl>
}

export function Row({ label, children }: { label: ReactNode; children: ReactNode }) {
  return (
    <>
      <dt>{label}</dt>
      <dd>{children}</dd>
    </>
  )
}

/** A status dot plus label, used wherever something is up or down. */
export function Dot({ state, children }: { state: 'ok' | 'warn' | 'err' | 'off'; children?: ReactNode }) {
  const cls = state === 'off' ? 'dot' : `dot ${state}`
  return (
    <span className="nowrap">
      <span className={cls} /> {children}
    </span>
  )
}

/** A value with a copy link beside it. */
export function Copyable({
  value,
  display,
  hidden,
}: {
  value: string
  display?: ReactNode
  /** Masks the value until revealed. */
  hidden?: boolean
}) {
  const [shown, setShown] = useState(!hidden)
  const [copied, setCopied] = useState(false)

  useEffect(() => {
    if (!copied) return
    const t = window.setTimeout(() => setCopied(false), 1200)
    return () => window.clearTimeout(t)
  }, [copied])

  if (!value) return <span className="dim">not set</span>

  return (
    <span className="row tight">
      <span className="secret">{shown ? (display ?? value) : '•'.repeat(Math.min(value.length, 24))}</span>
      {hidden ? (
        <button type="button" className="link" onClick={() => setShown((s) => !s)}>
          {shown ? 'hide' : 'show'}
        </button>
      ) : null}
      <button
        type="button"
        className="link"
        onClick={() => {
          void copy(value).then(setCopied)
        }}
      >
        {copied ? 'copied' : 'copy'}
      </button>
    </span>
  )
}

/**
 * A button that runs an async action, disabling itself while in flight and
 * showing any error beside itself rather than in a corner toast.
 */
export function ActionLink({
  run,
  children,
  confirm,
  danger,
  disabled,
  onDone,
}: {
  run: () => Promise<unknown>
  children: ReactNode
  /** Text the operator must type to proceed. For irreversible operations. */
  confirm?: { prompt: string; phrase?: string }
  danger?: boolean
  disabled?: boolean
  onDone?: () => void
}) {
  const mutation = useMutation({ mutationFn: run, onSuccess: () => onDone?.() })

  const go = useCallback(() => {
    if (confirm) {
      if (confirm.phrase) {
        const typed = window.prompt(`${confirm.prompt}\n\nType "${confirm.phrase}" to confirm.`)
        if (typed !== confirm.phrase) return
      } else if (!window.confirm(confirm.prompt)) {
        return
      }
    }
    mutation.mutate()
  }, [mutation, confirm])

  return (
    <span className="row tight">
      <button
        type="button"
        className={danger ? 'link danger' : 'link'}
        onClick={go}
        disabled={mutation.isPending || disabled}
      >
        {mutation.isPending ? 'working…' : children}
      </button>
      {mutation.error ? (
        <span className="err">
          {mutation.error instanceof Error ? mutation.error.message : String(mutation.error)}
        </span>
      ) : null}
    </span>
  )
}

/** An empty-state line for a table or list. */
export function Empty({ children }: { children: ReactNode }) {
  return <div className="empty">{children}</div>
}

/** A boxed message. Used for the things an operator must not miss. */
export function Notice({ kind, children }: { kind?: 'warn' | 'err'; children: ReactNode }) {
  return <div className={kind ? `notice ${kind}` : 'notice'}>{children}</div>
}
