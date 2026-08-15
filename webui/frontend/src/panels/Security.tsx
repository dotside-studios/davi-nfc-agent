import { useCallback, useEffect, useState } from 'react'
import { fmtDateTime } from '../format'
import type { ControlState } from '../types'
import { useAction } from '../useControl'
import { ActionLink, Copyable, Dot, KV, Notice, Panel, Row } from '../ui'
import { Clients } from './Clients'
import { Devices } from './Devices'
import { Origins } from './Origins'

/**
 * Who may reach this agent and with what credential: paired devices, the
 * browser allowlist, the shared secret, and the certificate. One scrolling page
 * with the sections listed down the side.
 */

type SectionId = 'clients' | 'devices' | 'origins' | 'credentials' | 'trust' | 'certificate'

const SECTIONS: { id: SectionId; label: string }[] = [
  { id: 'clients', label: 'Connected clients' },
  { id: 'devices', label: 'Paired devices' },
  { id: 'origins', label: 'Browser origins' },
  { id: 'credentials', label: 'Credentials' },
  { id: 'trust', label: 'Device trust' },
  { id: 'certificate', label: 'Certificate' },
]

/**
 * Tracks which section is in view and scrolls to one on demand. Every section
 * stays rendered — the nav jumps between them rather than filtering, so the
 * whole subject can be read by scrolling.
 */
function useSectionNav(): [SectionId, (s: SectionId) => void] {
  const [active, setActive] = useState<SectionId>(() => {
    const raw = location.hash.replace(/^#\/?/, '').split('/')[1]
    return SECTIONS.some((s) => s.id === raw) ? (raw as SectionId) : 'clients'
  })

  const goTo = useCallback((id: SectionId) => {
    document.getElementById(`sec-${id}`)?.scrollIntoView({ block: 'start' })
    // Written without a hashchange, so the scroll is not fought by the
    // listener that would otherwise scroll it again.
    history.replaceState(null, '', `#/security/${id}`)
    setActive(id)
  }, [])

  // Land on the linked section after the first paint.
  useEffect(() => {
    const raw = location.hash.replace(/^#\/?/, '').split('/')[1]
    if (!SECTIONS.some((s) => s.id === raw)) return
    const frame = requestAnimationFrame(() => {
      document.getElementById(`sec-${raw}`)?.scrollIntoView({ block: 'start' })
    })
    return () => cancelAnimationFrame(frame)
  }, [])

  // Highlight whichever section is nearest the top of the scrollport.
  useEffect(() => {
    const scroller = document.querySelector('main')
    if (!scroller) return

    const onScroll = () => {
      // At the bottom the last section is the one being read, even though it
      // is too short to reach the top of the scrollport.
      if (scroller.scrollTop + scroller.clientHeight >= scroller.scrollHeight - 4) {
        setActive(SECTIONS[SECTIONS.length - 1].id)
        return
      }

      const threshold = scroller.getBoundingClientRect().top + 80
      let current = SECTIONS[0].id
      for (const s of SECTIONS) {
        const el = document.getElementById(`sec-${s.id}`)
        if (el && el.getBoundingClientRect().top <= threshold) current = s.id
      }
      setActive(current)
    }

    onScroll()
    scroller.addEventListener('scroll', onScroll, { passive: true })
    return () => scroller.removeEventListener('scroll', onScroll)
  }, [])

  return [active, goTo]
}

export function Security({ state }: { state: ControlState }) {
  const [active, goTo] = useSectionNav()
  const { security, origins, devices, clients } = state

  // Counts in the nav, so a problem is visible before scrolling to it.
  const badge = (id: SectionId) => {
    if (id === 'clients' && clients.length > 0) {
      return <span className="count"> ({clients.length})</span>
    }
    if (id === 'devices' && devices.length > 0) {
      return <span className="count"> ({devices.length})</span>
    }
    if (id === 'origins') {
      return (
        <>
          <span className="count"> ({origins.allowed.length})</span>
          {origins.blocked.length > 0 ? <span className="warn"> · {origins.blocked.length}</span> : null}
        </>
      )
    }
    if (id === 'certificate') {
      if (!security.cert) return <span className="warn"> none</span>
      if (security.cert.expired) return <span className="err"> expired</span>
    }
    return null
  }

  return (
    <div className="sections">
      <nav className="section-nav">
        <h3>Security</h3>
        {SECTIONS.map((s) => (
          <button
            key={s.id}
            type="button"
            className={active === s.id ? 'active' : undefined}
            onClick={() => goTo(s.id)}
          >
            {s.label}
            {badge(s.id)}
          </button>
        ))}
      </nav>

      <div className="section-body">
        <section id="sec-clients">
          <Clients state={state} />
        </section>
        <section id="sec-devices">
          <Devices state={state} />
        </section>
        <section id="sec-origins">
          <Origins state={state} />
        </section>
        <section id="sec-credentials">
          <CredentialsSection state={state} />
        </section>
        <section id="sec-trust">
          <TrustSection state={state} />
        </section>
        <section id="sec-certificate">
          <CertificateSection state={state} />
        </section>
      </div>
    </div>
  )
}

function CredentialsSection({ state }: { state: ControlState }) {
  const act = useAction()
  const { security } = state

  return (
    <Panel title="Credentials">
      <KV>
        <Row label="API secret">
          <Copyable value={security.apiSecret} hidden />
        </Row>
        <Row label="">
          <ActionLink
            danger
            run={() => act.mutateAsync({ name: 'security.rotateAPISecret' })}
            confirm={{
              prompt:
                'Rotate the API secret?\n\nEvery client and device using the old secret will be disconnected and must be reconfigured. Paired devices hold their own credentials and are not affected.',
              phrase: 'rotate',
            }}
          >
            regenerate API secret
          </ActionLink>
        </Row>

        <Row label="Pairing PIN">
          <Copyable value={security.pairingPIN ?? ''} />
        </Row>
        <Row label="">
          {security.pairingPIN ? (
            <ActionLink
              run={() => act.mutateAsync({ name: 'security.rotatePairingPIN' })}
              confirm={{ prompt: 'Generate a fresh pairing PIN? Any pairing URL already handed out stops working.' }}
            >
              regenerate PIN
            </ActionLink>
          ) : (
            <span className="dim">Pairing server is disabled.</span>
          )}
        </Row>

        <Row label="Console sessions">
          {security.controlSessions} open
          {security.controlSessions > 0 ? (
            <>
              {' · '}
              <ActionLink
                danger
                run={() => act.mutateAsync({ name: 'security.revokeControlSessions' })}
                confirm={{
                  prompt:
                    'Sign out every control center session?\n\nThis one included — you will need to reopen the console from the tray menu.',
                }}
              >
                sign out all
              </ActionLink>
            </>
          ) : null}
        </Row>
      </KV>

      <Notice>
        The API secret is shown here in full. This page already required loopback access, a
        same-origin request and a token minted from the tray — a higher bar than reading the file it
        comes from, so hiding it would protect nothing.
      </Notice>
    </Panel>
  )
}

function TrustSection({ state }: { state: ControlState }) {
  const { security } = state

  return (
    <Panel title="Device trust">
      <KV>
        <Row label="Public key pin">
          <Copyable value={security.publicKeyPin ?? ''} />
        </Row>
      </KV>
      <div className="dim" style={{ margin: '4px 0' }}>
        Phones and readers authenticate this agent by pinning that value rather than by trusting a
        certificate authority. It survives certificate reissues — which happen whenever this
        machine's addresses change — so a device that pins it keeps working when the machine moves
        network. Pin this, never the certificate.
      </div>

      <KV>
        <Row label="Local CA">
          {security.caInstalled ? (
            <Dot state="warn">installed in the system trust store</Dot>
          ) : (
            <Dot state="ok">not installed</Dot>
          )}
        </Row>
        {security.caFingerprint ? (
          <Row label="CA fingerprint">
            <Copyable value={security.caFingerprint} />
          </Row>
        ) : null}
      </KV>

      {security.caInstalled ? (
        <Notice kind="warn">
          A certificate authority in a trust store can sign for <b>any</b> name, not just this agent.
          Whoever holds its key can intercept this machine's traffic. Providing a certificate for a
          name you control is preferable wherever you can arrange it.
        </Notice>
      ) : null}
    </Panel>
  )
}

function CertificateSection({ state }: { state: ControlState }) {
  const act = useAction()
  const { security, server } = state
  const cert = security.cert
  const uncovered = cert ? missingAddresses(cert.hosts, server.localIPs) : []

  return (
    <Panel
      title="Certificate"
      tools={
        <ActionLink
          run={() => act.mutateAsync({ name: 'security.regenerateCertificate' })}
          confirm={{
            prompt:
              'Regenerate the TLS certificate and restart the listeners?\n\nConnected clients and devices will be dropped. Devices that pin the public key are unaffected; browsers may need the new certificate trusted.',
          }}
        >
          regenerate
        </ActionLink>
      }
    >
      {!cert ? (
        <div className="stack">
          <Dot state="warn">Not serving TLS.</Dot>
          <div className="dim">
            Traffic to this agent is unencrypted. Browsers will refuse a{' '}
            <span className="mono">wss://</span> connection outright, with no warning to click
            through.
          </div>
        </div>
      ) : (
        <>
          <KV>
            <Row label="Status">
              {cert.expired ? (
                <Dot state="err">Expired {fmtDateTime(cert.notAfter)}</Dot>
              ) : cert.expiresInHr < 24 * 14 ? (
                <Dot state="warn">Expires {fmtDateTime(cert.notAfter)}</Dot>
              ) : (
                <Dot state="ok">Valid until {fmtDateTime(cert.notAfter)}</Dot>
              )}
            </Row>
            <Row label="Subject">{cert.subject || <span className="dim">—</span>}</Row>
            <Row label="Issuer">
              {cert.selfSigned ? <span className="warn">self-signed</span> : cert.issuer}
            </Row>
            <Row label="Valid from">{fmtDateTime(cert.notBefore)}</Row>
            <Row label="Fingerprint">
              <Copyable value={cert.fingerprint} />
            </Row>
            <Row label="Covers">
              {cert.hosts.length === 0 ? (
                <span className="err">no names — no client can verify this certificate</span>
              ) : (
                <span className="mono">{cert.hosts.join(', ')}</span>
              )}
            </Row>
          </KV>

          {uncovered.length > 0 ? (
            <Notice kind="warn">
              This certificate does not cover <span className="mono">{uncovered.join(', ')}</span>. A
              client reaching the agent on one of those addresses will fail to verify it — which a
              browser reports as a plain connection failure, indistinguishable from the agent being
              down. Regenerating picks up the machine's current addresses.
            </Notice>
          ) : null}
        </>
      )}
    </Panel>
  )
}

/** Local addresses the certificate does not name — the usual cause of a
 *  connection failure after a machine changes network. */
function missingAddresses(hosts: string[], localIPs: string[]): string[] {
  const covered = new Set(hosts.map((h) => h.toLowerCase()))
  return localIPs.filter((ip) => !covered.has(ip.toLowerCase()))
}
