# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `ServerPlugin.CheckOrigin` reports the origin check the listener applies, for
  whatever serves a WebSocket endpoint beside it. `ServerPlugin.OnOriginsChange`
  follows the allowlist from something built before the plugin activates
- `ServerPlugin.ClientCount`, `Clients`, `DisconnectClient` and
  `OnClientsChange` report on and act on the clients connected right now. A
  subscription outlives the server behind it, so it survives a restart
- `agent.Rebuildable` marks a component that captured its configuration when it
  was built. `Agent.RestartServers` stops and starts those and leaves the rest
  running, rather than reaching for the one component it knew by name
- The agent operates every reader through `nfc.Supervisor` rather than opening
  one at startup. `Agent.Reader` is `Agent.Supervisor`, and mode, feedback and
  Classic keys are set on the supervisor, which applies them to a reader opened
  later too. A client operation reaches the reader holding the tag rather than
  the one reader there used to be
- `nfc.Supervisor` operates every reader a manager offers rather than one chosen
  at startup. It opens each, polls it, and publishes what they scan on one
  signal, with each scan naming the reader it came from. A reader plugged in
  while it runs is picked up and one unplugged is dropped. Operations name the
  reader they apply to, so two readers do not queue behind each other, and mode,
  feedback and Classic keys are the supervisor's policy so a reader opened later
  runs under it too

- The pinned device filters rather than locks. A scan from a reader the operator
  is not asking for is dropped, wherever it was read, so a preference set from
  the console takes effect without waiting for something to restart the reader.
  A device that reports its own scans, such as a phone, is unaffected by which
  reader is pinned
- `event.Signal.Channel` hands back a channel carrying what a signal emits, and
  the function that stops it, for a consumer that drains on its own terms or is
  watching one while debugging. A full buffer drops rather than blocking, so a
  slow reader cannot stall a reader poll loop or a socket
- `nfc.NFCData` and `nfc.DeviceStatus` carry the device they came from, so a
  consumer knows which reader presented a tag rather than asking the tag what
  produced it. Filled by the reader and by the phone driver; the wire is
  unchanged
- A manager reports what its devices scan and answers for the tags they hold:
  `nfc.TagReporter` carries an `event.Signal` of `nfc.ScannedTag`, the tag as
  the device reported it, `nfc.TagHolder` is what the tag router asks, and
  `multimanager` implements both by fanning its children in. What is read off a
  tag is the supervisor's, so every scan is processed in one place however it
  arrived, and the agent subscribes to the supervisor alone
- Plugin API: `agent.Plugin` is one method, `Activate(agent.AgentContext) error`,
  run before the agent starts. A plugin registers a `Component` with `ctx.Use`, a
  route with `ctx.Mount` and a tray entry with `ctx.Systray`. Plugins are Go
  values the program constructs and registers with `Agent.Plugins.Add`; nothing
  is loaded at run time
- Three plugins ship: `agent.ServerPlugin` owns the listener and its
  `agent.Endpoint`s, `agent.PairingPlugin` runs the pairing server and its tray
  entries, `agent.TrustPlugin` holds the certificate the other two read
- `traymenu.Discard` and `traymenu.Section` as a `Container`
- Subscriptions: `Agent.Events()` publishes what the agent reports as typed
  signals, connected to at any time and disconnected through the handle each
  connection returns. `State`, `Preferences`, `Servers`, `Devices` and `Tag`
  carry the new value; `Any` carries an `agent.Change` naming what moved, for a
  surface that redraws. A plugin gets the same surface as `ctx.Events`
- `Events().Tag` carries every scan the agent broadcasts, so a program embedding
  the agent acts on cards without connecting to its own WebSocket. The broadcast
  to clients is unaffected
- `Events().Reader` carries the reader's status and `Events().Readers` the
  readers that can be picked, which used to reach the WebSocket clients and
  nothing else. `Agent.Readers` is the matching read, filtered so a phone is
  never offered as a reader to pin
- `event.Signal`, the fan-out primitive behind all of it, extracted from
  `traymenu`, which now aliases it
- Explicit agent lifecycle: `Agent.State` reports `stopped`, `starting`,
  `running` or `stopping`, `Events().State` observes settled transitions, and
  `Use` registers a `Component` started after the reader and servers and stopped
  first. A component that fails to start unwinds the ones before it
- `agent.DefaultOptions` returns what the flags default to, for building an agent
  without a command line
- `lock()`, `transceive()` and `getCapabilities()` on `NFCClient`, which had been
  on the wire for releases without reaching the library. With them: `tagRemoved`,
  `deviceID` on a scan, and `NFCRequestError` carrying the agent's error code and
  whether a retry could succeed
- Reader feedback: **Flash and Beep on Scan** in the tray has ACR122 readers
  flash green and beep on a read, write or lock, and flash red twice on a refused
  write or lock. Off by default
- `SCardControl` in both PC/SC backends, with the escape control code built per
  platform, and the ACS LED and buzzer command on top of it. Where a stack will
  not carry escape commands the same command travels as a pseudo-APDU; the
  channel that answers is remembered per connection
- `docs/custom-builds.md`, on building your own agent: the shipped agent as a
  `main.go` you could have written, then headless, no control center, no hardware
  backend, and reacting to tags in Go. Every Go sample compiles against this tree
- End-to-end tests in `e2e/`, wiring an agent as that page does and driving it
  over its published protocols on a real TLS listener: scans reaching clients and
  subscribers, writes and raw exchanges routed to a phone, read-only mode, pairing
  and revocation, and the lifecycle. Only the reader hardware is a stand-in

### Changed

- The clients connected to an agent are the server plugin's, not the agent's.
  `Agent.ClientCount`, `Clients` and `DisconnectClient` are the same three
  methods on `ServerPlugin`, and `Events().Clients` is
  `ServerPlugin.OnClientsChange`. The agent no longer holds a pointer to the
  server serving them
- `agent.OriginStore` and `agent.ParseAllowedOrigins` are `server.OriginStore`
  and `server.ParseAllowedOrigins`, beside the origin checks that read them, and
  the allowlist is `agent.ServerPlugin`'s: it builds the store under the agent's
  config directory, seeds it from `ServerPlugin.AllowedOrigins`, consults it on
  every upgrade and owns the tray's **Allowed Origins** section. `Setup` parses
  what the flags named onto `Runtime.AllowedOrigins` for the plugin to serve
  behind. An agent serving nothing holds no allowlist
- `agent.ServerPlugin` serves the clients. It runs the client server, mounts
  `/ws` and the health checks itself, and routes a connection by the mode it
  declares; `ServeMode` names a handler for either mode, such as a device
  driver's endpoint. An agent with no server plugin registered runs no client
  server and serves no HTTP, and still reports every scan through `Events()`
- The agent reports what its readers scan and what serves clients subscribes,
  rather than the agent pushing scans into a server it built. `Events().Tag`
  fires from the agent's own path now, so a plugin following the agent sees the
  same stream a client does, and the card-type filter and the pinned device
  still decide what passes
- `server.CheckAuth`, `server.CheckPairedDevice` and `DeviceAuth.Check` name the
  paired device they admitted, and `remotenfc.ServerOptions.Authenticate` and
  `agent.DeviceEndpointOptions.Authenticate` take that shape. A device admitted
  under a name registers under it; an empty name identifies nobody
- `nfc.Manager.ListDevices` is `Devices`, returning `nfc.DeviceListing`: the
  path, the identity the device holds with its driver, and what the driver
  knows it can do before anything opens it.
  `Capabilities.CanPoll` is what a reader list is built from, so a device that
  reports its own scans is left out by declaring itself rather than by the
  agent asking whether it is a phone. `nfc.DevicePaths` lists paths alone
- `remotenfc` names its devices by identity rather than prefixing them. An
  aggregate adds the prefix naming the manager, so a caller asking the driver
  had to know to strip one
- Choosing a device is a preference, not a restart. The pin filters what the
  agent serves, so the console and the tray set it rather than stopping and
  starting the agent, which dropped every connected client to change a
  preference. A phone can be chosen like any other device: filtering to one is
  the same operation whatever is holding the tag
- The console counts the devices it lists. "Remote devices" was the phone
  driver's own count of what it had registered, beside a panel built from the
  pairing registry; both come from the paired devices now, so a device shown
  offline is not counted as active
- The agent remembers the last scan rather than reading it back out of the
  client server, which kept it for nobody else. It survives a restart now: the
  servers are rebuilt, and the card on the reader is still there.
  `clientserver.Server.GetLastCard` is gone
- The client server asks the agent for the tag a request names rather than the
  readers the agent happens to hold. `Agent` implements `nfc.TagHolder`, so a
  plugin acts on a card through the agent too, and an operation before `Start`
  or after `Stop` is refused rather than reaching for a supervisor that is not
  there
- The supervisor answers for every tag the agent can reach, the ones on its
  readers and the ones the manager's own devices hold. A phone's scan already
  arrived on its signal, so what can be done to that tag is now asked in the
  same place, so what resolves a client request needs one holder and the
  agent's mode rather than a source per kind
- One interface answers for a tag wherever it is. `nfc.TagHolder` names the tag
  a device holds and performs the write, lock, raw exchange and capability
  report on it, and `nfc.Supervisor` implements it for the readers the agent
  opened. The tag router picks a source and calls it, having carried a branch
  per operation before
- `nfc.NFCReader` is internal. One reader covers one device, which is machinery
  behind `nfc.Supervisor` rather than something to hold: a program driving
  readers itself builds a supervisor, and `nfctest.EmulatedReader` is one over
  an emulated device
- `Config.RemoteOps` and `Config.RemoteScans` are gone, with `Options` matching.
  A driver of paired devices is registered with the manager, which is where the
  agent now asks. `server.DeviceOps` is `nfc.TagHolder`
- `server/unifiedserver` is now `server/listener`, package `listener`, so
  `unifiedserver.Server` and `unifiedserver.Config` are `listener.Server` and
  `listener.Config`
- A preference change is reported on its own signal. It used to ride the client
  hook, which runs when a client connects or disconnects, so nothing could
  follow one without the other
- `Options.RequirePaired` is `Options.RequirePairedDevice`, matching
  `Config.RequirePairedDevice` and the agent's own methods
- `Options.Version` is gone. Setup never read it; the shipped command's
  `parseFlags` returns it alongside the options instead
- One `Preferences` type. `agent.Preferences` is what the agent holds and what
  the console and the tray both take; `console.Preferences` is gone, along with
  the converter between them. The reader mode is an `nfc.ReaderMode` throughout
  and travels as its name on the wire, so the `ModeName` field that held the
  same value a second way is gone too. The JSON shape is unchanged
- `wsconn` moved to `server/wsconn`. `server.SafeConn` and `server.NewSafeConn`,
  which were aliases onto it, are gone: take `wsconn.SafeConn` directly
- `ServerPlugin` and `PairingPlugin` no longer take a `*TrustPlugin`. Each takes
  what it needs: `ServerPlugin.Config` the certificate files, with `Setup`
  resolving them onto `Runtime.CertFile`/`KeyFile`, `ServerPlugin.Certificates`
  the reissue signal, and `NewPairingPlugin` a `tls.CertificateAuthority`.
  `TrustPlugin` keeps the tray entry that installs the local authority and loses
  `CertFile`, `KeyFile`, `Authority` and `Watcher`
- The `webui` package merged into `agent/console`, which now holds the gate, the
  routes, the state snapshot, the dispatcher, the `Host` adapter and the
  embedded frontend. `webui.Host` and `webui.Preferences` are `console.Host` and
  `console.Preferences`; the two-layer `Config`/`New`/`Server` collapsed into one
- The agent holds no listener. `Agent.Routes` is what it serves of its own,
  `/ws` and the two health checks, as data for whatever mounts it. `Setup` builds
  no listener and no pairing server: the program registers both as plugins. Gone
  with it: `Config.Server`, `Agent.UnifiedServer`, `Runtime.Server`
- The agent holds no certificate. `agent.TrustPlugin` wraps the `*tls.Manager`
  from `Runtime.Certificates`, and the pairing server and listener take narrower
  contracts: `PairingConfig.CA` is `tls.CertificateAuthority`, two methods rather
  than the whole manager
- `tls.Manager` reports every reissue on `CertificateWatcher.WatchReissues`, and
  `ServerPlugin` rebinds on one, so installing a CA no longer needs a restart
- The server plugin owns the tray's Server URLs submenu: the device and client
  addresses, the API secret, and their copy and regenerate actions moved out of
  `agent/tray`
- `traymenu` no longer imports a toolkit. The Fyne driver is `traymenu/fynetray`,
  so only a command using the real tray needs cgo on macOS, and `traymenu.New(nil)`
  draws nothing. Clipboard copying is its own `clipboard` package
- The binary is `cmd/davi-nfc-agent`, and the module root holds no Go files. The
  agent, console and tray are packages of their own; the root package was 19
  files in which the tray read the agent's private fields
- `Agent` configuration is settled at construction. `Setup` assigned twelve
  exported fields one at a time after `NewAgent`, so anything holding the agent
  could rebind the port or swap the origin allowlist behind the running servers
- `agent.Runtime` carries only what the agent does not. `Origins`, `Devices`,
  `Bootstrap` and `BootstrapPort` were assigned twice and could drift apart
- `tray.New` takes the `agent.Runtime` rather than four fields unpacked from it
- `buildinfo` is per-build rather than package-level variables, so a program
  built on the agent announces its own name and writes to its own config
  directory instead of `davi-nfc-agent`'s
- Routes are mounted on the server rather than configured into it. A console
  attached after the mux was built was never served
- CORS is applied per route at the mount, rather than wrapping everything except
  two routes named in the server's own code
- The agent no longer knows what a console is: `agent.Console`, `SetConsole` and
  the accessors are gone, and a program mounts the control center's routes itself
- The device protocol lives with the driver that speaks it. `protocol` held both
  wire formats; the device half is now in `nfc/remotenfc`
- The device endpoint requires an authenticator. `Manager.Handler` took an origin
  check and nothing else, so mounting it unwrapped served an open endpoint
- `deviceserver` is split into what it was: a credential check and the routing
  that answers "reader or device?"
- The agent drains its own tag sources; the reader pump was in `deviceserver`
- `ServerBridge` is gone. It was six channels connecting two objects that could
  call each other. `server.TagOps` replaces the request half, and
  `protocol.CodedError` carries a wire code on an ordinary error
- `nfc.WriteMessage` is the write pipeline for any tag. Encoding, the capacity
  check, retries and confirmation were methods on `*NFCReader` that never touched
  one
- A tag declares what it cannot confirm rather than the pipeline branching on its
  kind. `TagCapabilities.ReadsAreSnapshot` says a read answers from the scan, so
  the write skips a confirmation it cannot trust
- The agent no longer searches its manager for a device driver.
  `server.DeviceOps` and a scan channel are handed in, so nothing above the
  manager imports `nfc/remotenfc`
- `Agent.Shutdown` is the way out; `Stop` pauses. `Stop` no longer closes the NFC
  manager, which is built once for the process
- The control center consumes the client library instead of reimplementing it.
  `webui/frontend` imports `@davi/nfc-agent-client`; what is left is the event
  feed and the scan history
- `client/` is TypeScript and `client/dist` is generated from it. `make client`
  builds `dist/`, which is committed so a `<script>` tag still works. The package
  is `@davi/nfc-agent-client`, was `@davi/nfc-client`. `getStatus()` and
  `getLastTag()` are gone: they called endpoints this agent does not serve.
  Reconnection backs off from 250ms to 5s rather than retrying every 3s

### Fixed

- A paired device connects as itself. It registered under an identity minted per
  connection, so nothing could match it to the device that paired: the console
  showed every paired device offline while it was connected. The credential
  names the device, and the driver registers it under that name
- Starting without naming a device is auto-detect again. It pinned whichever
  reader was listed first, so an agent that polls every reader dropped the scans
  of all but one of them, and the preferences reported a choice nobody made
- The tray follows the agent rather than only its own clicks. A mode, filter or
  feedback setting changed from the console left the tray's menu showing the old
  value, a device paired elsewhere did not appear, and an agent that stopped on
  its own left the tray offering Stop
- The tray no longer offers a phone as a reader to pin. It listed the manager's
  devices raw, where the console filtered them through `nfc.ListReaders`;
  choosing one pinned the reader to a device that is never opened
- A stop takes the pumps down with the servers. `stopLocked` repeated their
  teardown inline and left out the cancel, so the goroutine draining the reader
  survived every stop and a start after it added another
- More than one subscriber can follow devices and origins. `DeviceRegistry.OnChange`,
  `OriginStore.OnChange` and `OriginStore.OnBlocked` stored a single callback, so
  the last registration replaced the previous one: with the console and the tray
  both following the registry, only the tray was told when a device was paired
- `traymenu.Close` now stops clicks. It closed a done channel that the click
  path and the dispatch loop each selected on alongside the click queue, and Go
  picks at random when both are ready, so a click racing a close ran about half
  the time
- Every method on a nil `*console.Server` tolerates it, as a build without a
  console has always been documented to expect. `NotifyChange` and `ConsoleURL`
  did not
- `Config.CardTypes` reached nothing. New built an empty card-type filter and
  dropped it, so an agent built with a card-type allowlist read every type
- Choosing a reader from the control center did not announce the change, so a
  second open console page kept showing the previous one until something
  unrelated redrew it. Every other preference already announced itself
- A write can no longer land on a tag the client did not mean. Requests were
  routed by preference, evaluated when the request arrived rather than when the
  tag was scanned, so lifting a card between the two moved the write. Requests
  now carry `uid`; new codes `TAG_MISMATCH` and `TAG_NOT_NAMED`, and a
  per-request `allowUntargeted` for a client that cannot name its tag
- A write through the client library could not succeed: it named no tag, which
  the agent refuses with `TAG_NOT_NAMED`
- A write to a phone reports what it did. The device route returned a bare map
  where the client server reads an `*nfc.WriteResult`, so the response carried
  none of the documented fields
- Restarting the servers no longer doubles the reader. `startServers` called
  `NFCReader.Start` every time while `RestartServers` leaves the reader running,
  so each reissue or rotation left another worker polling it
- A restarted agent serves again. `Agent.Stop` dropped the server it was given
  and `unifiedserver.Server` refused to start once stopped, so a stop-and-start
  left a dead port
- A port already in use fails the start. `unifiedserver.Start` ran on a goroutine
  whose error was dropped, so `Start` returned nil with nothing listening
- `Start` and `Stop` no longer race. Only `RestartServers` took a lock, while the
  tray, console and network watcher reach all three from different goroutines
- A quick stop could crash the agent: a goroutine read the `UnifiedServer` field
  rather than a captured value, so a `Stop` landing first left it starting nil
- The card-type filter no longer races. It was a bare map handed to the device
  server and mutated in place by the console and the tray
- Package `agent` no longer writes to process-wide state: `flag.BoolVar`,
  `flag.Parse` and `log.SetOutput` belong to a program, not a library
- A hand-built `nfc.Card` reports an error instead of faulting. `Read` assumed
  the unexported tag that `NewCard` sets
- The agent starts without auto-TLS again: `-auto-tls=false` and an externally
  provisioned `-cert`/`-key` both panicked before the tray appeared
- A phone's device reports the tag it is holding. `remotenfc.Device.GetTags`
  waited and returned nothing, so held tags were tracked a second time on the
  manager
- A tag that declared nothing is unknown, not incapable. `remotenfc.Tag` refused
  writes, locks and exchanges whenever a scan carried no per-tag capabilities,
  which is every tag a v0 device holds
- A device that said nothing about itself no longer refuses for its tags.
  `capabilities` on `hello` was a value on the wire, so an omitted block and one
  of all falses arrived alike. It is a pointer now
- A device is no longer refused for what it calls itself. Registration required
  `platform` to be `ios`, `android` or `web`, which the bundled client's own
  default of `unknown` and its Node example both failed
- A phone is no longer offered as the agent's own reader
- A reader that will not open is reported once, not on every poll
- The tray's dynamic menus receive their clicks. The tray library drops a click
  when nobody is receiving, and the card filters, readers, origins and paired
  devices were polled between other events, so those clicks were lost
- Copying a device URL from the tray hands out a device URL, not the client one
- The offer to trust this agent in browsers returns if the certificate authority
  goes away. `CAInstalled` reads the filesystem on every call, but the tray only
  looked at startup
- All Types accepts a card type the agent has never heard of. It filled the
  filter with the eight types this agent enumerates instead of emptying it
- The control center's capability panel was blank on every tag: it read five
  fields the agent does not send
- The console shows what an NDEF record says. The record tables read `r.text` and
  `r.uri`; the agent sends one `content` field
- A deliberately disconnected client auto-reconnects again: `connect()` did not
  clear the flag `disconnect()` sets
- The protocol reference described NDEF records the agent never sent

### Removed

- `Events().Clients` and `ChangeClients`. An agent with no server plugin
  registered has no clients to count, so the signal was only ever meaningful
  through the plugin that now carries it
- `Agent.Origins`, `Config.Origins`, `Agent.CheckOrigin`, `Events().Origins`,
  `Events().Blocked`, `ChangeOrigins` and `ChangeBlocked`. Which browser origins
  may connect is a question for what serves the connection, so it is the server
  plugin's; a build with no plugin registered admits nobody by origin because it
  admits nobody at all
- `Config.DeviceEndpoint`, `Options.DeviceEndpoint` and
  `agent.DeviceEndpointOptions`. A program builds its device endpoint from what
  the agent answers, `DeviceAuth.Check`, `TagModificationAllowed` and
  `PublicKeyPin`, plus `servers.CheckOrigin()`, and mounts it as
  `ServerPlugin.ServeMode[server.ModeDevice]` rather than handing the agent a
  builder to call
- `Agent.ClientServer`, `Agent.Routes` and `agent.Route`. What the agent serves
  is the server plugin's, so the agent holds no server and hands over no routes.
  What answers about the clients moved with it; see Changed
- `clientserver.Config.OnTag`. It let an embedder observe a scan before the
  clients saw it, which is what the agent used it for; the agent reports the
  scan before handing it over now, so nothing sets it
- `Agent.RemoteDevices` and `console.Host.RemoteDevices`. The agent reached past
  its manager into the child holding phones for a count the console can take
  from the devices it already lists. `Agent.OnlineDevices` answers from what the
  manager reports, by the identity each device holds
- `nfc.RemoteManager`, `nfc.ReaderLister` and `MultiManager.ListReaders`. Which
  devices this agent can read from is what a driver declares about each of them,
  rather than three interfaces asking whether a manager holds phones
- `nfc.IsRemoteDevice`, `nfc.RemoteDeviceChecker`, `MultiManager.RemoteDevice`
  and `Agent.IsReader`. They kept a phone from being pinned, back when the pin
  named the device the agent opened and polled: a phone there became a
  connection retried for the life of the process. Nothing opens the pin now
- `server/tagrouter`. Resolving a client request to the tag it names is what the
  client server does with the holder it was given, and the wire vocabulary it
  answers in was never a source's to speak. `clientserver.Config` takes `Tags`
  and `AllowTagModification`; `Config.Ops` still replaces the lot for a build
  with its own. `Agent.Router` is gone with it
- `tray.App.AttachConsole`, `console.Server.AttachTray` and the `console.Tray`
  interface. The tray held a console it never read, and the console acted
  through the tray so its menu would follow; the tray follows the agent's
  events now. What is left is `console.Config.Quit`, since ending the program
  is the program's
- The tray's 500ms card poll and its direct subscription to the NFC manager's
  device-change channel. Both are `Agent.Events()` subscriptions now
- The settings file and everything that arbitrated with it. Preferences were
  persisted to `settings.json` and read back at startup, which is what made three
  shapes necessary for six values. The agent holds them now and nothing writes
  them anywhere: `agent.Config` goes in, and a change made from the console or a
  tray menu lasts as long as the agent runs. Gone with it: `settings.Store`,
  `settings.Explicit`, `ApplySettings`, `SetExplicit`, `Runtime.Settings` and
  `AgentContext.Settings`. `agent.Config` gains `Mode`, `CardTypes` and
  `DevicePath`, which it could never carry
- Package `settings`. `ParseMode` and `FormatMode` are `nfc.ParseReaderMode` and
  `ReaderMode.String`; the console keeps its own `webui.Preferences`
- Dead code in `deviceserver`: `Handle` had no callers, the `devices` map was
  never read, and the fallback WebSocket loop ran only with no driver configured
- The agent's callback registrations, replaced by `Agent.Events()`: `OnTag`,
  `OnStateChange`, `OnClientsChange`, `OnPreferencesChange`, `OnServerRestart`
  and the `ServerRestarts` channel. The channel had one consumer by
  construction, and a second reader would have taken the signal from the first

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
  closed device as `fmt.Errorf("device is closed")`, its own phrase, matching
  no sentinel. `IsDeviceClosedError` looks for the typed `ErrDeviceClosed` or,
  failing that, the string `device closed`, so it declined every one of them:
  two spellings that read identically to a person and not at all to
  `strings.Contains`. Nothing downstream handled a phone disconnecting as a
  result: the reader still had a device, so it polled straight back into the
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
  as the device was polled, and the reason a line is worth reading is that the
  condition changed. It now reports a fault the first time, again when the
  reason changes, and again when one returns after the device has worked,
  matching what `ListDevices` was given in 1.1.1 against the same log buffer
- **The control center no longer discards tags scanned by a phone.** A tag
  tapped on a paired phone reached the frontend and never appeared in the
  console, which reads the same broadcast on the same endpoint. The console
  cleared its tag whenever a `deviceStatus` reported no card, and that status
  describes the agent's own reader, whose `cardPresent` is false for the entire
  life of every phone scan. The tag was received, displayed, and wiped by the
  next status message. No other consumer reads tag presence out of reader
  status, which is why only this one lost them. Reader status now clears only a
  tag the reader itself produced; a tag from a device is cleared by the device
  saying so
- **A tag leaving a phone's field is recorded as a removal.** A device reports it
  as a `tagData` with no UID, which the console treated as a scan, rendering a
  blank tag over the real one and logging a scan of nothing

## [1.1.1] - 2026-08-15

### Added

- **Trust this agent in browsers, without a terminal.** A browser cannot open a
  `wss://` connection to an untrusted certificate, and unlike a page visit there
  is no warning to click through: the page simply never connects, and nothing
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
  never present, and a device that paired with one was locked out permanently.
  re-pairing returned the same unusable pin. The CA route now signs the same
  persistent key, so the pin holds across adopting a CA and devices paired
  before **Trust This Agent in Browsers** keep working after it. An install
  already serving the wrong key reissues on the next start
- **Reissuing a certificate no longer installs a certificate authority.**
  `RegenerateCertificates` called the CA routine directly instead of going
  through the routing that picks self-signed or CA, so the Control Center's
  **regenerate** action, labelled as nothing more than reissuing a certificate,
  created a local CA, installed it in the system trust store and prompted for
  a password, on an agent that had deliberately never had one. That defeated
  1.1.0's own change to stop installing a CA by default. Reissuing now keeps
  whichever route the install already uses, and putting a CA in the trust store
  happens only when explicitly asked for
- **A missing reader no longer floods the log.** `ListDevices` is polled
  continuously by the tray, the console and the device watcher, and every failed
  poll logged. With no reader attached that was 85 of 108 lines, one repeating
  message, drowning anything else that happened, including the certificate
  errors the log was added to surface. A failure is now reported once, again if
  the reason changes, and a recovery gets a line of its own. Same conditions
  measured after: 1 line in 21

## [1.1.0] - 2026-08-15

### Added

- **Control Center.** A web console served by the agent itself at its own root,
  covering what neither the tray nor the flags could do. Four tabs: Overview,
  Tag, Activity and Security. The reader controls (start/stop, device,
  mode, card filter, port) sit on Overview rather than behind a settings tab,
  so the page an operator lands on is the page they work from. Opened from the
  tray's
  **Open Control Center**, which mints a single-use token and hands it to the
  browser. Being same-origin, it is also the one browser client that works on a
  fresh install without `-install-ca`: a page visit can be accepted manually,
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
  `webui/`: the gate, the routes, the state snapshot, the dispatcher and the
  frontend it embeds. None of it imports the agent. It declares what it
  needs of the agent as `webui.Host`, implemented by a single adapter in
  `package main`, which both keeps the console's whole reach into the agent
  readable in one file and lets its tests run against a fake host with no
  hardware. `go build -tags nowebui .` then drops it: no `/control` routes, no
  privileged API, no tray entry and no embedded console, about 820 KB smaller,
  with none of the console's strings present. Only three files in `package main`
  carry the constraint; the call sites tolerate a nil console, so no shared file
  needs a tag of its own. The agent's own protocol is unaffected: raw tag
  exchanges, settings persistence and the log ring remain in both builds, each
  being reachable without the console
- **Raw exchanges with a tag, from a client and from the console.** The agent
  could already transceive with a tag, but only agent-to-device: no client
  could ask for one, so DESFire, ISO-DEP applets and capability probing meant
  writing a program. `transceiveRequest`/`transceiveResponse` open that channel
  to clients, routed like a write: to the device holding a tag, otherwise to
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
  issued, counted per connection, so a client that is only listening is
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
  reachable on an address the certificate omits, previously indistinguishable
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
  round trip per command, so it is for what genuinely needs it (DESFire,
  ISO-DEP applets, capability probing) and not as a general read path
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
  `AllowedOrigins`, the escape hatch built alongside it, was never populated:
  no flag, no environment variable, and `agent.go` left it nil on both servers.
  Every browser console was affected, since a page is by construction served
  from somewhere other than the agent's port. The REST endpoints meanwhile
  answered `Access-Control-Allow-Origin: *`, so the two halves of the same API
  disagreed about who may call them
- Pairing required auto-TLS. The endpoint lived on a server that only ran when
  the agent managed its own certificates, so the deployment using an externally
  provisioned certificate, the one that most needs per-device credentials,
  had no way to pair
- `ServerBridge.Close` closed channels that producers could still be sending
  on, which panics the losing goroutine. Only the done channel is closed now;
  consumers already exit on their own context
- A tag scan was published to clients before the route a write needs was
  registered, so a client reacting to `tagData` with an immediate write could
  be told no device held a tag it had just been told about
- Tag scans that fail to parse (a malformed UID, an undecodable NDEF message)
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
  allowlist is deliberately not consulted: an entry there authorises a console
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
  composable with `lock`). Reversible: the tag can be rewritten afterward
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
