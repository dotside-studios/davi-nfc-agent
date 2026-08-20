# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **`docs/custom-builds.md`: building your own agent.** The package split left
  the agent importable but undocumented, so the way to change what the binary
  does was still to fork it. The new page is the counterpart to the refactor —
  the shipped agent as a `main.go` you could have written, then the variations:
  headless, no control center, no hardware backend, reacting to tags in Go.
  Every Go sample on the page compiles against this tree.

- **`(*Agent).OnTag` observes every scan.** Writing that page turned up a gap:
  a program embedding the agent had no supported way to see a card. The obvious
  move — reading `Agent.Bridge.TagData` — is wrong, because the client server is
  that channel's only consumer, so a second reader takes scans away from the
  browsers instead of copying them. Observers registered before `Start` are
  called on each scan, in the order the clients see them, and the broadcast is
  unaffected either way. Carried by `clientserver.Config.OnTag`, beside the
  existing `OnChange`

- **`agent.DefaultOptions`** returns what the flags default to, for building an
  agent without a command line. The zero `Options` asked for no TLS and port 0;
  `Setup` now also falls back to the default port rather than binding whatever
  the kernel offers

- **The pairing server is a component of the agent.** It was started inside
  `Setup` and stopped only by the command's signal handler, so `Agent.Stop` left
  it bound: stopping the agent from the tray left port 9472 listening, and an
  embedder calling `Stop` leaked a listener. It now starts and stops with the
  agent like anything else registered through `Use`. `agent.Config` loses
  `Bootstrap` and `BootstrapPort`; what the pairing server needs lives on
  `PairingConfig` instead, and `Config.Pairing` being nil disables it.

  One consequence worth knowing: the pairing listener now binds at `Start`
  rather than during `Setup`, so a port already in use is reported when the
  agent starts rather than when it is built

- **The agent has an explicit lifecycle, and things can hook into it.** Its state
  was inferred from whether `Reader` and the servers happened to be nil, which
  two callers could disagree about halfway through a teardown. `Agent.State`
  now reports `stopped`, `starting`, `running` or `stopping`; `OnStateChange`
  registers an observer of settled transitions; and `Use` registers a
  `Component` — anything with a `Start(ctx)`/`Stop()` — that the agent brings up
  once the reader and servers are ready and takes down first, in reverse order.
  A component that fails to start aborts the start and unwinds what came before
  it, so a failed `Start` leaves the agent stopped rather than half up.

  `Use` refuses registration while the agent is running rather than accepting it
  and never starting it. That is the trap the console's attach mechanism falls
  into today: attach a console after `Start` and the agent reports having one
  while the listener has never heard of it

### Changed

- **The agent no longer reaches into its manager for a device driver.**
  `findDeviceDriver` searched the manager for a `*remotenfc.Manager`, pulling a
  concrete driver back out of the abstraction the agent had just been handed,
  and the tag router took that driver by its own type. So both packages named a
  device protocol they had no business knowing.

  A driver now reaches them as three values the caller supplies:
  `server.DeviceOps` to route operations, a channel of scans, and a function
  that builds the device handler. `agent` and `server/tagrouter` import
  `nfc/remotenfc` nowhere. Supply none and the agent serves its own reader.

  The interface is satisfied by shape rather than by import, so the driver does
  not name its consumers either: a second kind of remote device implements the
  same four methods and needs no change anywhere else.

  What the agent decides about device connections travels with it, in
  `agent.DeviceEndpointOptions`: the credential check, the origin check, the
  read-only gate and the key pin. The gate is now read per operation rather than
  captured when the endpoint is built, so a mode change while running takes
  effect

- **The bridge is gone; a client request is a call.** `ServerBridge` was six
  channels and a response channel per request, connecting exactly two objects:
  every channel had one receiver. It was a hand-rolled RPC between things that
  could call each other, and its real cost was not the 430 lines. Because the
  two ends talked over channels, each needed goroutines to drain them, so each
  had a lifetime, so something had to remember to start it. Forgetting was
  silent: clients connected, counts read correctly, and every scan was
  discarded.

  `server.TagOps` replaces the request half. `clientserver` calls it and
  `tagrouter` implements it, so a write is `Write(ctx, op)` returning a result
  and an error. Scans travel the other way by `clientserver.Broadcast`, called
  by whatever produced them. Neither package has a goroutine left, neither has
  a `Start` or a `Stop`, and there is nothing left to forget.

  `protocol.CodedError` carries a wire code on an ordinary error, which is what
  let an operation return its refusal rather than report it beside a value.

### Fixed

- **A write to a phone reports what it did.** The device route returned a bare
  map, and the client server only reads a write outcome when it is an
  `*nfc.WriteResult`, so the assertion failed and the response carried nothing
  but "Write operation completed successfully" -- no `uid`, no `locked`, none of
  the fields [api.md](docs/api.md) documents. Both routes now return a result:
  the device route fills in what the agent knows and leaves `verified` false,
  since the device confirms the write but does not read it back

- **Routes are mounted on the server rather than configured into it.**
  `unifiedserver` took the control API and the console as `Config` fields, read
  once when the mux was built, so a console attached afterwards was never
  served: the agent reported having one while the listener had never heard of
  it. It now takes `Mount(pattern, handler)`, refuses a mount once started
  rather than accepting one nothing will reach, and stops importing
  `clientserver` entirely. It is a listener, a mux and an mDNS advertisement.

- **The agent no longer knows what a console is.** `agent.Console`,
  `SetConsole` and the accessors are gone. A program mounts the control
  center's routes on the server it already holds, and a build that wants none
  mounts none. `NotifyChange` becomes `OnClientsChange`, beside the existing
  `OnTag`. `Runtime.Server` carries the listener so a caller can mount before
  anything starts.

- **CORS is applied per route, at the mount.** It used to wrap everything the
  unified server built except two routes singled out in its own code. The
  routes are not alike: the client endpoint and the health checks are called
  cross-origin by web apps, while the control API administers the agent and the
  console is a page. `server.CORS` wraps the first kind at the point of
  mounting, which makes the asymmetry visible instead of a comment.

- **The device protocol lives with the driver that speaks it.** `protocol` held
  both wire formats, so the browser-facing client API and the phone driver drew
  from one package and neither could be read without the other. The device half
  moves into `nfc/remotenfc`: its message types, the version negotiation, the
  UID and NDEF conversion, and the twelve device WebSocket message types.
  `protocol` keeps what both actually share, dropping from 657 to 267 lines.

  Moving it wholesale was not possible and would have been wrong: `clientserver`
  uses only the envelope and the error taxonomy, with no device symbol at all,
  so it would have imported the phone driver to serve a browser.

  `remotenfc` already aliased several of these types back into itself, with a
  comment saying the duplication "bought a hand-written translation step and
  nothing else". It had: two definitions of `DeviceRegistrationRequest` differed
  by a `protocolVersion` field. The aliases are gone and the superset is the one
  definition

- **The device endpoint requires an authenticator.** `Manager.Handler` took an
  origin check and nothing else, so mounting it without wrapping it served an
  open device endpoint: the upgrade succeeded, the device registered, and
  nothing said otherwise. `ServerOptions.Authenticate` is now required, and a
  handler built without it refuses every connection with 503 unless
  `AllowUnauthenticated` says that is deliberate. The driver still knows nothing
  about API secrets or pairing; it asks for a check and `server.DeviceAuth.Check`
  is the agent's

- **`deviceserver` is gone, split into the two things it was.** It had not been
  a server for a while: no listener, no port, and one HTTP method that was a
  credential check in front of somebody else's handler. What remained was
  authentication and the routing that answers "reader or device?" for each
  client request, held together only because it was the one place that could
  see both.

  The credential check is `server.DeviceAuth`, an `http.Handler` wrapper beside
  the `CheckAuth` and `CheckPairedDevice` it calls. The routing is
  `server/tagrouter`, which serves no HTTP at all. `unifiedserver` now takes a
  plain `http.Handler` for the device endpoint instead of a device server, and
  no longer imports either of them, so what mounts the endpoint decides what
  stands in front of it.

  A build that wires these up itself does what the agent does:
  `auth.Wrap(remote.Handler(opts))` for the endpoint, `tagrouter.New` for the
  routing, and `server.PumpTagData` to join the driver to the bridge. Nothing
  is started implicitly by something else

- **The agent drains its own tag sources.** The reader pump lived in
  `deviceserver`, which had nothing to do with serving it: the package was once
  "the tag-facing side of the bridge", so the hardware reader went there, and
  the name stayed after phones arrived and took the same word. The agent now
  starts the reader and forwards its scans, and `server.PumpTagData` joins any
  driver's channel to the bridge, so a consumer wiring `remotenfc` up directly
  writes one call rather than its own loop. `deviceserver` keeps authentication
  and the choice of tag source

### Removed

- **Dead code in `deviceserver`.** `Handle` had no callers, so the message
  registry it fed was permanently empty; the `devices` map was written and
  deleted but never read; and the fallback WebSocket loop behind them ran only
  when no device driver was configured, where it accepted a device, logged "no
  handler for message type" at its registration, and left it waiting for a
  reply that could not come. That connection is now refused with 503, which is
  what a device can act on

- **`Agent.Shutdown` is the way out; `Stop` is the way to pause.** Merged from
  master, where `Stop` no longer closes the NFC manager: the manager is built
  once for the process, and both the tray's stop button and its device switch
  stop the agent expecting to start it again against the same one. Closing it
  on the way down left that restart holding a manager already shut. `Shutdown`
  stops the agent and then closes the manager, and is what the tray's exit path
  calls. An embedder that runs the agent for the life of the process wants
  `Shutdown`; one that starts and stops it wants `Stop`

- **The binary moved to `cmd/davi-nfc-agent`.** With the agent, console and
  tray in packages of their own, the module root held one file whose only job
  was to join them — so it becomes a `cmd`, and the module root now holds no Go
  files at all. `go install` and `go build ./cmd/davi-nfc-agent` both produce a
  binary named for the directory, exactly as before; the Makefile, both build
  scripts and the cross-compile job follow the path

- **The root package is now just the wiring.** `package main` held the agent,
  the CLI, the tray and the console adapter in one heap of 19 files, so
  everything reached everything: the tray read the agent's private fields, and
  the console adapter found the tray by type-asserting an `any`. None of it
  could be imported, and none of it could be built without a system tray.
  The agent now lives in `agent`, the control center in `agent/console` and the
  tray in `agent/tray`, leaving `main.go` as the composition root — the one
  place that picks an NFC backend and joins the three. Behaviour, flags and the
  on-disk config are unchanged, with one visible exception: the two
  `[multi] Manager registered` lines now precede the startup banner, because the
  command builds the backend before handing it to the agent.

  The dependencies run one way. `agent` knows the console only as an interface
  of two handlers and a redraw signal, so `agent/console` imports `agent` and
  never the reverse; the console reaches the tray through a `Tray` interface it
  declares itself, which replaces the `consoleHost any` type assertion. Nothing
  below `main` imports both. The upshot is that `agent` pulls in neither
  `fyne.io/systray` nor a PC/SC backend, so it can be embedded headless — and
  choosing the backend stays in `main`, where moving PC/SC into `nfc/pcsc` put
  it.

  `SystrayApp` is `tray.App`, `applySettings` is now the `(*Agent).ApplySettings`
  method, `getLocalIPs` is `agent.LocalIPs`, and the five tray actions the
  console drives are exported. `DEFAULT_DEVICE_PORT` and
  `DEFAULT_BOOTSTRAP_PORT` become `agent.DefaultDevicePort` and
  `agent.DefaultBootstrapPort` now that they are part of a package's API

- **`tray.New` takes the `agent.Runtime`** rather than four fields unpacked
  from it at the call site

- **A custom build can carry its own identity.** `buildinfo` was a set of
  package-level variables stamped by this repository's release ldflags, which
  meant a program built on the agent announced itself as `davi-nfc-agent` and,
  worse, wrote its certificates and paired devices into this agent's
  configuration directory — two builds on one machine quietly sharing both.
  `buildinfo.Info` makes that identity a value: set `Options.Info` (or
  `Config.Info`) and it follows through the configuration directory, the log
  banner, the control center header, the tray tooltip, the pairing pages and the
  iOS profile, and the mDNS service devices look for. Blank fields fall back to
  the agent's own, so overriding `DirName` alone is enough to stop two builds
  colliding on disk. The shipped binary sets none of it and is unchanged; a test
  pins `Info.String()` to the old `BuildInfo()` output, and another pins the
  mDNS name an unconfigured agent advertises

- **The agent's configuration is settled at construction.** `Agent` carried 22
  exported fields, twelve of which `Setup` assigned one at a time after
  `NewAgent`, so the object existed in a half-built state and anything holding
  it afterwards could rebind the port, swap the origin allowlist or withdraw the
  pairing requirement behind the running servers. `agent.New(agent.Config{…})`
  now takes them together and copies them in; the fields behind it are read
  through methods — `Origins()`, `Devices()`, `DevicePort()` and the rest — and
  cannot be reassigned. What may legitimately change while running keeps a
  method: `SetRequirePairedDevice`, `SetAllowCardType`, and the new `SetConsole`
  for the one write a command still makes. `Reader`, `Bridge` and the three
  servers stay exported, being state that comes and goes rather than
  configuration. `NewAgent` is replaced by `New`

- **`agent.Runtime` stops duplicating the agent.** `Origins`, `Devices`,
  `Bootstrap` and `BootstrapPort` were assigned twice by `Setup` — once on the
  agent, once beside it — so half the struct was aliases of one object that
  could drift apart. It now carries only what the agent does not: the settings
  store, the log ring and the reader to open at startup. Everything else is
  read through `rt.Agent`

### Fixed

- **A phone's device reports the tag it is holding.** `nfc.Device.GetTags` is
  the question "what tag do you have", and `remotenfc.Device` satisfied the
  interface without answering it: the method waited and returned nothing, on
  the grounds that scans arrive by push and a phone is never opened as the
  agent's reader. So the driver grew a second place to keep held tags, a map on
  the manager keyed by device ID, and every path that needed one had to know to
  look there instead. That is the origin of the reader-and-device split in the
  request routing.

  The tag now lives on the device holding it, which is what `GetTags` returns.
  The manager keeps only which device scanned most recently, for a request that
  names no device. The wait stays for the empty case, since that is what paces a
  caller polling a device with nothing in its field

- **A port already in use fails the start.** `unifiedserver.Start` blocked for
  the server's lifetime, so the agent ran it on a goroutine and dropped its
  error. Occupying the port left `Start` returning nil, the state reading
  running, and nothing listening. It now binds before returning and serves
  afterwards, so the failure reaches the caller.

- **The card-type filter no longer races.** It was a bare `map[string]bool`
  handed to the device server at construction and mutated in place afterwards
  by the console and the tray, which is why `ApplySettings` cleared it key by
  key rather than replacing it. The goroutine filtering scans read that map
  while those writes landed. Go aborts the process on a concurrent map write
  rather than merely returning the wrong answer, so this was a crash waiting on
  timing, not a stale read. The filter is now a type that guards itself and is
  asked rather than shared

- **A write can no longer land on a tag the client did not mean.** Requests were
  routed by preference rather than by name: the agent's own reader while it
  reported a card, otherwise whichever device had scanned most recently. That
  preference was evaluated when the request arrived, not when the tag was
  scanned, so lifting a card in between moved the request to a phone's tag. A
  payload encoded for one tag was written to another, irreversibly when the
  request also locked. Nothing caught it, because the request had no field
  naming the tag it meant: `uid` was reported in the *response*, so a client
  learned which tag it had hit only afterwards.

  `writeRequest`, `lockRequest`, `transceiveRequest` and `capabilitiesRequest`
  now carry the `uid` of the tag they apply to, and the agent finds whichever
  source is holding it instead of choosing one. A tag that is not present fails
  with `NO_CARD`; a tag present but not the one named fails with the new
  `TAG_MISMATCH`, which is not retryable. Naming a `deviceID` as well holds that
  device to the UID too, so an id remembered from an earlier scan cannot act on
  whatever that device is holding now.

  The check runs at both ends of the route. On the reader it happens inside the
  tag operation, against the tag physically present, so it cannot go stale
  between the check and the write: `WriteOptions.ExpectUID`, and the new
  `LockCardExpecting`, `TransceiveExpecting` and `GetCapabilitiesExpecting`
  beside the existing calls, which keep their behaviour when given no UID.

  A request naming neither a tag nor a device is refused with `TAG_NOT_NAMED`
  rather than guessed at. A client that cannot name its tag sets
  `allowUntargeted: true` on the request to get the old behaviour back. It is a
  request field rather than an agent setting so that one such client carries the
  risk itself instead of the operator weakening every client on the agent.

  The bundled JavaScript client fills `uid` in from the last tag it saw, so
  `client.write({ records })` is targeted without any application change. Its
  `tagData` now also exposes `deviceID`, which the payload always carried and
  the client dropped

- **The agent starts without auto-TLS again.** `-auto-tls=false` and an
  externally provisioned `-cert`/`-key` both panicked at startup, before the
  systray appeared. Neither configuration has a certificate authority, and the
  bootstrap server is built to run without one so phones can still pair. Its
  nil checks did not work: the caller passed a nil `*tls.Manager`, and a nil
  pointer boxed into an interface is not a nil interface, so every check passed
  and then dereferenced nil. `NewBootstrapServer` now unboxes it once, so those
  checks hold

- **`Start` and `Stop` no longer race.** The tray, the console and the network
  watcher all reach them from different goroutines, and only `RestartServers`
  took a lock — `Start` and `Stop` mutated `Reader`, `Bridge` and the three
  server fields with no synchronisation at all. Four concurrent start/stop pairs
  under `-race` reported thirteen races. All lifecycle transitions now serialise
  on one mutex, and the whole repository is race-clean

- **A quick stop could crash the agent.** `startServers` launched a goroutine
  that read the `UnifiedServer` field rather than a captured value, so a `Stop`
  landing before the goroutine was scheduled left it calling `Start` on a nil
  server. The unified server had the same shape internally: no mutex at all,
  with `Start` and `Stop` both touching `httpServer`, `ctx` and `mdnsServer`
  while overlapping by design — `Start` blocks until `Stop` cancels it. Both are
  now guarded, and a `Stop` arriving before the listener binds prevents the bind
  instead of racing it, rather than leaving an mDNS advertisement pointing at a
  listener that never came up

- **`-require-paired-devices` can no longer be withdrawn by a stored setting.**
  The flag set the requirement, and then `applySettings` -- the last thing
  startup did -- pushed the persisted value straight back over it. An operator
  who launched with the flag while `requirePairedDevice` was false in
  `settings.json` got an agent that logged *Paired devices required* and then
  admitted unpaired devices anyway, which is the one direction of this setting
  that costs security rather than convenience. The console had the same hole
  from the other side: saving any preference re-applied the stored settings, so
  a toggle unrelated to pairing could withdraw the requirement mid-run.

  A requirement asked for on the command line or in the environment is now
  locked for that run: `Config.RequirePairedDeviceLocked` records where it came
  from, and `SetRequirePairedDevice` refuses to lower it, saying so in the log
  rather than silently. A requirement that came only from settings stays the
  operator's to toggle, which is what the console's switch is for. Either source
  can still turn it on

- **Package `agent` no longer writes to process-wide state.** Moving the CLI
  into the agent took `flag.BoolVar`, `flag.Parse` and `log.SetOutput` with it,
  all of which belong to a program rather than a library: registering flags
  writes to `flag.CommandLine`, so an embedder with its own flags got a
  collision, and `Setup` silently redirected the standard logger out from under
  whatever logging its caller had arranged. The twelve flags now live in
  `cmd/davi-nfc-agent/flags.go`, which fills the same `Options` the library
  already exposed, and the command installs the log ring itself before calling
  `Setup` — which is what keeps the startup sequence in the ring the console
  reads. `Options.Logs` carries it in; `Options.DevicePortSet` carries in the
  one piece of flag state `Setup` needs, and now lets an embedder assigning
  `DevicePort` outrank a port persisted in settings, which it previously could
  not

- **A hand-built `nfc.Card` reports an error instead of faulting.** `Card` is
  exported and its fields are settable, but `Read` assumed the unexported tag
  every card built by `NewCard` has, so one composed field-by-field panicked
  inside `io.ReadAll` — on the client server's broadcast goroutine, if such a
  card ever reached it. Nothing in the agent constructs one that way, so this
  was unreachable in the shipped binary; it stops being unreachable once
  callers write against `nfc` directly


## [1.1.3] - 2026-08-15

### Fixed

- **A phone is no longer offered as the agent's own reader.** Every device the
  managers knew was listed as a reader to pick, phones included, so the reader
  menu offered `smartphone:85bacf02-…` beside an ACR122 and picking one pinned
  it in settings; auto-selection could reach the same result on its own, taking
  whatever `ListDevices` returned first. But a phone is never opened and polled
  here — it reports what it scans over the device bridge, which is why its tags
  arrive whether or not the reader has it selected. So the pin named a device
  that could not be connected and would not become connectable, and the agent
  spent every poll trying, forever, with a line each time, while the console
  said "No readers detected" beside the reader it claimed was pinned — both
  true and neither explaining the other. A manager now reports whether its
  devices are remote and `MultiManager` can list readers alone; the reader menu
  and both auto-selections ask for readers, while the device list keeps
  everything, since that is what the pairing views are built from. A phone
  already pinned is ignored at startup with a line saying why, and selecting one
  is refused rather than accepted and quietly dropped
- **A reader that will not open is reported once, not every poll.** `doPoll`
  runs continuously and logged each failed connection attempt, so an unplugged
  reader — or a device path naming one that will never appear — filled the log
  at the polling rate with one repeated line. It is now reported once for as
  long as the reason holds, and again once the reader has worked in between,
  which is the rule `HandleError` was given in 1.1.2. The same counter serves
  both, so a fault reported by one is not repeated by the other
- **The console shows what an NDEF record says.** The record tables read
  `r.text` and `r.uri`. The agent sends neither: it decodes text and URI records
  alike into one `content` field, named that because a record carries one value
  and the type beside it already says which kind. So the value column was empty
  for every record of every type, and a tag holding a URL showed the type `uri`,
  a dash, and the base64 of the bytes — everything except the URL
- **The protocol reference described NDEF records that the agent never sent.**
  The read direction in [api.md](docs/api.md) documented each record as carrying
  `type: "T"`, a `text` field and a `uri` field, with `payload` as an array of
  numbers. The agent sends a human-readable `type`, one `content` field, and a
  base64 `payload` — so a client written against that page saw an empty value
  for every record, which is the bug the console had just been fixed for. The
  section now describes what is actually on the wire, and says that the read and
  write directions share these names

## [1.1.2] - 2026-08-15

### Added

- **A tag broadcast names the reader that read it.** `tagData` described the tag
  and not its source, so a client holding one had no way to tell whether a
  `deviceStatus` applied to it. The payload now carries `deviceID` when the tag
  came from a paired device, taken from the source the tag already records.
  Absent means the agent's own hardware, which is what every tag was before
  devices existed. See [Tag Data](docs/api.md#tag-data)

### Changed

- The tag inspector names the reader a tag came from, since with a phone paired
  that is no longer a question with one answer

### Fixed

- **A smartphone disconnecting no longer floods the log.** `GetTags` reported a
  closed device as `fmt.Errorf("device is closed")` — its own phrase, matching
  no sentinel. `IsDeviceClosedError` looks for the typed `ErrDeviceClosed` or,
  failing that, the string `device closed`, so it declined every one of them:
  two spellings that read identically to a person and not at all to
  `strings.Contains`. Nothing downstream handled a phone disconnecting as a
  result — the reader still had a device, so it polled straight back into the
  closed one, a loop with no delay and a log line every turn, and a reconnection
  never once attempted. The three returns are now `nfc.ErrDeviceClosed`, which
  survives the `%w` wrapping `getTags` adds; the string fallback accepts both
  spellings, since it exists for text this package does not control
- **A poll that hit an unrecognized error is paced.** `handleDeviceErrors`
  treats "the device manager still has a device" as a sign the device recovered,
  and polls again immediately. That holds for the branches that close and reopen
  one, but an error no branch recognized left the device exactly as it was, so
  the test passed trivially and the loop spun at full speed. The delay for an
  unhandled error existed already, one branch further down, unreachable in the
  case that needed it most. A poll that recognizes nothing now waits the same
  interval, so an error past the classifiers costs a line a second rather than a
  line a millisecond
- **A standing device fault is reported once, not on every poll.** `HandleError`
  is reached from the poll loop, so an error that persisted was logged as often
  as the device was polled — and the reason a line is worth reading is that the
  condition changed. It now reports a fault the first time, again when the
  reason changes, and again when one returns after the device has worked,
  matching what `ListDevices` was given in 1.1.1 against the same log buffer
- **The control center no longer discards tags scanned by a phone.** A tag
  tapped on a paired phone reached the frontend and never appeared in the
  console, which reads the same broadcast on the same endpoint. The console
  cleared its tag whenever a `deviceStatus` reported no card — and that status
  describes the agent's own reader, whose `cardPresent` is false for the entire
  life of every phone scan. The tag was received, displayed, and wiped by the
  next status message. No other consumer reads tag presence out of reader
  status, which is why only this one lost them. Reader status now clears only a
  tag the reader itself produced; a tag from a device is cleared by the device
  saying so
- **A tag leaving a phone's field is recorded as a removal.** A device reports it
  as a `tagData` with no UID, which the console treated as a scan — rendering a
  blank tag over the real one and logging a scan of nothing

## [1.1.1] - 2026-08-15

### Added

- **Trust this agent in browsers, without a terminal.** A browser cannot open a
  `wss://` connection to an untrusted certificate, and unlike a page visit there
  is no warning to click through — the page simply never connects, and nothing
  on either side says why. The only remedy was `-install-ca`, a launch flag, so
  a non-technical operator could not get a web page talking to the reader at
  all. The tray gains **Trust This Agent in Browsers** and the Control Center
  gains the same action under *Device trust*, both stating the tradeoff before
  they run: a CA in a trust store can sign for any name, so a certificate for a
  name you control is still preferable. The listeners restart afterwards, so the
  reissued certificate is the one served. The entry hides itself once there is a
  CA, and the flag stays for scripted provisioning

### Fixed

- **The public key pin now describes the key the agent actually serves.** The
  pin is computed from `agent.key`, the long-lived key the self-signed route
  signs, and that is what `/pair` hands a device. The CA route did not use it:
  truststore's `MakeCert` generates a key of its own, which was then installed
  as the server key. So an agent with a CA advertised a pin its handshake could
  never present, and a device that paired with one was locked out permanently —
  re-pairing returned the same unusable pin. The CA route now signs the same
  persistent key, so the pin holds across adopting a CA and devices paired
  before **Trust This Agent in Browsers** keep working after it. An install
  already serving the wrong key reissues on the next start
- **Reissuing a certificate no longer installs a certificate authority.**
  `RegenerateCertificates` called the CA routine directly instead of going
  through the routing that picks self-signed or CA, so the Control Center's
  **regenerate** action — labelled as nothing more than reissuing a certificate
  — created a local CA, installed it in the system trust store and prompted for
  a password, on an agent that had deliberately never had one. That defeated
  1.1.0's own change to stop installing a CA by default. Reissuing now keeps
  whichever route the install already uses, and putting a CA in the trust store
  happens only when explicitly asked for
- **A missing reader no longer floods the log.** `ListDevices` is polled
  continuously by the tray, the console and the device watcher, and every failed
  poll logged. With no reader attached that was 85 of 108 lines — one repeating
  message, drowning anything else that happened, including the certificate
  errors the log was added to surface. A failure is now reported once, again if
  the reason changes, and a recovery gets a line of its own. Same conditions
  measured after: 1 line in 21

## [1.1.0] - 2026-08-15

### Added

- **Control Center.** A web console served by the agent itself at its own root,
  covering what neither the tray nor the flags could do. Four tabs: Overview,
  Tag, Activity and Security. The reader controls — start/stop, device,
  mode, card filter, port — sit on Overview rather than behind a settings tab,
  so the page an operator lands on is the page they work from. Opened from the
  tray's
  **Open Control Center**, which mints a single-use token and hands it to the
  browser. Being same-origin, it is also the one browser client that works on a
  fresh install without `-install-ca` — a page visit can be accepted manually,
  where a bare `wss://` to an untrusted certificate fails outright. Built with
  Vite and React; `webui/frontend/dist` is committed and embedded, so
  `go build .` still needs nothing but Go.
  See [Control Center](docs/control-center.md)
- **The agent's log is readable.** Log output went to stderr and nowhere else,
  so an agent started from a desktop launcher discarded every certificate
  warning, refused origin and reader failure as it produced it. The last 5000
  lines are now kept in memory, streamed live to the console, filterable by
  level and text, and downloadable for a bug report. Nothing is written to disk
- **NDEF composer and tag inspector.** Writing a tag by hand previously meant
  being a client application: the agent's support for smart posters, vCards,
  MIME, geo, Android Application Records and fully raw records was unreachable
  without writing code against the WebSocket protocol. The console composes any
  of them, sizes the message against the tag's reported capacity as it is
  typed, and can erase or permanently lock. It writes over the ordinary client
  endpoint, so there remains one implementation of the write path
- **Live event feed.** Scans, writes, locks and errors as they happen,
  filterable, pausable and exportable as NDJSON. The tray only ever showed the
  card currently on the reader, so a tag presented and taken away left no trace
- **The control center is a removable module.** Everything it needs lives in
  `webui/` — the gate, the routes, the state snapshot, the dispatcher and the
  frontend it embeds — and none of it imports the agent. It declares what it
  needs of the agent as `webui.Host`, implemented by a single adapter in
  `package main`, which both keeps the console's whole reach into the agent
  readable in one file and lets its tests run against a fake host with no
  hardware. `go build -tags nowebui .` then drops it: no `/control` routes, no
  privileged API, no tray entry and no embedded console — about 820 KB smaller,
  with none of the console's strings present. Only three files in `package main`
  carry the constraint; the call sites tolerate a nil console, so no shared file
  needs a tag of its own. The agent's own protocol is unaffected: raw tag
  exchanges, settings persistence and the log ring remain in both builds, each
  being reachable without the console
- **Raw exchanges with a tag, from a client and from the console.** The agent
  could already transceive with a tag, but only agent-to-device: no client
  could ask for one, so DESFire, ISO-DEP applets and capability probing meant
  writing a program. `transceiveRequest`/`transceiveResponse` open that channel
  to clients, routed like a write — to the device holding a tag, otherwise to
  the hardware reader. The console gains an APDU panel with hex in, hex and
  ASCII out, decoded ISO 7816 status words, per-exchange timing, and presets
  built from the commands `nfc/apdu.go` already constructs.
  **Refused in read-only mode**: a raw command can write to a configuration
  page, burn OTP bits or lock a tag permanently, and the agent can neither tell
  that apart from a `SELECT` nor undo it
- **Connected clients are visible, and can be disconnected.** The client server
  tracked its connections but exposed only a count, so "something is writing to
  my tags" had no answer. Each connection now reports its origin, address, user
  agent, how long it has been connected, and how many writes and locks it has
  issued — counted per connection, so a client that is only listening is
  distinguishable from one changing tags. Any one of them can be disconnected;
  it is free to reconnect, so revoking its origin remains what bars it
- **Per-device revocation.** Paired devices are listed with platform, pairing
  time, last seen and whether they are connected, and each can be revoked on
  its own. The tray's only option was to revoke every device, which made
  removing one lost phone cost every other phone its pairing
- **Settings persist.** Reader mode, card-type filter, reader selection, port
  and the paired-device requirement are written to `settings.json` in the
  config directory. Mode and filter previously lived only in the tray's menu
  state and were lost on every launch; device and port could only be set with a
  flag, and so had to be repeated in whatever started the agent. An explicit
  flag still wins, and no file is written until something is deliberately saved
- **Certificate and origin diagnostics.** The console reports certificate
  expiry, issuer and the names it actually covers, and warns when the agent is
  reachable on an address the certificate omits — previously indistinguishable
  from the agent being down, since a browser reports both as a failed
  connection. Origins refused since startup accumulate with a one-click
  **allow**, where the tray offered a blocked origin only while its menu was open
- **Device protocol versioning.** Devices offer the `davi-nfc-device.v1`
  WebSocket subprotocol and send a `hello` first frame carrying the protocol
  version alongside registration, so setup costs one round trip. The response
  reports the version both sides will speak, clamped to what the agent
  implements rather than refused when a device asks for something newer.
  Devices that offer nothing still send `registerDevice` and get a
  byte-identical exchange back
- **Full capability declaration on the wire.** Devices can declare APDU
  transceive, raw framing, lock support, device type, supported tag families
  and baud rate; tag scans can carry the capabilities the device determined for
  that specific tag. All additions are `omitempty`, so a device declaring
  nothing extra sends exactly the previous object
- **Classified errors.** Error payloads carry `retryable`, plus the failing
  operation and tag. Combined with `code` this distinguishes "retry with
  backoff" from "ask the user to present the tag again" from "stop". Covers
  both the device and client endpoints; existing code strings are unchanged
- **`goodbye` frame.** A device can announce a deliberate departure, which the
  agent acknowledges with a close handshake. Silent disconnects are classified
  from the WebSocket close code, so a clean shutdown is no longer
  indistinguishable from a dead radio
- **Writing to tags held by remote devices.** A write is routed to the device
  reporting the most recent scan whenever no hardware reader has a card
  present, correlated by request ID and bounded by a 20s timeout. A device that
  disconnects mid-write releases its waiters immediately rather than making
  each wait out the timeout. Carries an idempotency key so a device can
  recognize a write it already applied after a lost response
- **Transceive command channel.** `deviceTransceiveRequest`/`Response` let the
  agent exchange raw data with a tag a device is holding, with separate
  declarations for APDU-level and framing-level exchange. Costs one network
  round trip per command, so it is for what genuinely needs it — DESFire,
  ISO-DEP applets, capability probing — not as a general read path
- **Origin allowlist with first-party defaults**, persisted to
  `allowed-origins.json` and managed from the tray. A refused origin is offered
  as a one-click *"Allow …"*, which persists without a restart. Preload with
  `-allowed-origins` or `DAVI_NFC_ALLOWED_ORIGINS`
- **Public key pinning.** The agent reports `serverInfo.publicKeyPin` and logs
  it at startup. Devices record it when pairing and compare it on later
  connections, recognizing the agent without any certificate authority. It
  survives certificate reissues, which happen whenever the host's addresses
  change
- **Per-device pairing.** `POST /pair` on the bootstrap server exchanges the
  kiosk PIN for a credential belonging to one device, returned alongside the
  agent's key pin. Tokens are stored hashed and shown once. The tray lists
  paired devices and revokes them individually, or all at once
- **`-require-paired-devices`** (also `DAVI_NFC_REQUIRE_PAIRED_DEVICES`, and a
  tray toggle that applies immediately): admits only devices holding a paired
  credential, withdrawing both the shared secret and the loopback bypass for
  device connections. Browser consoles are unaffected

### Changed

- **Self-signed certificates by default; the local CA is now opt-in.** The
  agent previously created a certificate authority and installed it into the
  system trust store on startup. It now serves a self-signed certificate from a
  key it generates once and keeps, touching no trust store. Use `-install-ca`
  for browsers that cannot pin and have no externally provisioned certificate,
  or point `-cert`/`-key` at one you provide. **An install that already has a
  CA keeps using it**, so a browser console working today is unaffected
- Tags held by remote devices now report `canWrite`, `canLock` and
  `canTransceive` honestly, based on what the device declared *and* whether it
  is still connected, instead of always reporting read-only
- **Single-port architecture.** The device server (previously port 9470) and
  client server (previously port 9471) are now served from one listener on a
  single port (default 9470). NFC devices connect to `/ws?mode=device` (or with
  the `X-Device-Mode: true` header); web clients connect to plain `/ws`. Both
  `/health` and `/api/v1/health` are served on the one port and report
  `"type":"agent"`. A new `server/unifiedserver` package fronts the existing
  device/client handlers and routes each connection; the in-process bridge
  between them is unchanged.

### Removed

- The `-client-port` flag. The client endpoint now shares the agent port; set
  the single port via `-device-port` (default 9470).

### Fixed

- **A hosted web console could not connect at all.** The WebSocket origin guard
  rejects any `Origin` that is not the agent's own `host:port`, and
  `AllowedOrigins` — the escape hatch built alongside it — was never populated:
  no flag, no environment variable, and `agent.go` left it nil on both servers.
  Every browser console was affected, since a page is by construction served
  from somewhere other than the agent's port. The REST endpoints meanwhile
  answered `Access-Control-Allow-Origin: *`, so the two halves of the same API
  disagreed about who may call them
- Pairing required auto-TLS. The endpoint lived on a server that only ran when
  the agent managed its own certificates, so the deployment using an externally
  provisioned certificate — the one that most needs per-device credentials —
  had no way to pair
- `ServerBridge.Close` closed channels that producers could still be sending
  on, which panics the losing goroutine. Only the done channel is closed now;
  consumers already exit on their own context
- A tag scan was published to clients before the route a write needs was
  registered, so a client reacting to `tagData` with an immediate write could
  be told no device held a tag it had just been told about
- Tag scans that fail to parse — a malformed UID, an undecodable NDEF message —
  were reported as the transient `TAG_SEND_FAILED`, inviting a device to resend
  a payload that can never be accepted. They are now `INVALID_DATA` and marked
  permanent
- A failed write reported `WRITE_FAILED` whatever went wrong, because the
  reader's typed error was flattened to a string at the bridge before the
  client server saw it

- Certificate generation silently ignored a failure to set `CAROOT`, and the
  hosts cache ignored write errors, so a truncated cache could be read back as
  a complete host list on the next start
- Handlers in the TLS bootstrap server logged a certificate or profile as
  "served" even when writing the response body had failed

### Security

- The Control Center's API is gated independently of the client and device
  endpoints. Every request must arrive over loopback, declare the agent's own
  origin, and carry a session cookie minted by the tray; the routes are served
  without the permissive CORS headers the client endpoints carry. The origin
  allowlist is deliberately not consulted — an entry there authorises a console
  to read tags and must never confer the ability to revoke a device or rotate
  the secret. Loopback is determined from `RemoteAddr`, never from a forwarding
  header
- `golang.org/x/text` and `golang.org/x/net` are updated past two
  vulnerabilities reachable from certificate generation: an infinite loop on
  invalid input in `x/text` (GO-2026-5970) and a failure to reject ASCII-only
  Punycode labels in `x/net/idna` (GO-2026-5026)
- Builds move to Go 1.25.13, clearing four standard-library vulnerabilities
  the agent reaches through its own TLS and HTTP servers: unbounded
  post-handshake messages (GO-2026-6090) and an Encrypted Client Hello privacy
  leak (GO-2026-5856) in `crypto/tls`, a missing `ReadHeaderTimeout` on the
  unencrypted HTTP/2 check in `net/http` (GO-2026-6089), and unbounded
  recursion in `encoding/asn1` (GO-2026-5972) reached when parsing the CA
  certificate
- The agent no longer installs a certificate authority into the system trust
  store by default. A CA in a trust store can sign for **any** name, not just
  this agent, so whoever holds its key can intercept that machine's traffic.
  See *Changed* above for the replacement and the compatibility path
- Per-device credentials replace a single shared secret as the recommended way
  to authenticate a device, so removing one device no longer means rotating a
  secret that logs out every other device at the same time

## [1.0.3] - 2026-06-29

### Fixed

- NTAG locking now locks the **entire** user area. `MakeReadOnly` previously set
  only the static lock bytes (pages 3-15), leaving the bulk of an NTAG215/216
  writable while reporting a successful lock; it now also sets the model's
  dynamic lock bytes. Validated end-to-end against in-memory tag emulators
- DESFire read/write now interpret the DESFire native status word (wrapped
  `91 00` = OK) instead of requiring ISO `90 00`. The old generic check would
  have rejected every real DESFire response
- DESFire read/write now follow the additional-frame (`91 AF`) chain, so NDEF
  payloads larger than a single ~59-byte native frame work. Validated against
  the in-memory DESFire emulator; the per-frame size is datasheet-modeled and
  wants a hardware cross-check
- TLS network watcher no longer infinite-loops regenerating certificates when
  the hosts-cache write fails. The watcher now compares against in-memory
  `lastHosts` (updated only after a fully successful regeneration) instead of
  the possibly-stale disk cache, so a partial failure retries cleanly on the
  next tick instead of re-running truststore install + cert generation forever
- TLS `Manager` network-watcher state (`networkChangeChan`, `stopWatchChan`,
  `lastHosts`) is now mutex-guarded, fixing a data race under `-race` and a
  double-`StopWatching` close-of-closed-channel panic
- Network-change watchers now shut down reliably on quiescent sockets. The
  close-to-interrupt trick (which Linux/Darwin don't guarantee will wake a
  thread already blocked in `recvfrom`) is replaced with a short receive
  timeout so the watcher loop observes stop and returns within ~200ms

### Added

- Tag capabilities exposed over the wire: every `tagData` broadcast now carries
  a `capabilities` object (memory, max-NDEF size, `canWrite`, `canLock`,
  `isReadOnly`, `supportsPassword`), and clients can fetch the present tag's
  capabilities on demand via a `capabilitiesRequest`/`capabilitiesResponse`
  message. Backed by `NFCReader.GetCapabilities` and `Card.Capabilities`
- Erase/format support: `NFCReader.EraseCard` and an `empty` write record type
  overwrite a tag with an empty NDEF message (verified like any write, and
  composable with `lock`). Reversible — the tag can be rewritten afterward
- Password-protection capability reporting (`TagCapabilities.SupportsPassword`,
  true for NTAG21x) and the reader API contract (`SetCardPassword`,
  `RemoveCardPassword`, `PasswordOptions`). The destructive NTAG config writes
  (PWD/PACK/AUTH0/ACCESS) are intentionally gated off and return a clear
  not-supported error pending validation on real hardware, since a wrong
  configuration can permanently lock a tag
- Tag locking (make read-only) exposed through the API: write-and-lock in one
  step via `"lock": true` on a write request, or lock an already-written tag
  with a standalone `lockRequest`. Supported on lockable tags (NTAG,
  Ultralight); others return a clear error. New `NFCReader.LockCard` and
  `WriteOptions.Lock`
- Expanded write record types beyond text/uri: `url`, `mailto`/`email`, `tel`,
  `sms`, `geo`, `smartposter` (URI + title), `mime`, `vcard`, `external`, `aar`
  (Android Application Record / app launch), and fully custom `raw` records
  (TNF + type + payload). New `NDEFSmartPoster` and `NDEFRaw` builders
- URI records are now written with the longest matching NFC Forum abbreviation
  prefix (e.g. `https://`, `tel:`, `mailto:`), saving bytes on small tags; the
  decoder understands the full prefix table for tags written by other tools
- Read-after-write verification: writes are now confirmed by reading the data
  back and comparing it to what was written, bringing write reliability to parity
  with the read path
- Automatic write retry with linear backoff on transient failures (configurable
  via `WriteOptions.MaxWriteAttempts`); permanent failures (card removed,
  read-only, capacity exceeded) are never retried
- Pre-flight capacity check that rejects NDEF messages larger than the tag's
  usable capacity before any write is attempted
- Structured write results (`WriteResult` / `WriteMessageWithResult`) surfaced in
  the `writeResponse` payload: `uid`, `tagType`, `bytesWritten`, `verified`, and
  `attempts`
- MIFARE Classic NDEF formatting and custom-key support: blank Classic 1K cards
  can be formatted as NFC Forum tags (MAD written to sector 0, sector trailers
  switched to NFC Forum config, with validation that keeps trailers rewritable
  with Key B to prevent bricking), and cards provisioned with non-default keys
  can be read/written via `NFCReader.SetClassicKeys` / `pcscClassicTag`
  candidate keys (#3)
- PNP phone pairing: a QR-first flow for using a phone as an NFC reader. The
  agent generates a 6-digit PIN (`crypto/rand`) shown in the systray; `/qr.png`
  encodes a PIN-gated `/install` URL, and `/install` user-agent-routes to an
  unsigned iOS `.mobileconfig` or an Android DER `.crt` that the OS installs
  directly. `/ca.pem` remains (PIN-gated) for legacy clients, and five wrong
  PIN attempts lock pairing until restart. New systray "Pair Phone" and
  "Pairing PIN" items. Adds `github.com/skip2/go-qrcode`
- Native OS network-change watcher for certificate regeneration: subscribes to
  the platform address-change source (Linux `AF_NETLINK`, macOS `PF_ROUTE`,
  Windows `NotifyAddrChange`) so a network roam regenerates TLS certs in
  milliseconds instead of waiting on the old 5s poll, now demoted to a 30s
  safety net
- IPv6 support: `GetLANIPs` / `getLocalIPs` now return both IPv4 and IPv6
  globals (IPv4 preferred for `ips[0]` callers), all host:port composition goes
  through `net.JoinHostPort` so IPv6 literals are bracketed, and `::1` is
  accepted as a valid host
- Clipboard fallback on Linux: the systray copy buttons now pick the clipboard
  utility by display server (`wl-copy` on Wayland, then `xclip`/`xsel` on X11)
  instead of assuming `xclip`, with a clear install hint naming the packages
  when none is present. macOS (`pbcopy`) and Windows (`clip`) are unchanged

### Security

- Tier-1 hardening across the WebSocket servers. `CheckOrigin` now rejects
  cross-site WebSocket hijacking (both Upgraders previously returned `true`,
  letting any visited website read live NFC events from localhost); the device
  server (phone-as-reader) and client server now require an API secret
  (loopback-bypassed, constant-time compared, supplied via `?secret=` or
  `Authorization: Bearer`); the secret is auto-generated (32-byte URL-safe
  base64) and persisted under the config dir with mode 0600 + Windows DACL on
  first run; and both the pairing PIN and the API secret are rotatable from the
  systray ("Regenerate Pairing PIN" / "Regenerate API Secret")
- Windows TLS file permissions: the TLS and CA directories, `server.key`, and
  `hosts.txt` now receive an explicit DACL granting only the current user,
  Administrators, and SYSTEM. Unix 0600/0700 mode bits are advisory on Windows,
  so private keys were previously world-readable

### Changed

- A `writeResponse` with `success: true` now guarantees the data was verified on
  the tag; unconfirmed writes return an error instead
- Streamlined the custom Manager/Device/Tag extension surface for library
  consumers: custom tag implementations can embed `BaseTag` to inherit sensible
  defaults instead of implementing the full interface by hand

## [1.0.2] - 2026-01-19

### Fixed

- Critical segmentation fault (SIGSEGV) caused by race condition in PC/SC context management where context could be released while another goroutine was using it

## [1.0.1] - 2026-01-18

### Fixed

- Concurrent WebSocket write panic caused by multiple goroutines writing to the same connection
- Excessive "no NFC devices found" log spam when no device is connected

### Changed

- Device discovery moved to agent level for cleaner separation of concerns
- Agent now starts without a device and waits for device connection
- Hot plug-n-play support: devices are auto-discovered when plugged in and paths are cleared on disconnect
- Systray now reads device state from agent (agent is source of truth)
- "Refresh Devices" menu item now auto-selects first available device if none connected

### Removed

- Last scanned card is no longer sent to newly connected WebSocket clients

## [1.0.0] - 2026-01-11

### Added

- Two-server architecture: Device Server (port 9470) for NFC readers and Client Server (port 9471) for applications
- Hardware NFC reader support via PC/SC (ACR122U and other PC/SC-compatible readers)
- Remote device support: smartphones, browsers with WebNFC, and custom hardware can connect as NFC readers
- NDEF read/write support for Text and URI record types
- MIFARE Classic, DESFire, and Ultralight tag support
- ISO14443-4 Type 4A tag support (experimental)
- JavaScript client libraries: NFCClient (consumer) and NFCDeviceClient (universal device input with configurable WebSocket client)
- Auto-TLS certificate management with CA bootstrap server (port 9472)
- mDNS/Bonjour service discovery for automatic device detection
- System tray UI for device management and status monitoring
- Cross-platform builds: Linux (amd64, arm64), macOS (amd64, arm64), Windows (amd64)
- Build versioning with embedded commit hash and build time
- Network change detection for automatic certificate regeneration
- Protocol validation for PC/SC device operations
- Support for handling unsupported NFC tags with error reporting
