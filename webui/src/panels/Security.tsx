import { useEffect, useState } from 'react'
import { fmtDateTime } from '../format'
import type { ControlState } from '../types'
import { useAction } from '../useControl'
import { ActionLink, Copyable, Dot, KV, Notice, Panel, Row } from '../ui'
import { Devices } from './Devices'
import { Origins } from './Origins'

/**
 * Who may reach this agent and with what credential: paired devices, the
 * browser allowlist, the shared secret, and the certificate. One tab with
 * sections down the side, because they are read as one subject and none of
 * them fills a tab alone.
 *
 * The reader and server settings deliberately live on Overview instead — those
 * get turned while working, and these do not.
 */

type SectionId = 'devices' | 'origins' | 'credentials' | 'trust' | 'certificate'

const SECTIONS: { id: SectionId; label: string }[] = [
  { id: 'devices', label: 'Paired devices' },
  { id: 'origins', label: 'Browser origins' },
  { id: 'credentials', label: 'Credentials' },
  { id: 'trust', label: 'Device trust' },
  { id: 'certificate', label: 'Certificate' },
]

/** Section in the hash after the tab, so one can be linked to directly. */
function useSection(): [SectionId, (s: SectionId) => void] {
  const read = (): SectionId => {
    const raw = location.hash.replace(/^#\/?/, '').split('/')[1]
    return SECTIONS.some((s) => s.id === raw) ? (raw as SectionId) : 'devices'
  }

  const [section, setSection] = useState<SectionId>(read)

  useEffect(() => {
    const onHash = () => setSection(read())
    window.addEventListener('hashchange', onHash)
    return () => window.removeEventListener('hashchange', onHash)
  }, [])

  return [
    section,
    (next: SectionId) => {
      location.hash = `#/security/${next}`
      setSection(next)
    },
  ]
}

export function Security({ state }: { state: ControlState }) {
  const [section, setSection] = useSection()
  const { security, origins, devices } = state

  // Counts in the nav, so a problem is visible without opening the section.
  const badge = (id: SectionId) => {
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
            className={section === s.id ? 'active' : undefined}
            onClick={() => setSection(s.id)}
          >
            {s.label}
            {badge(s.id)}
          </button>
        ))}
      </nav>

      <div className="section-body">
        {section === 'devices' ? <Devices state={state} /> : null}
        {section === 'origins' ? <Origins state={state} /> : null}
        {section === 'credentials' ? <CredentialsSection state={state} /> : null}
        {section === 'trust' ? <TrustSection state={state} /> : null}
        {section === 'certificate' ? <CertificateSection state={state} /> : null}
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
