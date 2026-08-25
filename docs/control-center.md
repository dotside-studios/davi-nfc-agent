# Control Center

A web console served by the agent for managing it and for advanced tag
operations.

## Opening it

Choose **Open Control Center** from the tray menu. That mints a single-use token,
opens your browser at `https://localhost:9470/control/session?token=…`, and
exchanges the token for a session cookie.

The token is spent on first use and expires after two minutes, so a URL that
ends up in shell history or browser autocomplete is already useless. Sessions
last 12 hours.

There is no way in other than the tray. See
[Who can open it](#who-can-open-it) below.

> **On a self-signed certificate**, the browser shows its usual warning the
> first time and you click through. This is the one browser client that works on
> a fresh install without `-install-ca`: a page visit can be accepted manually,
> whereas a bare `wss://` connection to an untrusted certificate fails outright.

## Who can open it

The console is privileged. It rotates the API secret, revokes device
credentials, edits the origin allowlist and can lock a tag irreversibly. Three
checks must all pass on every control request:

| Check | Refuses |
|---|---|
| Loopback only | Administering the agent from another host |
| `Origin`/`Referer` is the agent itself | A page you happen to be visiting driving the console via your cookie |
| A tray-minted session cookie | Another local account or process pointing at the port |

The `Origin` check reads `RemoteAddr`, never `X-Forwarded-For`: a forwarding
header is attacker-controlled and is not evidence of locality.

The origin allowlist is not consulted. An entry in `allowed-origins.json`
authorises a console to *read tags*; it never confers the ability to revoke a
device or rotate the secret. The control routes are also served without the
permissive CORS headers the client endpoints carry.

Use **Security → sign out all** to end every console session at once, including
your own.

## Tabs

**Overview**: the page an operator works from, split a third to two thirds.

The left third holds state and the knobs that get turned: start/stop, reader
selection, mode, card-type filter, port, restart and quit, plus the server URLs
and certificate summary needed to decide whether to turn any of them.

The right two thirds hold what the reader is doing: the tag on it now with its
capabilities and record list, the tail of the log, and every distinct tag seen
this session with a scan count and when it was last presented. That history is
in the console only; the agent does not persist it.

**Tag**: inspector and NDEF composer.

- Reads: UID, type, technology, the record tree, and the capabilities the agent
  determined for that tag (memory, usable capacity, writable, lockable,
  password support).
- Writes: every record type the agent supports (text, URI, smart poster,
  vCard, MIME, geo, tel/sms/mailto, Android Application Records and fully raw
  records), with a live size estimate against the tag's actual capacity.
- Also erase, and lock (irreversible, and gated behind typing `lock`).
- A **raw exchange (APDU)** console: hex in, hex and ASCII out, with ISO 7816
  status words decoded, per-exchange timing, presets built from the commands
  `nfc/apdu.go` already constructs, and a toggle between APDU-level and
  framing-level exchange. Refused in read-only mode, since the agent cannot
  tell a `SELECT` from a write to a configuration page.

The console writes over the ordinary client endpoint, exactly as an application
would, so there is one implementation of the write path.

**Activity**: tag events on the left, agent log on the right, both filterable
and both filling the window. They are read together: a failed write and the line
explaining it arrive at the same moment. Events export as NDJSON,
the log downloads as text.

**Security**: who may reach this agent and with what credential. One page with
section links down the side that scroll to each part; everything stays on screen
so it can be read straight through. Each section is linkable:
`#/security/origins`, `#/security/certificate` and so on.

| Section | Holds |
|---|---|
| Connected clients | Every application on the client endpoint: origin, address, how long it has been connected, how many writes and locks it has issued, and a disconnect |
| Paired devices | Platform, pairing time, last seen, whether connected now; revoke one or all; the paired-device policy |
| Browser origins | The allowlist, every origin refused since startup with a one-click **allow**, and the session override |
| Credentials | API secret, pairing PIN, open console sessions, and the rotations for each |
| Device trust | Public key pin, local CA status and fingerprint, and **Trust this agent in browsers** when no CA is installed |
| Certificate | Expiry, issuer, fingerprint, the names it covers, and a warning when the agent is reachable on an address it omits |

The reader and server settings are not here; they live on
Overview, because those get turned while working and these do not.

## Logs

The agent logs through the standard library logger, which writes to stderr.
Started from a desktop launcher there is no stderr to read, so a certificate
warning, refused origin or reader failure would be discarded as it was
produced.

The agent keeps the last 5000 lines in memory. They are visible under
**Activity**, streamed live, and downloadable as text for a bug report. Severity
is inferred from the message text, which is good enough to filter on but not to
rely on otherwise.

The buffer is in memory only, and is lost when the agent exits.

## Browser origins

The agent only accepts WebSocket upgrades whose `Origin` matches its own
host:port. Otherwise any site the operator visits could drive the reader,
including permanently locking cards. A console served from anywhere else, which
includes every hosted one, must be allowed. The Davi consoles already are.

When a page is refused, the tray offers it as *"Allow example.com"*, and one
click admits it and persists the choice, with no restart. **Browser origins** in
the console lists the allowlist, every origin refused since startup, and the
session override. To preload one instead, at first run or for an unattended
install:

```bash
./davi-nfc-agent -allowed-origins "console.example.com,localhost:3002"
# or
DAVI_NFC_ALLOWED_ORIGINS="console.example.com" ./davi-nfc-agent
```

Entries are matched on host:port. Full URLs are accepted and reduced, so
`https://console.example.com` and `console.example.com` are equivalent. The
allowlist lives in `allowed-origins.json` in the config directory.

**Allow any origin (this session)** turns the check off until the agent
restarts. It is never persisted, and it is not a way to skip configuring an
origin: while it is on, any page the operator opens can read, write and
permanently lock cards.

A trusted certificate is a separate requirement. The allowlist decides *who may
connect*; TLS decides whether the browser will open the connection at all. A
`wss://` connection to an untrusted certificate fails outright, and unlike a
page visit there is no warning to click through. See
[TLS & Certificates](api.md#tls--certificates).

## Where a preference lives

The agent holds every preference and nothing writes them anywhere. A change made
in the console or from a tray menu reaches the agent, and both surfaces redraw
from it, so the two cannot disagree about what the agent is doing:

```
console ─┐                          ┌─ agent            (what is in force)
         ├─ Agent.SetReaderMode &c ─┤
tray  ───┘                          └─ tray menu        (redrawn from the agent)
```

The console's `settings` block in the state snapshot is `Agent.Preferences()`,
what the agent is set to right now.

A change lasts as long as the agent runs. What it starts with comes from
`agent.Config`, which the command fills from its flags, so a preference that
should survive a restart is set at launch rather than saved.

A program embedding the agent that wants an operator's change to outlive the
process reads it back from the agent and persists it however it likes. That is
the same arrangement the origin allowlist and the device registry are under,
each of which keeps its own file.

## Building

`webui/frontend/dist` is committed and embedded with `go:embed`, so `go build ./cmd/davi-nfc-agent`
works with no Node installed. After changing anything under
`webui/frontend/src`:

```bash
make webui     # npm install && npm run build
git add webui/frontend/dist
```

For hot reload against a running agent:

```bash
make webui-dev                                  # proxies to localhost:9470
VITE_AGENT=https://localhost:9480 make webui-dev
```

The dev server proxies `/control` and `/ws`, so it drives a real agent rather
than a mock. If `webui/frontend/dist` is missing entirely the agent still starts
and serves its protocol; the root falls back to the plain-text banner.

To drive the console without a reader, for screenshots or to check a panel that
needs paired devices and blocked origins to be interesting, there is a
harness that serves the real control handler over a seeded host and a stubbed
tag feed. It is skipped unless `SCREENSHOT_ADDR` is set:

```bash
SCREENSHOT_ADDR=127.0.0.1:9911 SCREENSHOT_TOKEN_FILE=/tmp/tok \
  go test -run TestScreenshotHarness -timeout 20m ./webui/
# then open http://127.0.0.1:9911/control/session?token=$(cat /tmp/tok)
```

## Leaving it out

The console is its own package. `webui/` holds the whole thing: the gate, the
routes, the state snapshot, the dispatcher, and the frontend it embeds:

```
webui/
  webui.go            the Host interface, the package's only view of the agent
  auth.go             the three-check gate
  server.go           routes, the live socket
  state.go            the state snapshot
  actions.go          the action dispatcher
  embed.go            go:embed of frontend/dist
  frontend/           the Vite + React console
    src/
    dist/             built and committed
```

`webui` does not import the agent. It declares the ~35 methods it needs as
`webui.Host`, and `agent/console/host.go` implements them, so the console's
entire reach into the agent is readable in one file, and its tests run against a
fake host with no hardware behind them.

`go build -tags nowebui ./cmd/davi-nfc-agent` produces an agent without the
control center: no
`/control` routes, no privileged API, no tray entry, and no frontend in the
binary. Roughly 820 KB smaller, and the console's strings are absent entirely.

```bash
make build-nowebui
make test-nowebui     # the suite under the same tag
```

Only three files carry `//go:build !nowebui`:

```
agent/console/console.go         the one wiring entry point
agent/console/host.go            the Host implementation
agent/tray/systray_console.go    the tray entry
```

`agent/console/console_nowebui.go` and `agent/tray/console_nowebui.go` supply
the stubs under the opposite tag.

The agent itself needs no tag either way. It knows the console only as
`agent.Console`, two handlers to mount and a redraw signal, so the dependency
runs one way: `agent/console` imports `agent`, never the reverse, and `main` is
the only place that wires the two together. A build without a console leaves
that interface nil, and every call site already tolerates it.

Dropping the console from a custom build is therefore a tag, not a patch, and
deleting `webui/` outright leaves only `agent/console/` to remove.

The agent's protocol is unaffected. Raw tag exchanges and the log ring stay
in either build. Both are reachable without the console, and the transceive
channel is part of the client API rather than a console feature.

## API

The console's endpoints, all under the gate described in [Who can open it](#who-can-open-it).
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
`devices.setRequirePaired`, `clients.disconnect`, `origins.allow`, `origins.revoke`,
`origins.setAllowAny`, `security.rotateAPISecret`, `security.rotatePairingPIN`,
`security.installCA`, `security.regenerateCertificate`, `security.revokeControlSessions`.

Tag operations are absent here on purpose: the console does those over the
client API described in [api.md](api.md).
