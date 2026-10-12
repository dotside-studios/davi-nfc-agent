# Custom Builds

The agent is a Go module, and the binary in `cmd/davi-nfc-agent` is an ordinary
program built from packages this repository exports. To change what it does,
write your own `main.go` against those packages.

```bash
go get github.com/dotside-studios/davi-nfc-agent
```

Pin the version you build against. These packages follow the agent's releases
and do not yet carry a compatibility guarantee.

## A complete agent

This is a trimmed version of
[`cmd/davi-nfc-agent/main.go`](../cmd/davi-nfc-agent/main.go): TLS, pairing, the
WebSocket API, the control center and the tray, with the command line replaced
by `agent.DefaultOptions`. The shipped program also prints `-version`, runs
without TLS when provisioning fails, reloads the key file on `SIGHUP`, and quits
the tray on `SIGINT` and `SIGTERM`; this one leaves those out.

```go
package main

import (
	"log"
	"net/http"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/agent/console"
	"github.com/dotside-studios/davi-nfc-agent/agent/pairingplugin"
	"github.com/dotside-studios/davi-nfc-agent/agent/serverplugin"
	"github.com/dotside-studios/davi-nfc-agent/agent/tray"
	"github.com/dotside-studios/davi-nfc-agent/agent/trustplugin"
	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/multimanager"
	"github.com/dotside-studios/davi-nfc-agent/nfc/pcsc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/secure/pairing"
	tlspkg "github.com/dotside-studios/davi-nfc-agent/secure/tls"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/listener"
)

func main() {
	opts := agent.DefaultOptions()

	// The console reads its log from this ring. Install routes the packages
	// that log on their own channels into it; Options hands it to the agent.
	opts.Logs = logbuf.New(logbuf.DefaultCapacity)
	logbuf.Install(opts.Logs)

	// Hardware readers and phones behind one manager.
	devices := remotenfc.NewManager(remotenfc.DeviceTimeout)
	backends := multimanager.NewMultiManager(
		multimanager.ManagerEntry{Name: nfc.ManagerTypeHardware, Manager: pcsc.NewManager()},
		multimanager.ManagerEntry{Name: nfc.ManagerTypeSmartphone, Manager: devices},
	)

	// The certificate lives under the config directory the agent will use, so
	// resolve it the same way Setup does.
	if opts.ConfigDir == "" {
		opts.ConfigDir = agent.DefaultConfigDir(opts.Info.OrDefault().DirName)
	}
	certs, err := tlspkg.Provision(opts.ConfigDir, opts.InstallCA)
	if err != nil {
		log.Fatal(err)
	}
	opts.CertFile, opts.KeyFile = certs.CertFile, certs.KeyFile
	opts.PublicKeyPin = certs.PublicKeyPin

	paired := pairing.New(backends, pairing.Options{
		ConfigDir:    opts.ConfigDir,
		CA:           certs.Manager,
		AppName:      opts.Info.OrDefault().DisplayName,
		PublicKeyPin: func() string { return certs.PublicKeyPin },
	})

	rt, err := agent.Setup(opts, backends)
	if err != nil {
		log.Fatal(err)
	}

	// Read per request, so a policy changed while the agent runs needs nothing
	// rebuilt.
	paired.UseSecret(rt.Agent.APISecret)
	paired.Require(rt.Agent.RequirePairedDevice)
	paired.AllowLoopback(rt.Agent.AllowLoopbackBypass)
	paired.UsePort(rt.Agent.DevicePort)

	servers := &serverplugin.Plugin{
		Config:         listener.Config{CertFile: opts.CertFile, KeyFile: opts.KeyFile},
		Certificates:   certs.Manager,
		AllowedOrigins: server.ParseAllowedOrigins(opts.AllowedOrigins),
		TokenVerifier:  paired.TokenVerifier(),
	}
	servers.ServeMode = map[string]http.Handler{
		server.ModeDevice: paired.Admit(devices.Handler(remotenfc.ServerOptions{
			CheckOrigin:          servers.CheckOrigin(),
			AllowTagModification: rt.Agent.TagModificationAllowed,
			PublicKeyPin:         rt.Agent.PublicKeyPin,
		})),
	}
	servers.Add(serverplugin.Endpoint{
		Name:    "pairing",
		Pattern: "/pair",
		Handler: paired.PairHandler(),
	})

	app := tray.New(rt)

	c := console.New(console.Config{
		Agent:         rt.Agent,
		Logs:          rt.Logs,
		Servers:       servers,
		Pairing:       paired,
		BootstrapPort: opts.BootstrapPort,
		Certificates:  certs.Manager,
		Quit:          app.Quit,
	})
	servers.Add(c.Endpoints()...)

	// The server plugin goes first: it publishes the listener the others mount
	// on. Activation order is also the order of their tray entries.
	plugins := []agent.Plugin{servers}
	if opts.BootstrapPort > 0 {
		plugins = append(plugins, pairingplugin.New(paired, opts.BootstrapPort))
	}
	plugins = append(plugins, &trustplugin.Plugin{Manager: certs.Manager})
	if err := rt.Agent.Plugins.Add(plugins...); err != nil {
		log.Fatal(err)
	}

	app.Run()
}
```

The rest of this page refers back to `opts`, `rt`, `certs`, `paired`, `devices`
and `servers` as declared here.

### What Setup does

`agent.Setup(opts, manager)` resolves the config directory, loads the API secret
from it or generates one on first run, and loads the key file named by
`Options.KeysFile` or `DAVI_NFC_KEYS`, failing if that file cannot be loaded
(see [Card keys](card-keys.md)). It also reads the environment variables that
raise `RequirePairedDevice`, `AllowLoopbackBypass` and `AllowRawAPDU`. It returns
an `*agent.Runtime` holding the agent, the log ring, the device path to open and
the resolved config directory.

`Setup` provisions no certificate, builds no listener and reads no paired
devices. `CertFile`, `KeyFile` and `BootstrapPort` on `Options` are passed
through for the program to hand to whatever serves them, and `PublicKeyPin` goes
to the agent as it is, so that devices are told it.

`agent.New` is the alternative to `Setup`, for a program with its own
configuration: it takes an `agent.Config` built from values you already hold.
Either way the configuration is fixed once the agent exists and is read back
through methods, so nothing can rebind the port or withdraw the pairing
requirement behind the running servers. The preferences that may change while
running have setters: `SetReaderMode`, `SetCardTypeFilter`, `SetPinnedDevice`,
`SetDevicePort`, `SetRequirePairedDevice` and `SetReaderFeedback`. Nothing
persists them: a change lasts as long as the agent runs. `SetKeys` replaces the
card keys, including on readers already open, which is what the shipped
command's `SIGHUP` reload calls.

Flags and the standard logger belong to the program. The shipped command
registers its flags on the global flag set in
[`cmd/davi-nfc-agent/flags.go`](../cmd/davi-nfc-agent/flags.go) and fills
`Options` from them; package `agent` registers none, so it does not collide with
the flags of a program embedding it.

### Certificates

`tlspkg.Provision` manages a certificate under the config directory and returns
a `tlspkg.Provisioned`: the `*tlspkg.Manager` that reissues it and holds the
local authority, the pair a listener serves, and the public key pin. Each
consumer takes the part it needs: `serverplugin.Plugin.Config` the files,
`serverplugin.Plugin.Certificates` the reissue signal, `pairing.Options.CA` and
`console.Config.Certificates` the authority, and `trustplugin.Plugin` the
manager, for the tray entry that installs the authority into the system trust
store. A build serving a certificate provisioned elsewhere names the pair on
`listener.Config` and leaves `Certificates` nil, there being nothing to reissue.

### Pairing

`pairing.Gate` is the credential store, the `/pair` endpoint that issues into
it, the check that admits on it, and the revocation that ends a session when a
credential is withdrawn. The agent is handed the backends, not the gate. What
the gate needs of a backend is `pairing.Sessions`, one method, so a revocation
can reach a session already open; pass the manager tree, or nil for backends
holding none.

Its policy comes from `UseSecret`, `Require` and `AllowLoopback`, each read per
request, so rotating the secret, withdrawing the paired-device requirement or
changing the bypass needs nothing rebuilt. See
[The loopback bypass](security.md#the-loopback-bypass) for what `AllowLoopback`
admits.

The gate covers both halves of `/ws`. `paired.Admit` wraps the device endpoint
and names the device it admitted, so the driver registers it under the identity
it paired with. `paired.TokenVerifier()` on the server plugin lets the client
server accept the same credentials. Leave both out and the build pairs nobody:
mount the device endpoint bare and every device is admitted on the API secret
alone, each under an identity the driver mints per connection. That is what a
build reached only over a trusted transport, and every test, wants.

`/pair` is mounted from the gate, so devices can pair over the agent's own
listener whatever the build does about `pairingplugin`. The plugin adds the
cleartext bootstrap listener that hands the authority to a device that does not
trust the certificate yet, and the tray entries for its address, PIN and paired
devices. For the listener without the menu entries, register
`pairingplugin.NewServer(paired.PairingServer(), port)` with `ctx.Use` or as a
`serverplugin.Endpoint` component.

### The NFC backend

The manager is `Setup`'s second argument, which is why every package beneath
`cmd` builds without one. A manager reports what its devices scan through
`nfc.TagReporter` and answers for the tags they hold through `nfc.TagHolder`,
both optional, and `multimanager` implements both by fanning its children in.
Serving phones is not the manager's business: the driver's endpoint goes on the
server plugin as `ServeMode[server.ModeDevice]`, and a build that mounts none
serves its own readers alone.

## Plugins

A plugin is a value with one method. It is handed an `agent.AgentContext` once,
before the agent starts, and registers whatever it wants the agent to run.

```go
type BackupPlugin struct {
	Every time.Duration
}

func (p *BackupPlugin) Activate(ctx agent.AgentContext) error {
	backups := &backupWorker{every: p.Every, dir: ctx.ConfigDir()}

	ctx.Systray.Add("Back Up Now", traymenu.OnClick(backups.Run))
	return ctx.Use(backups)
}
```

```go
rt.Agent.Plugins.Add(&BackupPlugin{Every: time.Hour})
```

`backupWorker` here is any `agent.Component`: `Name() string`,
`Start(context.Context) error` and `Stop() error`. A plugin that also implements
`Name() string` is called that in logs and errors; otherwise it is named after
its type.

A build's plugins are what it imports, fixed at compile time, so one left out
takes its dependencies with it, the same way `nfc/pcsc` and the tray do.

The context carries what a plugin needs to wire itself in:

| | |
|---|---|
| `ctx.Agent` | The agent, for its configuration and for what it can be told to do |
| `ctx.Events` | What the agent reports: see [Following the agent](#following-the-agent) |
| `ctx.Use(c...)` | Registers `agent.Component`s, started and stopped with the agent |
| `ctx.Systray` | The menu the plugin's entries go on |
| `ctx.Serve(m)` | Publishes the `agent.Mounter` the agent serves from |
| `ctx.Mount(pattern, h)` | Adds a route to it |
| `ctx.Logger()`, `ctx.LoggerAt(level)`, `ctx.Info()`, `ctx.ConfigDir()`, `ctx.Logs()` | The plugin's log channel, and the agent's identity, config directory and log ring |

`ctx.Systray` is the top level of the tray's own menu, so a plugin's entry looks
no different from one the tray declared itself. Entries land where the tray
activated the plugins, since a menu item always goes to the end of its parent. A
plugin with more than one entry groups them under a submenu of its own, with
`ctx.Systray.Section("Backups")`.

`ctx.Systray` is never nil. A headless agent hands over a menu that draws
nothing, so a plugin can add its entries without checking for a tray.

A plugin has no `Deactivate`. Anything with a lifetime is a `Component`, which
the agent starts once the reader and the servers are up and stops, in reverse
order, before taking them down again.

### Activation

Plugins are activated once, in the order they were added, before anything is
opened or bound. The tray does it as it draws its menu, so the entries land on
the real one; `Agent.Start` does it if nothing else has. Adding a plugin after
that is refused.

Activation is also where a plugin publishes what the agent is served from and
mounts its routes, so a listener's port and address are worth reading only once
it has happened.

```go
// A headless build with no tray to draw their entries on.
if err := rt.Agent.Activate(nil); err != nil {
	log.Fatal(err)
}
```

A plugin that returns an error fails the agent's start, naming the plugin, and
the same failure is reported by every start afterwards.

### The shipped plugins

Three plugins ship with the agent. A build registers what it wants; registering
none leaves an agent that drives the reader and serves nothing.

| Plugin | Owns |
|---|---|
| `serverplugin.Plugin` | The listener, everything mounted on it, the client server, the origin allowlist, and the tray's **Server URLs** and **Allowed Origins** submenus |
| `pairingplugin.Plugin` | The pairing bootstrap listener, and the entries showing its address, PIN and paired devices |
| `trustplugin.Plugin` | The entry that installs the local certificate authority |

The per-field details are on the types: `go doc serverplugin.Plugin`,
`serverplugin.Endpoint`, `pairingplugin.New`, `trustplugin.Plugin`.

### The server plugin

The server plugin goes on first. It publishes the listener with `ctx.Serve`,
which is what backs `ctx.Mount` for every plugin registered after it, and an
`agent.Mounter` is one method wide so the agent never names a server type.

What goes on that listener is a `serverplugin.Endpoint`: a route, something with
a lifetime, a menu entry, or any combination.

```go
servers.Add(serverplugin.Endpoint{Name: "webhooks", Pattern: "/hooks/", Handler: hooks})
servers.Add(serverplugin.Endpoint{Name: "queue drain", Component: drain})
```

The plugin mounts what the agent is reached on first and reserves it: `/ws`,
where devices and clients both connect, and `/health` with `/api/v1/health`
beside it. An endpoint on one of those paths fails the start, as two endpoints
on one path do, rather than leaving the mux to decide.

`/ws` routes a connection by the mode it declares. A connection declaring none
is a client, served by the client server the plugin builds from the agent: the
API secret, the loopback bypass, the tag permissions, the scans and the reader
status, with `TokenVerifier` added for a paired build. A device declares
`server.ModeDevice`, which is served only when the build names the driver's
endpoint in `ServeMode`. An entry under `server.ModeClient` replaces the client
server the plugin would build. The client server lives as long as the plugin
rather than as long as a run, so a client stays connected across a stop and
start of the agent and receives again once it runs.

The allowlist of browser origins is the plugin's too, since it decides which
upgrades it admits. `AllowedOrigins` seeds it and the store persists under the
config directory; `Origins` supplies one loaded elsewhere.
`servers.CheckOrigin()` is the same decision for a handler mounted beside it,
and `servers.OriginPolicy()` the same for anything taking a
`server.OriginPolicy`. Both resolve per request, so they can be handed over
before the plugin has a store and follow an origin allowed while the agent runs.

The clients connected right now are reported through the plugin:
`servers.ClientCount()`, `servers.Clients()` and `servers.DisconnectClient(id)`
answer from whatever is under `server.ModeClient` when it is a
`*clientserver.Server`, and report nothing when it is not.
`servers.Events().Clients` follows the count and takes a subscriber before the
plugin activates, which is when a console is built. `servers.Events().Origins`
does the same for the allowlist, carrying the allowed and refused origins and
the session-wide bypass.

The listener is bound by a component the plugin registers, so it comes up once
the agent is serving and goes down before it. Give it `Certificates` and a
reissued certificate rebinds it on its own; leave it nil for one that never
changes underneath.

A build that registers no server plugin serves no HTTP and runs no client
server, which is what a program driving the readers directly wants. It still
gets every scan through `Agent.Events()`.

### The pairing and trust plugins

The pairing entries follow the gate, so rotating the PIN from the menu or from
the console relabels both. The trust entry is shown only while there is
something to install, and `Install` blocks on the operating system's password
prompt: the menu calls it off the dispatch goroutine, and a program calling it
directly should do the same. A `trustplugin.Plugin` with a nil `Manager` is
inert.

### The control center

The console is a set of server plugin endpoints, so it is served from the
agent's port and listed with the other addresses. It takes the gate and the
certificate manager directly, not the pairing and trust plugins, and it reads
the listener from the server plugin, which builds it when it activates. The
address it hands out is the listener's, the PIN it rotates is the gate's, and
the authority it installs is the certificate manager's, so a tray entry stays in
step with the same action taken from a page. `console.New` also connects to
`Events().Any`, so every change the agent reports redraws an open page.

`Quit` is what the console's quit control calls, since ending the program
belongs to whoever owns it. Under `-tags nowebui` no console is compiled in and
`Endpoints` is empty, so a program needs no build tag of its own.

## Naming your build

A build that keeps the agent's identity also keeps its configuration directory,
and two programs sharing that directory share their certificates and paired
devices. `Options.Info`, or `Config.Info` when building the agent directly,
replaces the identity for the whole tree:

```go
opts := agent.DefaultOptions()
opts.Info = buildinfo.Info{
	Name:        "gate-agent",
	DirName:     "gate-agent",
	DisplayName: "Gate Reader",
	Version:     "2.1.0",
}
```

That name follows through everywhere the agent presents itself: the
configuration directory, the log banner, the control center header, the tray
tooltip, the pairing pages and the iOS configuration profile, and the mDNS
service the devices look for. Fields left blank fall back to the agent's own, so
overriding only `DirName` is enough to stop two builds colliding on disk.

## Package layout

| Package | Contents |
|---|---|
| `agent` | The agent: its lifecycle, configuration, events, plugin registry and tag operations |
| `agent/serverplugin` | The listener plugin: `/ws`, the health checks, the client server, the origin allowlist |
| `agent/pairingplugin` | The pairing bootstrap listener and its tray entries |
| `agent/trustplugin` | The tray entry that installs the local certificate authority |
| `agent/console` | The control center: the privileged API, the embedded frontend, and the adapter onto the agent |
| `agent/tray` | The system tray |
| `nfc` | The reader supervisor, tag drivers, NDEF encoding and decoding |
| `nfc/pcsc` | The PC/SC hardware backend |
| `nfc/remotenfc` | Phones and WebNFC browsers: the device protocol, its WebSocket endpoint, the sessions and the tags behind them |
| `nfc/multimanager` | Several backends behind one `nfc.Manager` |
| `nfc/keyfile` | The JSON key file holding MIFARE Classic, DESFire and NTAG 424 DNA keys |
| `nfc/ntag424` | NTAG 424 DNA SDM/SUN verification and configuration |
| `nfc/ev1`, `nfc/ev2`, `nfc/lrp` | DESFire and NTAG 424 DNA authentication and secure messaging |
| `nfc/virtualnfc` | A software reader carrying virtual tags, with no hardware or wire protocol behind it |
| `nfc/nfctest` | In-memory tag emulators for testing without hardware |
| `server` | The bridge between tag sources and clients, origins, CORS, and the credential checks both endpoints are gated by |
| `server/clientserver` | The client WebSocket endpoint, and what it performs on the tag a request names |
| `server/listener` | One HTTP listener: a port, a mux of what was mounted on it, TLS and mDNS |
| `server/deviceid` | The identity a connection was admitted under, carried from the check to the handler |
| `server/netinfo` | The addresses this machine serves on |
| `server/wsconn` | Write-safe WebSocket wrapper shared by the servers and the device driver |
| `secure/pairing` | The paired-device gate: credential store, pairing server, admission check |
| `secure/tls` | Certificate provisioning, reissue and the local authority |
| `secure` | Restricting persisted files to the current user |
| `protocol` | The wire vocabulary both protocols share: the message envelope, the error taxonomy, NDEF input |
| `event` | The signals and properties the agent and the menus publish on |
| `traymenu` | Declarative tray menus, with no toolkit behind them |
| `traymenu/fynetray` | The real tray, on `fyne.io/systray` |
| `logbuf` | The log ring, and the named channels that write into it |
| `buildinfo`, `clipboard` | Build identity, and copying text to the system clipboard |
| `e2e` | Tests only: an agent wired as on this page, driven over its protocols |

Dependencies run in one direction. `agent/console` and `agent/tray` import
`agent`; neither is imported by it, and no package below `cmd` imports both.
Two properties follow:

- `agent` depends on no GUI toolkit. `fyne.io/systray` arrives only with
  `agent/tray`.
- `agent` depends on no NFC backend. `nfc/pcsc` arrives only where it is
  imported, which in the shipped binary is `cmd/davi-nfc-agent/main.go`.

Omitting the tray or the hardware backend therefore drops imports without
changing the agent.

## Running headless

Omitting the tray and the console leaves a service that reads cards and serves
the WebSocket API to browser clients.

```go
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/agent/serverplugin"
	"github.com/dotside-studios/davi-nfc-agent/nfc/pcsc"
	tlspkg "github.com/dotside-studios/davi-nfc-agent/secure/tls"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/listener"
)

func main() {
	opts := agent.DefaultOptions()
	opts.ConfigDir = "/var/lib/davi-nfc"
	opts.AllowedOrigins = "console.example.com"

	certs, err := tlspkg.Provision(opts.ConfigDir, false)
	if err != nil {
		log.Fatal(err)
	}

	rt, err := agent.Setup(opts, pcsc.NewManager())
	if err != nil {
		log.Fatal(err)
	}

	servers := &serverplugin.Plugin{
		Config:         listener.Config{CertFile: certs.CertFile, KeyFile: certs.KeyFile},
		Certificates:   certs.Manager,
		AllowedOrigins: server.ParseAllowedOrigins(opts.AllowedOrigins),
	}
	if err := rt.Agent.Plugins.Add(servers); err != nil {
		log.Fatal(err)
	}

	// An empty device path selects the first reader, and waits if none is
	// attached yet.
	if err := rt.Agent.Start(rt.DevicePath); err != nil {
		log.Fatal(err)
	}
	// Shutdown stops the agent and then closes the manager. Stop alone leaves
	// the manager open, since the agent can be started against it again.
	defer rt.Agent.Shutdown()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
}
```

This server plugin serves the client server on `/ws` and the two health
checks, with the root falling back to a plain-text banner. `ServeMode` names no
device endpoint, so no phone connects, and with no `TokenVerifier` a client
authenticates with the API secret. Leave the `Config` blank to serve plain HTTP.
`-tags nowebui` additionally removes the console from the binary.

## Building without a hardware backend

Passing only the remote manager keeps `nfc/pcsc` out of the build entirely. The
result requires no `libpcsclite` at build or run time and cross-compiles to any
target.

```go
devices := remotenfc.NewManager(remotenfc.DeviceTimeout)

rt, err := agent.Setup(opts, devices)
if err != nil {
	log.Fatal(err)
}

servers.ServeMode = map[string]http.Handler{
	server.ModeDevice: devices.Handler(remotenfc.ServerOptions{
		CheckOrigin:          servers.CheckOrigin(),
		AllowTagModification: rt.Agent.TagModificationAllowed,
		PublicKeyPin:         rt.Agent.PublicKeyPin,
	}),
}
```

This mounts the device endpoint bare; wrap it in `paired.Admit` as in
[A complete agent](#a-complete-agent) to admit only paired devices.

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./cmd/your-agent
```

Phones and WebNFC browsers connect over the [Device API](device-api.md) and
report the tags they scan, so such a build is complete for deployments where
every reader is a phone.

## Following the agent

`rt.Agent.Events()` is what the agent reports. Connect a handler to a signal and
it runs on every emission; the connection it returns removes it again.

| Signal | Carries |
|---|---|
| `State` | Each settled lifecycle transition |
| `Preferences` | The preferences after a change, whoever made it |
| `Servers` | The agent's configured port, after a listener has bound again |
| `Reader` | The reader's status: connected, and whether a card is on it |
| `Readers` | The readers that can be picked, when the set changes |
| `Tag` | Every scan the agent broadcasts |
| `Any` | The kind of every change above, except scans and reader status |

`State`, `Preferences`, `Servers` and `Readers` are `event.Property`: connecting
calls the handler with the current value before returning, so a subscriber
draws its first frame without reading the agent separately and cannot miss a
change in between. `Signal.Connect` on the same field connects without that
first call, for a subscriber that wants the next value rather than this one.
`Tag`, `Reader` and `Any` carry traffic and have no current value to report.

```go
conn := rt.Agent.Events().Preferences.Connect(func(p agent.Preferences) {
	log.Printf("reader is now in %s mode", p.Mode)
})
defer conn.Disconnect()
```

`Any` is for a surface that redraws rather than acts on the value, so it carries
an `agent.Change` naming what moved instead of the value itself. Scans and
reader status are left out of it: a page redrawing per card is not what a
subscriber to "something changed" is asking for. Subscribe to `Tag` and `Reader`
by name for those.

Handlers run on the goroutine that made the change, in the order they connected,
so they must not block. Work that may take time belongs on a channel of your
own; `Channel(buffer)` on a signal returns one with its cancel function.
Connecting and disconnecting is safe at any time, including from inside a
handler and while the agent runs.

### Observing scans

`Events().Tag` carries every scan the agent broadcasts, in the order the
connected clients receive it.

```go
rt.Agent.Events().Tag.Connect(func(data nfc.NFCData) {
	if data.Card == nil {
		return
	}
	log.Printf("scanned %s (%s)", data.Card.UID, data.Card.Type)
})
```

A subscriber observes rather than intercepts. The scan reaches every connected
client regardless, and what the handler does changes nothing.

### Acting on a tag

The agent answers for every tag it can reach, on a reader it polls or on a
device that reported one, so a plugin acts on a card without reaching for the
readers behind it:

```go
device, uid, ok := rt.Agent.TagOn("")
if ok {
	_, err := rt.Agent.WriteTag(ctx, device, uid, msg, false, "check-in-42")
	if err != nil {
		log.Print(err)
	}
}
```

`TagOn`, `DevicesHoldingTags`, `WriteTag`, `LockTag`, `TransceiveTag` and
`TagCapabilities` are `nfc.TagHolder`, the same interface the client server is
given, so what a plugin can do to a tag is what a client can. An empty device
means whatever is holding a tag; naming one that is not is refused, as is any
operation while the agent is not serving.

## Driving the readers directly

A program that needs no WebSocket API at all can skip the agent and operate the
readers itself. `nfc.Supervisor` opens every reader the manager offers, so a
second one plugged in is picked up rather than ignored, and each scan names the
reader it was read on.

```go
package main

import (
	"log"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/pcsc"
)

func main() {
	readers, err := nfc.NewSupervisor(pcsc.NewManager(), 5*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	defer readers.Stop()

	// Read-only also puts the write path out of reach, including Lock, which
	// cannot be undone. It applies to every reader, including one opened later.
	readers.SetMode(nfc.ModeReadOnly)

	scans, stop := readers.Scans().Channel(16)
	defer stop()

	if err := readers.Start(); err != nil {
		log.Fatal(err)
	}

	for data := range scans {
		if data.Err != nil {
			log.Printf("scan error on %s: %v", data.Device, data.Err)
			continue
		}
		if data.Card != nil {
			log.Printf("%s scanned %s (%s)", data.Device, data.Card.UID, data.Card.Type)
		}
	}
}
```

An operation names the reader it applies to, which `data.Device` carries:

```go
result, err := readers.WriteMessage(ctx, data.Device, msg, nfc.WriteOptions{ExpectUID: data.Card.UID})
```

Naming no reader means the only one there is, and is refused once there is more
than one rather than picking for you.

Both loops are testable without hardware: `nfc` exports `NewMockManager` and
`NewMockTag`, and `nfc/nfctest` provides emulators. Construct cards with
`nfc.NewCard`: a `nfc.Card` assembled field by field has no tag behind it and
cannot be read from.

## Adding a reader backend

`nfc.Manager`, `nfc.Device` and `nfc.Tag` are interfaces, and `multimanager`
runs several implementations alongside one another, which is how hardware
readers and phones coexist. Implementing them for a reader of your own is
covered in [Extending NFC support](extending-nfc-support.md).

## Build tags

| Build | Effect |
|---|---|
| *(default)* | Everything; PC/SC through goscard, which uses no cgo |
| `-tags nowebui` | No control center: no `/control` routes, no privileged API, no tray entry, no embedded frontend |
| `-tags cgopcsc` | PC/SC through `ebfe/scard` instead, which requires cgo and `libpcsclite` |
| `CGO_ENABLED=0` | Builds everything the default build does except `agent/tray` on macOS |

`fyne.io/systray` talks to Cocoa, so `agent/tray`, and any command that imports
it, needs cgo on macOS. Every other package builds without cgo for every
supported target, so a headless build is portable.

## Related documentation

- [Client API](client-api.md) and [Device API](device-api.md): the WebSocket
  protocols
- [Configuration](configuration.md): flags, environment variables and files
- [Security](security.md): the security model
- [Control Center](control-center.md): how the console attaches to the agent
- [Extending NFC support](extending-nfc-support.md): adding a reader or tag type
