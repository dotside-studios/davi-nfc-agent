# Control Center

A web console served by the agent itself, for the things the tray and the
command-line flags cannot do: reading the log, writing NDEF messages by hand,
inspecting a tag, revoking one device, and keeping settings across a restart.

## Opening it

Choose **Open Control Center** from the tray menu. That mints a single-use token,
opens your browser at `https://localhost:9470/control/session?token=…`, and
exchanges the token for a session cookie.

The token is spent on first use and expires after two minutes, so a URL that
ends up in shell history or browser autocomplete is already useless. Sessions
last 12 hours.

There is no way in other than the tray. That is deliberate — see
[Access](#access) below.

> **On a self-signed certificate**, the browser shows its usual warning the
> first time and you click through. This is the one browser client that works on
> a fresh install without `-install-ca`: a page visit can be accepted manually,
> whereas a bare `wss://` connection to an untrusted certificate fails outright.

## Access

The console is privileged. It rotates the API secret, revokes device
credentials, edits the origin allowlist and can lock a tag irreversibly. Three
checks must all pass on every control request:

| Check | Refuses |
|---|---|
| Loopback only | Administering the agent from another host |
| `Origin`/`Referer` is the agent itself | A page you happen to be visiting driving the console via your cookie |
| A tray-minted session cookie | Another local account or process pointing at the port |

The `Origin` check reads `RemoteAddr`, never `X-Forwarded-For` — a forwarding
header is attacker-controlled and is not evidence of locality.

**The origin allowlist is not consulted.** An entry in `allowed-origins.json`
authorises a console to *read tags*; it never confers the ability to revoke a
device or rotate the secret. The control routes are also served without the
permissive CORS headers the client endpoints carry.

Use **Security → sign out all** to end every console session at once, including
your own.

## Panels

**Dashboard** — agent status, URLs, addresses, certificate expiry and the names
it covers, pairing PIN, and a device summary. Everything the tray shows, at once
and selectable.

**Live** — scans, writes, locks and errors as they happen, filterable by kind,
pausable, exportable as NDJSON. The tray only ever shows the card currently on
the reader, so a tag presented and taken away otherwise leaves no trace.

**Tag** — inspector and NDEF composer.

- Reads: UID, type, technology, the record tree, and the capabilities the agent
  determined for that tag (memory, usable capacity, writable, lockable,
  password support).
- Writes: every record type the agent supports — text, URI, smart poster,
  vCard, MIME, geo, tel/sms/mailto, Android Application Records and fully raw
  records — with a live size estimate against the tag's actual capacity.
- Also erase, and lock (irreversible, and gated behind typing `lock`).

The console writes over the ordinary client endpoint, exactly as an application
would, so there is one implementation of the write path.

**Devices** — paired devices with platform, pairing time, last seen, and whether
they are connected right now. Each row revokes on its own; the tray can only
revoke all of them, which makes removing one lost phone cost every other phone
its pairing.

**Origins** — the allowlist, plus every origin refused since the agent started,
each with a one-click **allow**. The tray offers a blocked origin only while
that menu is open.

**Security** — API secret, pairing PIN, public key pin, CA status and
fingerprint, full certificate detail, and warnings when the certificate does not
cover an address the agent is reachable on.

**Log** — the agent's log output, filterable by level and text, downloadable.
See below.

**Settings** — reader mode, card-type filter, reader selection and port, all
written to `settings.json`.

## Logs

The agent logs through the standard library logger, which writes to stderr and
nowhere else. Started from a desktop launcher there is no stderr to read, so
every certificate warning, refused origin and reader failure was previously
discarded as it was produced.

The agent now also keeps the last 5000 lines in memory. They are visible under
**Log**, streamed live, and downloadable as text for a bug report. Severity is
inferred from the message text — good enough to filter on, not something to rely
on otherwise.

Nothing is written to disk. The buffer is lost when the agent exits.

## settings.json

Lives beside the other config files:

```json
{
  "mode": "readwrite",
  "cardTypes": [],
  "devicePath": "",
  "port": 0,
  "requirePairedDevice": false
}
```

| Key | Meaning |
|---|---|
| `mode` | `readwrite`, `read` or `write` |
| `cardTypes` | Card-type allowlist. Empty means all types |
| `devicePath` | Pinned reader. Empty means auto-detect |
| `port` | Agent port. `0` means the built-in default. Applied at startup only |
| `requirePairedDevice` | Admit only devices holding a paired credential |

An explicit flag still wins: passing `-device` or `-device-port` means it for
that run. No file is written until something is deliberately saved, so its
absence keeps meaning "never configured".

Credentials are deliberately not in this file — the API secret, paired devices
and origin allowlist keep their own.

## Building

`webui/dist` is committed and embedded with `go:embed`, so `go build .` works
with no Node installed. After changing anything under `webui/src`:

```bash
make webui     # npm install && npm run build
git add webui/dist
```

For hot reload against a running agent:

```bash
make webui-dev                                  # proxies to localhost:9470
VITE_AGENT=https://localhost:9480 make webui-dev
```

The dev server proxies `/control` and `/ws`, so it drives a real agent rather
than a mock. If `webui/dist` is missing entirely the agent still starts and
serves its protocol; the root falls back to the plain-text banner.

## API

The console's endpoints, all under the gate described in [Access](#access).
They are not a public API and carry no compatibility guarantee.

| Route | Purpose |
|---|---|
| `GET /control/session?token=` | Exchange a handoff token for a session cookie |
| `POST /control/signout` | End this session |
| `GET /control/state` | Full agent state snapshot |
| `GET /control/logs?since=` | Buffered log entries after a sequence number |
| `POST /control/action` | `{"action": "...", "params": {...}}` |
| `GET /control/ws` | Live state snapshots and log lines |

Actions: `agent.start`, `agent.stop`, `agent.restartServers`, `agent.quit`,
`reader.selectDevice`, `reader.setMode`, `reader.setCardTypes`,
`settings.save`, `devices.revoke`, `devices.revokeAll`,
`devices.setRequirePaired`, `origins.allow`, `origins.revoke`,
`origins.setAllowAny`, `security.rotateAPISecret`, `security.rotatePairingPIN`,
`security.regenerateCertificate`, `security.revokeControlSessions`.

Tag operations are absent here on purpose — the console does those over the
client API described in [api.md](api.md).
