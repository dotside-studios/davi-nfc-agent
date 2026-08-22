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

The `Origin` check reads `RemoteAddr`, never `X-Forwarded-For` — a forwarding
header is attacker-controlled and is not evidence of locality.

**The origin allowlist is not consulted.** An entry in `allowed-origins.json`
authorises a console to *read tags*; it never confers the ability to revoke a
device or rotate the secret. The control routes are also served without the
permissive CORS headers the client endpoints carry.

Use **Security → sign out all** to end every console session at once, including
your own.

## Tabs

**Overview** — the page an operator works from, split a third to two thirds.

The left third holds state and the knobs that get turned: start/stop, reader
selection, mode, card-type filter, port, restart and quit, plus the server URLs
and certificate summary needed to decide whether to turn any of them.

The right two thirds hold what the reader is doing: the tag on it now with its
capabilities and record list, the tail of the log, and every distinct tag seen
this session with a scan count and when it was last presented. That history is
in the console only — the agent does not persist it.

**Tag** — inspector and NDEF composer.

- Reads: UID, type, technology, the record tree, and the capabilities the agent
  determined for that tag (memory, usable capacity, writable, lockable,
  password support).
- Writes: every record type the agent supports — text, URI, smart poster,
  vCard, MIME, geo, tel/sms/mailto, Android Application Records and fully raw
  records — with a live size estimate against the tag's actual capacity.
- Also erase, and lock (irreversible, and gated behind typing `lock`).
- A **raw exchange (APDU)** console: hex in, hex and ASCII out, with ISO 7816
  status words decoded, per-exchange timing, presets built from the commands
  `nfc/apdu.go` already constructs, and a toggle between APDU-level and
  framing-level exchange. Refused in read-only mode, since the agent cannot
  tell a `SELECT` from a write to a configuration page.

The console writes over the ordinary client endpoint, exactly as an application
would, so there is one implementation of the write path.

**Activity** — tag events on the left, agent log on the right, both filterable
and both filling the window. They are read together in practice: a failed write
and the line explaining it arrive at the same moment. Events export as NDJSON,
the log downloads as text.

**Security** — who may reach this agent and with what credential. One page with
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

The reader and server settings are deliberately *not* here — they live on
Overview, because those get turned while working and these do not.

## Logs

The agent logs through the standard library logger, which writes to stderr and
nowhere else. Started from a desktop launcher there is no stderr to read, so
every certificate warning, refused origin and reader failure was previously
discarded as it was produced.

The agent now also keeps the last 5000 lines in memory. They are visible under
**Activity**, streamed live, and downloadable as text for a bug report. Severity is
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
  "requirePairedDevice": false,
  "readerFeedback": false
}
```

| Key | Meaning |
|---|---|
| `mode` | `readwrite`, `read` or `write` |
| `cardTypes` | Card-type allowlist. Empty means all types |
| `devicePath` | Pinned reader. Empty means auto-detect |
| `port` | Agent port. `0` means the built-in default. Takes effect when the listener is next bound, at startup or on **Restart servers** |
| `requirePairedDevice` | Admit only devices holding a paired credential |
| `readerFeedback` | Flash the reader's LED and sound its buzzer at what it reads and writes |

An explicit flag still wins: passing `-device` or `-device-port` means it for
that run, and `-require-paired-devices` cannot be withdrawn by the file or by
the console, where the switch is shown disabled rather than springing back. No
file is written until something is deliberately saved, so its absence keeps
meaning "never configured".

Credentials are deliberately not in this file — the API secret, paired devices
and origin allowlist keep their own.

## Where a setting lives

The file persists a preference; the **agent holds it**. Saving anywhere, from
the console or the tray's feedback toggle, writes to `settings.json`, and the
store's change hook is the single path from there to the running agent:

```
console ─┐                        ┌─ agent.ApplySettings   (what is in force)
         ├─ settings.Store.Update ┤
tray  ───┘                        └─ tray menu             (redrawn from the agent)
```

Nothing displays a preference from the file. The console's `settings` block in
the state snapshot is `Agent.Settings()`, what the agent is set to right now, so
a mode switched from the tray shows in the console, a pairing requirement the
command line locked on shows as on, and a reader mode restored from disk is on
the reader rather than only in the file.

The tray and the console still differ in reach, and deliberately: the tray's
mode and card-type menus change what the agent is doing for this session, the
console's change what it does from now on. Both are visible in both places,
because both read the agent.

A saved preference means "make the agent match the file", so the next save from
either place ends a session-only change made from the tray. The tray menu is
redrawn from the agent when that happens, so it shows the mode now in force
rather than the one that was picked.

## Building

`webui/frontend/dist` is committed and embedded with `go:embed`, so `go build .`
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

To drive the console without a reader — for screenshots, or to check a panel
that needs paired devices and blocked origins to be interesting — there is a
harness that serves the real control handler over a seeded host and a stubbed
tag feed. It is skipped unless `SCREENSHOT_ADDR` is set:

```bash
SCREENSHOT_ADDR=127.0.0.1:9911 SCREENSHOT_TOKEN_FILE=/tmp/tok \
  go test -run TestScreenshotHarness -timeout 20m ./webui/
# then open http://127.0.0.1:9911/control/session?token=$(cat /tmp/tok)
```

## Leaving it out

The console is its own package. `webui/` holds the whole thing — the gate, the
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

Nothing in `webui` imports the agent. It declares the ~35 methods it needs as
`webui.Host`, and `webui_host.go` in `package main` implements them — so the
console's entire reach into the agent is readable in one file, and its tests run
against a fake host with no hardware behind them.

`go build -tags nowebui .` produces an agent without the control center: no
`/control` routes, no privileged API, no tray entry, and no frontend in the
binary. Roughly 820 KB smaller, and the console's strings are absent entirely.

```bash
make build-nowebui
make test-nowebui     # the suite under the same tag
```

Only three files in `package main` carry `//go:build !nowebui`:

```
webui_enabled.go      the one wiring entry point
webui_host.go         the Host implementation
systray_console.go    the tray entry
```

`webui_disabled.go` supplies the stubs under the opposite tag. Call sites in
`agent.go`, `main.go` and `systray.go` hold a nil `*Console` and tolerate it, so
none of them needs a build tag of its own.

Dropping the console from a custom build is therefore a tag, not a patch — and
deleting `webui/` outright leaves only those four files to remove.

**The agent's protocol is unaffected.** Raw tag exchanges, settings
persistence and the log ring stay in either build — each is reachable without
the console, and the transceive channel in particular is part of the client API
rather than a console feature.

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

Tag operations are absent here on purpose — the console does those over the
client API described in [api.md](api.md).
