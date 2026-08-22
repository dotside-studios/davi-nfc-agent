# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **The agent is assembled from plugins.** The agent built its own servers, and
  the tray drew every feature's menu, so a feature had to be part of the agent
  to exist at all: the servers' addresses, the pairing PIN and the paired
  devices were all drawn by code that knew what each of them was, and the
  listener was constructed in `agent.go`. A consumer building a turnstile on
  this agent had two ways in — edit the agent, or do without.

  `plugin` is the runtime everything that serves now goes through. A plugin
  declares only the phases it has work in — `Init` to wire up, `Start` and
  `Stop` to serve, `Close` on the way out — and is handed a context of its own:
  a menu, the register of addresses the agent hands out, routes mounted on the
  agent's listener, a snapshot of what the agent is doing and a signal raised
  whenever that changes, the clipboard, a browser and the log. Init and Start
  run in registration order and Stop and Close in the reverse of it, so what
  everything else is mounted on comes up first and goes down last.

  Nothing in it reaches `fyne.io/systray`, and a build with no tray discards the
  menus rather than making a plugin ask whether anything draws one. A plugin
  registered from an init function in a consumer's own package needs no change
  to the agent, one registered after the agent is already up joins it where it
  is, and one that fails to wire up is dropped without taking the rest with it.

  `plugin.Harness` runs a plugin through the real lifecycle against a tray that
  records instead of drawing, so a plugin is testable with no desktop involved.
  See [docs/custom-builds.md](docs/custom-builds.md)

- **A plugin can serve HTTP without a listener of its own.** The single listener
  took two handlers it knew by name, both the control center's. It now takes
  mounts, so any plugin with a page or an API of its own is reachable at the
  address a device already trusts, under the certificate it already has, with no
  port, no TLS and no trust story of its own. A mount asking for a path the
  agent serves itself — `/ws`, `/health` — is refused and logged, naming the
  plugin that asked, rather than panicking the router at startup; the root is
  claimable, since the agent's banner is only there while nothing else wants it.

  The control center is served this way now and has no other mechanism: it is a
  plugin asking for `/control/` and `/`, like any other.

  A route may carry a `Label`, which puts its address on the agent's menus
  beside the agent's own. The URL is built by whatever bound the port — it knows
  the scheme, the host and the port actually taken, none of which the plugin can
  be sure of — and withdrawn again when that listener stops, so a mounted page
  publishes an address without building one

- **The reader can say what it just did.** Someone holding a card at the reader
  had no way to tell a completed scan from one that never happened: the agent
  said so on a screen they were not looking at. **Flash and Beep on Scan** in
  the tray menu now has the reader answer for its own work, with one green
  flash and a short beep on a tag read, written or locked, and two red flashes
  when a write or a lock fails. It is off by default and the choice is saved to
  `settings.json`
- **`SCardControl`, and LED and buzzer commands for ACR122 readers.** The
  adapter over the platform's PC/SC library only carried commands to the card,
  so a reader's own peripherals were unreachable. `scardCard` gained `Control`
  in both backends, with the escape control code built per platform, and
  `nfc/pcsc` implements the ACS bi-color LED and buzzer command on top of it.
  Where a stack will not carry escape commands, which is the default on
  pcsc-lite and on the Windows CCID class driver, the same command travels as a
  pseudo-APDU over the card connection instead. The channel that answers is
  remembered per connection, and a reader that answers on neither is left alone

### Changed

- **The servers are a plugin, not part of the agent.** `agent.go` built the
  bridge, the device handler, the client handler and the listener, held all four
  as fields, and computed their URLs in the tray. An agent that served something
  else, or nothing, was not expressible: the servers were the agent.

  `plugins/wsserver` is that stack as a plugin, registered by the command line
  in one line and by nothing else. Drop the line and the build has no WebSocket
  endpoints — the agent still opens the reader, still holds the settings, still
  has a tray. What it needs from the agent is one interface stated in its own
  package and implemented in `wsserver_host.go`, the way the console's
  `webui.Host` is implemented in `webui_host.go`, and every value is read again
  on each restart rather than taken once, so a listener that comes back after a
  rotated secret comes back with the new one.

  The agent asks its plugins what they can do rather than which one they are:
  `ServingPort` asks whatever is serving where it is, the console asks whatever
  serves clients for its client list, and the paired-device requirement reaches
  whatever admits devices. Nothing in `agent.go` names the server package

- **The servers publish their addresses, and the tray draws what is published.**
  The tray built the device, client and pairing URLs itself and drew a fixed
  entry and a matching copy entry for each. Nothing else could appear there: a
  feature serving something of its own had no way onto a menu whose contents
  were a hardcoded list of the three the agent shipped with.

  A plugin registers a `plugin.Endpoint` instead. The addresses are declared
  before anything is listening, so they hold their place on a menu drawn first,
  and filled in from the port actually bound — these are pasted into a device,
  and an address naming an unbound port is worse than none. A stopped server
  empties its own, which is what makes it read as `Not running` rather than
  disappear. The tray renders whatever is in the register and knows what none of
  it is. A row is now the address and its copy entry in one — clicking it copies
  — so what is copied cannot drift from what is read

- **Pairing is a plugin, and the tray no longer knows what pairing is.** The
  pairing PIN, its copy and its regenerate entry lived in the tray's URLs
  submenu, the tray held the bootstrap server to work them, and `main` started
  it. It is now a plugin with a **Pair a Phone** menu of its own carrying the
  PIN, the page, both copy entries and the rotation — and, new, an entry that
  opens the pairing page here, for pairing a phone from the code on the
  operator's own screen. It owns its listener's lifecycle, so pairing now comes
  up and goes down with the agent rather than running whether or not the agent
  was started, and a build with no pairing server registers nothing rather than
  hiding entries. `NewSystrayApp` no longer takes a pairing server or its port

- **One watcher for the card, not one per surface.** The tray polled the client
  server twice a second for the tag on the reader, which is the only way to see
  one arrive — and it worked only while there was a tray. The agent does the
  looking now, once, and publishes a snapshot when it differs from the last;
  the tray's labels and every plugin read the same one, so they cannot disagree
  about what is on the reader, and a headless build sees card scans too

- **A stop arriving while the listener was still coming up.** `unifiedserver`
  built its HTTP server and mDNS registration on the goroutine that blocks until
  shutdown, and `Stop` read the same fields from another, which raced and could
  leave a listener bound after the stop. Both are now handed over under a lock,
  and a stop that arrives first is found by the start, which takes itself back
  down instead of binding

- **What the launcher set, the run keeps.** Three surfaces configure this agent
  and each had invented its own precedence: the port came with a lock flag, the
  pairing requirement with a different one under a different name, and the rest
  with none at all. The tray made it worse by persisting reader feedback and
  forgetting mode and card types, so where an operator clicked decided whether
  their choice survived a restart.

  `settings.Explicit` marks the fields a caller set deliberately, on the command
  line, in the environment, or when building the agent in code, and replaces
  both lock flags. A field marked there belongs to the launcher for the whole
  run: the stored file does not change it, an operator does not change it, and
  both the tray and the console show the control disabled with the reason rather
  than accepting an edit the agent would refuse. The file keeps the operator's
  own preference untouched underneath, so a start without the flag applies it
  again.

  The tray now writes every preference it changes to `settings.json`, as the
  console does. Both are the same operator at the same machine; only **allow any
  origin** is still deliberately session-only, in both, because a safety-off
  should not outlive the session that needed it. The tray also opens its menus
  from the agent instead of from a hardcoded default, so a stored read-only mode
  no longer reads as read/write until something else redraws it

- **The agent holds the settings, and the console reads them back from it.**
  Preferences lived in two places at once. `settings.json` had what was last
  saved, the agent had what it was actually doing, and the console rendered a
  mixture: mode and card filter from the file, the reader's live mode and the
  pairing requirement from the agent, in fields beside each other in the same
  snapshot. A mode switched from the tray never reached the file, so the console
  went on showing the old one, and a pairing requirement the command line would
  not let a preference withdraw was reported as withdrawn.

  `Agent.ApplySettings` puts a stored file in force and `Agent.Settings` reports
  what is in force in the same shape, so the agent parses and holds the whole
  preference set rather than having its parts pushed at it. The console's
  `settings` block is that answer, and the duplicates beside it (`reader.mode`,
  `reader.devicePath`, `security.requirePairedDevice`) are gone from the
  snapshot and from `webui.Host` with them. A pairing requirement that came from
  the command line is now shown as locked instead of springing back when
  switched off.

  There is also one path from a saved preference to the running agent: the
  store's change hook. The console and the tray write to the store and neither
  applies anything itself, so the two cannot put the agent in different states.
  The agent's port is the port it is set to and the listener reports the one it
  is bound on, which is what makes a port saved in the console take effect on
  **Restart servers** rather than only at the next startup

- **The tray menu is a package now, and its clicks are signals.** A menu item
  from the tray library *drops* a click when nobody is receiving on its channel,
  and a `select` cannot name a changing set of items, so the card filters, the
  readers, the origins and the paired devices were polled with a `default`
  branch after each event. A click on one of those rows was lost unless another
  item happened to be handled at the same moment.

  `traymenu` builds the menu declaratively on the same library, keeps a receiver
  on every item for its whole life, and fans each click out through a `Signal`.
  Handlers are declared next to their item and run one at a time on a single
  dispatch goroutine, so they still need no lock to touch menu state. A radio
  group and a fixed-pool list replace what the tray had open-coded three times
  each, the list reporting what did not fit rather than truncating quietly, and
  the reader picker no longer leaks a menu item per refresh.

  The menu the operator sees is unchanged, and a fake driver builds and clicks
  it under test with no desktop involved

### Fixed

- **A stored reader mode survives a restart.** The mode was applied to the
  reader and nowhere else, and settings are applied before `Start` builds one,
  so an agent saved as read-only came back from every restart able to write
  while the console reported the read-only it had read from the file. The agent
  holds the mode and hands it to each reader it starts, next to the feedback
  preference that already worked this way. Picking a mode from the tray with the
  agent stopped now sticks too, instead of the tick springing back

- **Copying a device URL from the tray hands out a device URL.** The Server URLs
  submenu showed `ws://host:9470/ws?mode=device` and copied
  `ws://host:9470/ws`, which is the client address: a device set up from that
  clipboard connected as a client and was never routed a tag. Both now come from
  one builder, so an entry copies what it reads. The pairing entry also stopped
  labelling itself `CA Cert` once the agent stopped

- **The offer to trust this agent in browsers comes back if the certificate
  authority goes away.** `CAInstalled` is a look at the filesystem taken on
  every call, not a decision taken once, but the tray only looked at it at
  startup and after an install of its own. A config directory that lost its CA
  therefore left the menu entry hidden until the agent was restarted, which is
  the one entry that would put the CA back. The tray now looks again whenever
  the listeners restart

- **All Types accepts a card type the agent has never heard of.** The filter
  admitted everything when it was empty, but **All Types** filled it with the
  eight types this agent enumerates instead of emptying it. A phone reports the
  tag types it recognizes, and one outside that list was then refused under a
  setting that says every type is welcome. Stored settings with no card types
  took the same path at startup

- **A device that said nothing about itself no longer refuses for its tags.**
  `capabilities` on `hello` was a value on the wire, so a device that omitted it
  and one that declared every field false arrived identically, and both read as
  a refusal. A device that reports what it finds on each tag while saying
  nothing about its own abilities therefore had every one of those tags reported
  incapable: the same collapse of silence into no that the per-tag capabilities
  work removed one level down, still in place one level up.

  The field is a pointer now, matching the per-tag one, and the three states are
  distinct. A device that sends the object is taken at its word, and a `false`
  refuses that operation for every tag it holds, because a bridge that cannot
  carry an operation cannot carry it for any tag reached through it. A device
  that omits it has declared nothing, so the request goes out and the device
  answers it. A v0 device always sends the original triple, so nothing about it
  changes.

  Reading it either way stays backward compatible: an omitted object and an
  explicit `null` both mean nothing declared, and an empty object means declared.

- **A device is no longer refused for what it calls itself.** Registration
  required `platform` to be `ios`, `android` or `web`, but nothing branches on
  the value: it reaches a column in the console and stops. The allowlist turned
  a description into an admission test on a bridge that carries whatever speaks
  the protocol, which is why `DeviceCapabilities` documents `deviceType` values
  like `pn532-serial` that could never register.

  The bundled client failed it too. `nfc-device-client.js` defaults `platform`
  to `unknown` and its Node example sends `node`, so following the shipped
  documentation was refused at the door. Any identifier is accepted now, and a
  device that sends none is recorded as `unknown`.

- **A write to a phone reports its outcome.** The device route answered with a
  bare `{uid, deviceID}` map while the client server reads a write outcome by
  asserting `*nfc.WriteResult`. The assertion failed, the whole block was
  skipped, and a client got `success: true` carrying none of the documented
  fields -- no `uid`, `tagType`, `bytesWritten`, `verified`, `attempts` or
  `locked` -- where the same write through the agent's own reader returned all
  six. Both routes now answer in the one shape, with `verified` asked of the tag
  rather than assumed from the route: a tag whose reads are a snapshot cannot
  confirm a write, which is the same fact the reader's pipeline consults.

- **A tag that declared nothing is unknown, not incapable.** `remotenfc.Tag`
  refused writes, locks and exchanges whenever the scan carried no per-tag
  capabilities, collapsing "the device said this tag cannot" together with "the
  device said nothing about this tag". Only a v1 device can describe a tag it
  scanned; a v0 device sends what it can do and no more, and the wire protocol
  keeps accepting that forever. So every v0 device held tags that could do
  nothing -- the hard-coded `false` the capability work exists to remove,
  surviving in a narrower form.

  The three states are now distinct. A tag that declared it cannot is refused, a
  tag declared read-only is refused however capable its device, and a tag that
  declared nothing defers to the device holding it, which is the inference the
  protocol documents. The device-level check is unchanged, so a capability that
  outlives its session still buys nothing.

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

  A request that names no tag is refused with `TAG_NOT_NAMED` unless it sets
  `allowUntargeted`, which is per-request rather than an agent-wide flag so one
  legacy client cannot weaken the guarantee for the others.

- **A tag declares what it cannot confirm, instead of the pipeline branching on
  its kind.** A tag whose contents arrive from elsewhere answers a read with the
  snapshot taken when it was scanned, so reading back after a write compares
  against data the write could not have changed: confirmation with nothing
  behind it. `TagCapabilities.ReadsAreSnapshot` says so, and the write skips the
  step rather than asking what kind of tag it holds. `nfc.AtomicLockWriter` does
  the same for locking, folding it into the write where a tag offers both in one
  exchange, so a failure between the two cannot leave data written and the lock
  not applied.

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
  caller polling a device with nothing in its field.

- **The agent starts without auto-TLS again.** `-auto-tls=false` and an
  externally provisioned `-cert`/`-key` both panicked at startup, before the
  systray appeared. Neither configuration has a certificate authority, and the
  bootstrap server is built to run without one so phones can still pair. Its
  nil checks did not work: the caller passed a nil `*tls.Manager`, and a nil
  pointer boxed into an interface is not a nil interface, so every check passed
  and then dereferenced nil. `NewBootstrapServer` now unboxes it once, so those
  checks hold

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
