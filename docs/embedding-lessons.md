# Lessons from Turnstile

> **Scratch notes, not part of the doc set.** This file is standalone and
> linked from nowhere — delete it with a single `rm docs/embedding-lessons.md`
> and nothing else in the repo needs touching. It is a backlog and a rationale
> for future maintainers, kept here for convenience rather than published as
> reference documentation.

[Davi Turnstile](https://github.com/dotside-studios/davi) is a lane appliance:
it admits ticket holders from physical Davi NFC cards, keeps admitting from a
signed, encrypted roster while the WAN is down, and replays those admissions
once connectivity returns. It embeds this agent in read-only mode and serves a
staff control panel from the appliance itself.

It is also, so far, the most demanding embedder of this agent — it takes the
whole default build and changes only a handful of things — so it is the best
evidence we have of where the library's seams are, and where an embedder is
forced to copy or rebuild what the library could have offered.

This document records what Turnstile had to do, why, and what the agent could
provide so the next embedder does not. Nothing here is a released API. Where an
item names a change, treat it as a proposal to weigh, not a decision already
taken.

## How Turnstile uses the agent

Turnstile embeds the full default stack — PC/SC readers and phones behind one
`multimanager`, auto-TLS, device pairing, the listener with both `/ws`
protocols, and the tray — and pins exactly four things on top:

- its own `buildinfo.Info`, so its config directory, mDNS name and user agent
  are distinct from the agent's own;
- `nfc.ModeReadOnly`, because a lane never writes a card;
- `ReaderFeedback: true`, so the reader beeps and flashes at what it reads;
- its own control center in place of the agent's console.

Those four changes are the whole of Turnstile's divergence from the shipped
binary. Getting to them costs roughly 120 lines of copied assembly plus several
hundred lines of supporting packages the agent could have owned. The gap
between "four changes" and "a few hundred lines" is what this document is
about.

## The findings, ranked by payoff

### 1. The default assembly is copy-paste, not a constructor

`cmd/davi-nfc-agent/main.go` and Turnstile's `internal/app/app.go` are the same
program: `remotenfc.NewManager` → `multimanager` → `tls.Provision` →
`pairing.New` → `agent.Setup` → the four `paired.Use*`/`Require`/`AllowLoopback`
/`UsePort` calls → `trustplugin` → `serverplugin` with a hand-built `ServeMode`
map → the `/pair` endpoint → `pairingplugin` → `tray.New` → plugins registered
in a specific order (the server plugin first, because it publishes the listener
the rest mount on). An embedder that wants the default build with one field
changed reproduces all of it.

This has already drifted. v1.4.0 added `clientserver.Config.AllowRawTransceive`
(the gated raw-APDU channel). Turnstile's copy of that `ServeMode` map does not
carry it and will not gain it on a version bump — nothing in the type system
says a field went missing from a copied literal. This is the same failure mode
the agent already guards against elsewhere: a value forgotten in a hand-copied
config struct lands on its zero value, silently.

**Proposal.** A `Standard(opts)` constructor (or a small `preset` package) that
performs the assembly above and returns the built pieces as a struct —
something like `{Runtime, Servers, Pairing, Certs, Devices, Tray}` — so an
embedder replaces the one it cares about and keeps the rest. Then rewrite
`cmd/davi-nfc-agent/main.go` to use it, so the shipped binary and every
embedder share one assembly and drift becomes structurally impossible rather
than test-enforced. Turnstile's `newHost` would shrink to roughly fifteen
lines.

### 2. Subscribing to scans safely is rebuilt by every consumer

`event.Signal.Emit` runs handlers synchronously on the emitting goroutine, so a
scan handler must not block. `Signal.Channel(buffer)` exists for a consumer
that wants to drain on its own terms, but it drops on a full buffer **silently**
— no counter, no depth.

Turnstile could not use it. "Zero dropped scans" is a door-drill precondition
and a control-center readout, so a silent drop is exactly the thing it must be
able to see. It wrote its own agent-backed source: a bounded queue fed from
`Events().Tag`, exposing `Dropped() int64` and `QueueDepth() int`, with the
handler doing nothing on the emitting goroutine but a copy and a non-blocking
offer.

**Proposal.** A first-class subscription — `Signal.Subscribe(opts)` returning a
handle with `C()`, `Dropped()`, `Depth()` and `Close()`. Any embedder consuming
taps needs exactly those three numbers, and a silent drop is never the right
default for a scan.

### 3. Card-presence debounce belongs in the agent

A polling reader re-reports a held card for as long as it sits on the field.
`nfc.TagCache.HasChanged` is UID-change detection inside the driver, not a
windowed debounce, so an embedder that acts once per presentation has to add
one. Turnstile wrote a 3-second window keyed by the raw chip UID and **reset on
the tag-removal event** (`NFCData.RemovedUID`), so a deliberate
remove-and-re-present is a fresh tap rather than a suppressed duplicate.

This is presentation semantics, not application policy, and the agent already
emits the removal event the debounce needs — it simply does not offer the
debounce. Every scan-driven consumer has this problem or has it latent.

**Proposal.** An optional debounce on the scan subscription from finding 2,
keyed by UID and cleared by the removal event, off by default.

### 4. There is no headless console-auth flow

The agent's console gates a request on three things: it comes from loopback,
its `Origin` is the agent itself, and it carries a session token **minted by
the tray**. That last requirement is a desktop assumption. An appliance running
as a systemd unit, a LaunchDaemon or a Windows service has no tray click to
mint the first token.

Turnstile wrote its own gate: exchange the agent's API secret for an
`HttpOnly`, `SameSite=Strict` cookie plus a CSRF token, a 30-minute session
TTL, and a per-client rate limit on failed exchanges. The credential it spends
is one the agent already holds (`rt.Agent.APISecret`); the agent just has no
flow that spends it.

**Proposal.** Offer API-secret exchange as an alternative console gate,
selectable in `console.Config`, keeping the tray handoff for the desktop build.

### 5. The console is all-or-nothing, and mounts at the root

`console.Endpoints()` mounts the control API at `/control/` and the SPA at `/`.
An embedder that already serves its own page at `/` — as Turnstile does — cannot
run the console at all, and so loses everything behind it: the log view, tag
inspection, device revocation, certificate reissue and origin management.
Turnstile rebuilt a "Live reader" page over the client protocol to recover a
sliver of that.

**Proposal.** A configurable base path, and a split between `console.Routes()`
(the control API, which already sits behind the clean `host_contract.go`
interface) and the bundled SPA, so an embedder can serve the agent's control
API inside its own shell without adopting the agent's page.

### 6. Origin policy is dynamic for WebSockets but static for HTTP

`server.OriginPolicy` and `CheckOriginPolicy` are the right shape: a policy
object consulted per request. But `server.CORS` hardcodes
`Access-Control-Allow-Origin: *`.

Turnstile's kiosk origin is per-lane — it comes from each loaded service file,
not a launch flag — so it hand-rolled per-route CORS with three deliberate
outcomes: a matching origin gets the state and the CORS headers, the default
origin gets a CORS-bearing 404 the page reads as "no session loaded for this
door", and a mismatched origin gets a 403 with no CORS at all.

**Proposal.** A `CORSPolicy(policy, next)` middleware that echoes the matched
origin with `Vary: Origin`, and a way for a `serverplugin.Endpoint` to declare
its own policy, so a per-route, per-caller origin decision does not have to be
rebuilt outside the library.

### 7. Two Turnstile packages are not Turnstile-specific

- **Service hosting.** Roughly sixty lines that run the process under the host
  OS's service manager: a SIGTERM-cancelled context on Unix, the Windows SCM
  (`svc.Run`) with a stop handler when launched as a service, and a plain
  signal context when launched from a terminal. This is what makes "the agent
  as an unattended background component" real rather than a paragraph in
  `custom-builds.md`.
- **Service packaging.** A systemd unit, a LaunchDaemon plist, an elevated
  PowerShell installer, `SHA256SUMS`, and a build script that refuses to
  cross-compile a cgo target and verifies the embedded web bundle is committed.
  The agent's release workflow stops at archived binaries.

**Proposal.** Move a service-host helper into the agent (guarded by build tags
as the platform files already are), and ship reference service definitions
beside the release artifacts. This also connects to the setup review's finding
that browser trust is terminal-only: an appliance install is exactly the
context that forces a UI path for installing the CA, and the console already
reads CA state — only the action to change it is missing.

### 8. Testing: keep the emulator investment, add an operator budget

`nfctest` is why Turnstile can run a full multi-lane end-to-end test with no
hardware. That investment pays off directly in an embedder and is worth
continuing.

What Turnstile added on top is a **scenario** load test rather than a
throughput benchmark: presentations are deliberately serialized, because one
reader presents one card at a time, and the scenarios are the ones a door
actually meets — a clean online run, an outage falling back to the verified
roster, a hung backend that reaches the admit budget, a deterministically
flapping WAN, and backend revocation while a backlog replays — each reporting
p50/p95. Those figures then feed a physical door drill that names what
automation cannot establish: reader recovery, presentation latency, reader
reassignment and operator burden.

The agent has simulation tests but no operator-facing latency/drop budget and
no hardware drill. A budget-shaped simulation and a drill template in the agent
would give every embedder a release gate to start from.

A smaller note in the same spirit: `nfc.Clock` is a five-method interface, but
a consumer that only needs the current time (as Turnstile's debounce does) is
made to depend on all five and writes its own one-method clock instead. Export
the narrow interface a caller is asked to satisfy, not the wide one the agent
implements.

## The seam that already works — and why it is the template

The build-identity seam is the one an embedder does not fight: `buildinfo.Info`
carried on `Options.Info`, resolving `DefaultConfigDir(dirName)`, with a test
that uses Turnstile as its worked example ("a different `DirName` must give a
different config directory"). Turnstile's own `buildinfo` is about thirty lines
and simply works, because the library asked for exactly what varied and
resolved the rest.

Every proposal above is an argument to give the other seams that same property:
name the one thing an embedder changes, and own everything around it.

## Two functional gaps, distinct from library shape

- **Per-device scan subscription.** The agent supports multiple readers but
  fans every scan to every client. Turnstile routes by the reader that read
  each tap (`NFCData.Device`) to per-lane consumers itself. A device-scoped
  subscription — on `Events().Tag` and in the client protocol — is a natural
  agent feature, and fixed multi-station deployments want the same thing.
- **The cached-NDEF guarantee.** Turnstile reads the card's Davi URL from the
  NDEF message the agent already cached on the scan, and never re-reads the tag,
  because a scan handler must stay off the hardware. Its correctness depends on
  that property. Today it is observed behavior; it should be a documented
  guarantee of what a scan carries.

## One thing that should not move into the agent

Extracting the Davi card identifier from a `/c/{identifier}` URL, with a
fallback to the raw UID, is Davi domain logic, not generic NFC. It should stay
out of this agent. It is worth noting here only because it now exists in more
than one place across the Davi codebase and the copies do not agree on input
validation — which is a problem for Davi to own in one place, not for this
library to absorb.

## Summary

| # | Finding | Shape of the fix |
|---|---|---|
| 1 | Default assembly is copy-paste | A `Standard`/preset constructor the shipped binary also uses |
| 2 | Safe scan subscription is rebuilt | A counted, bounded `Signal.Subscribe` |
| 3 | Presence debounce is rebuilt | An optional debounce on that subscription |
| 4 | No headless console auth | API-secret exchange as a console gate |
| 5 | Console is all-or-nothing at `/` | Base path + API/SPA split |
| 6 | CORS is static | A per-route origin policy middleware |
| 7 | Service hosting and packaging live downstream | Move a service-host helper and reference units into the agent |
| 8 | No operator latency budget or drill | A budget-shaped simulation and a drill template; a narrower `Clock` |

Items 1 through 3 alone would take Turnstile's agent-facing code from a few
hundred lines to under sixty, and would close the drift channel that already
cost it the raw-APDU gate.
