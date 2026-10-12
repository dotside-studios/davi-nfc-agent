# agent/console

The Control Center: the gate, the routes, the state snapshot, the action
dispatcher, the adapter onto the agent, and the frontend it embeds. What it
does for an operator is in [docs/control-center.md](../../docs/control-center.md).

## Building the frontend

`agent/console/frontend/dist` is committed and embedded with `go:embed`, so `go build ./cmd/davi-nfc-agent`
works with no Node installed. After changing anything under
`agent/console/frontend/src`:

```bash
make webui     # npm install && npm run build
git add agent/console/frontend/dist
```

For hot reload against a running agent:

```bash
make webui-dev                                  # proxies to localhost:9470
VITE_AGENT=https://localhost:9480 make webui-dev
```

The dev server proxies `/control` and `/ws`, so it drives a real agent rather
than a mock. If `agent/console/frontend/dist` is missing entirely the agent still starts
and serves its protocol; the root falls back to the plain-text banner.

To drive the console without a reader, for screenshots or to check a panel that
needs paired devices and blocked origins to be interesting, there is a
harness that serves the real control handler over a seeded host and a stubbed
tag feed. It is skipped unless `SCREENSHOT_ADDR` is set:

```bash
SCREENSHOT_ADDR=127.0.0.1:9911 SCREENSHOT_TOKEN_FILE=/tmp/tok \
  go test -run TestScreenshotHarness -timeout 20m ./agent/console/
# then open http://127.0.0.1:9911/control/session?token=$(cat /tmp/tok)
```

## Package layout and the nowebui build

The console is one package. `agent/console/` holds the whole thing: the gate,
the routes, the state snapshot, the dispatcher, the adapter onto the agent, and
the frontend it embeds:

```
agent/console/
  doc.go              the package doc
  console.go          the wiring entry point: New, Endpoints
  host_contract.go    the Host interface, the console's only view of the agent
  host.go             the Host implementation over a live agent
  auth.go             the three-check gate
  server.go           routes, the live socket
  state.go            the state snapshot
  actions.go          the action dispatcher
  preferences.go      the preference DTO, shared with the tray
  embed.go            go:embed of frontend/dist
  frontend/           the Vite + React console
    src/
    dist/             built and committed
```

The serving half reaches the agent only through `Host`, the ~35 methods declared
in `host_contract.go` and implemented in `host.go`. The console's entire reach
into the agent is readable in one file, and its tests run against a fake host
with no hardware behind them.

`go build -tags nowebui ./cmd/davi-nfc-agent` produces an agent without the
control center: no `/control` routes, no privileged API, no tray entry, and no
frontend in the binary. Roughly 620 KB smaller, and the console's strings are
absent entirely.

```bash
make build-nowebui
make test-nowebui     # the suite under the same tag
```

Everything in `agent/console/` carries `//go:build !nowebui` except
`preferences.go`, which the tray needs in either build, and `doc.go`.
`agent/console/console_nowebui.go` and `agent/tray/console_nowebui.go` supply
the stubs under the opposite tag.

The agent itself needs no tag either way, and does not know the console at all.
`Endpoints` returns `[]serverplugin.Endpoint`, which the program hands to the server
plugin like any other route, so the dependency runs one way: `agent/console`
imports `agent`, never the reverse, and `main` is the only place that wires the
two together. Under `nowebui` that call returns nothing and the rest of the
build is unchanged.

Dropping the console from a custom build is therefore a tag, not a patch, and
deleting `agent/console/` removes it outright.

The agent's protocol is unaffected. Raw tag exchanges and the log ring stay
in either build. Both are reachable without the console, and the transceive
channel is part of the client API rather than a console feature.

## Routes

The console's endpoints, all behind the gate described in
[Who can open it](../../docs/control-center.md#who-can-open-it).
They are not a public API and carry no compatibility guarantee.

| Route | Purpose |
|---|---|
| `GET /control/session?token=` | Exchange a handoff token for a session cookie |
| `POST /control/signout` | End this session |
| `GET /control/state` | Full agent state snapshot |
| `GET /control/logs?since=` | Buffered log entries after a sequence number |
| `POST /control/action` | `{"action": "...", "params": {...}}` |
| `GET /control/ws` | Live state snapshots and log lines |

Actions: `agent.start`, `agent.stop`, `agent.quit`,
`reader.selectDevice`, `reader.setMode`, `reader.setCardTypes`,
`settings.save`, `devices.revoke`, `devices.revokeAll`,
`devices.setRequirePaired`, `clients.disconnect`, `origins.allow`, `origins.revoke`,
`origins.setAllowAny`, `security.rotateAPISecret`, `security.rotatePairingPIN`,
`security.installCA`, `security.regenerateCertificate`, `security.revokeControlSessions`.

The console performs tag operations over the
[client API](../../docs/client-api.md).
