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

This is the shipped binary with its flag set replaced by fixed options: TLS,
pairing, the WebSocket API, the control center and the tray.

```go
package main

import (
	"io"
	"log"
	"net/http"
	"os"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/agent/console"
	"github.com/dotside-studios/davi-nfc-agent/agent/tray"
	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/multimanager"
	"github.com/dotside-studios/davi-nfc-agent/nfc/pcsc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/listener"
)

func main() {
	opts := agent.DefaultOptions()

	// The console reads its log from this ring. Installing it as the log sink
	// before Setup is what captures the startup sequence; the agent never
	// touches the process logger itself.
	opts.Logs = logbuf.New(logbuf.DefaultCapacity)
	log.SetOutput(io.MultiWriter(os.Stderr, opts.Logs))

	// The driver serving phones. What it scans and what its devices hold reach
	// the agent through the manager below; its endpoint is mounted with the
	// server plugin, so the agent names no device protocol itself.
	devices := remotenfc.NewManager(remotenfc.DeviceTimeout)

	// Hardware readers and phones behind one manager, which the agent opens
	// its reader from.
	manager := multimanager.NewMultiManager(
		multimanager.ManagerEntry{Name: nfc.ManagerTypeHardware, Manager: pcsc.NewManager()},
		multimanager.ManagerEntry{Name: nfc.ManagerTypeSmartphone, Manager: devices},
	)

	rt, err := agent.Setup(opts, manager)
	if err != nil {
		log.Fatal(err)
	}

	// The tray entry that installs the local authority, so browsers on this
	// machine accept the agent.
	trust := &agent.TrustPlugin{Manager: rt.Certificates}

	// The listener and everything on it. Setup builds no listener; the program
	// decides what this agent serves. Setup resolved which certificate to
	// serve; Certificates is what rebinds the listener when it is reissued.
	servers := &agent.ServerPlugin{
		Config:       listener.Config{CertFile: rt.CertFile, KeyFile: rt.KeyFile},
		Certificates: rt.Certificates,

		// The driver's wire behind the agent's policy: the agent decides who is
		// admitted and what is allowed, the driver decides what a device says.
		ServeMode: map[string]http.Handler{
			server.ModeDevice: devices.Handler(remotenfc.ServerOptions{
				Authenticate:         rt.Agent.DeviceAuth.Check,
				CheckOrigin:          rt.Agent.CheckOrigin(),
				AllowTagModification: rt.Agent.TagModificationAllowed,
				PublicKeyPin:         rt.Agent.PublicKeyPin,
			}),
		},
	}

	// Pairing: a listener of its own, and the tray entries that hand out its
	// address and PIN. The agent holds no pairing server, so it is built here
	// and passed to the console. It hands the authority to a device that pairs.
	pairing := agent.NewPairingPlugin(rt.Agent, opts.BootstrapPort, rt.Certificates)

	app := tray.New(rt)

	// The control center, served from the same listener and listed with the
	// other addresses. A -tags nowebui build has none, and Endpoints is empty,
	// so this program needs no build tag of its own.
	c := console.New(console.Config{
		Agent:   rt.Agent,
		Logs:    rt.Logs,
		Servers: servers,
		Pairing: pairing,
		Trust:   trust,
		Quit:    app.Quit,
	})
	servers.Add(c.Endpoints()...)

	// The server goes on first: it publishes the listener the rest mount on,
	// and plugins are activated in the order they were added, which is also the
	// order their entries appear in the tray.
	if err := rt.Agent.Plugins.Add(servers, pairing, trust); err != nil {
		log.Fatal(err)
	}

	app.Run()
}
```

`agent.Setup` performs the work the flags imply: it resolves the config
directory, loads or generates the TLS certificate and the API secret, and reads
the paired devices and the origin allowlist. It returns an `*agent.Runtime`
holding the configured agent, the certificate manager, the log ring and the
reader path to open.

The listener, the pairing server and the control center are plugins the program
registers, not part of `Setup`. An agent with none of them drives the reader and
serves no HTTP, which is a valid build. Binding happens at start, so routes can
be declared before the port exists. See [Plugins](#plugins).

The certificate is the `*tls.Manager` that `Setup` returns as
`rt.Certificates`, alongside `rt.CertFile` and `rt.KeyFile`, the pair a listener
should serve. Each plugin takes the narrow part it needs: `ServerPlugin.Config`
the files and `ServerPlugin.Certificates` the reissue signal, `NewPairingPlugin`
the authority a pairing device is given. A build serving a certificate
provisioned elsewhere passes that pair and leaves `Certificates` nil, there
being nothing to reissue.

`agent.TrustPlugin` wraps the same manager for the one job the others do not do:
the tray entry that installs the local authority, hidden once there is nothing
left to install. `console.Config.Trust` takes it so the same install can be
started from a page. Leave `Manager` nil and the plugin is inert.

Pairing is `agent.PairingPlugin`, which runs the pairing server and owns the
menu entries that hand out its address and PIN.
`agent.NewPairingPlugin(a, port, trust)` takes the device registry, the key pin
and the name from the agent, so nothing already given to `Setup` is repeated.
Omit the plugin and the build pairs no devices: the console is handed `nil` and
reports pairing as disabled. For the listener without the menu entries, register
the component directly with `agent.PairingFor(a, port, ca)` and `ctx.Use` or an
`agent.Endpoint`.

The NFC backend is `Setup`'s second argument, which is why every package beneath
`cmd` builds without one. A manager reports what its devices scan through
`nfc.TagReporter` and answers for the tags they hold through `nfc.TagHolder`,
both optional, so the agent subscribes to the manager it was given rather than
being handed the driver. `multimanager` implements both by fanning its children
in. Serving those devices is not the manager's business: the driver's endpoint
goes on the server plugin as `ServeMode[server.ModeDevice]`, built from what the
agent answers, and a build that mounts none serves its own readers alone.

Flags and the standard logger belong to the program. Registering flags writes to
`flag.CommandLine`, which would collide with the flags of anything embedding the
agent, so the shipped command adds its own flag set on top of `Options` in
[`cmd/davi-nfc-agent/flags.go`](../cmd/davi-nfc-agent/flags.go) and installs the
log ring itself, as above.

`agent.New` is the alternative to `Setup`, for a program with its own
configuration: it takes an `agent.Config` and builds the agent from values you
already hold, leaving the certificate, secret and store loading to you. Either
way the configuration is fixed once the agent exists and is read back through
methods, so nothing can rebind the port or withdraw the pairing requirement
behind the running servers. The preferences that may legitimately change while
running have methods of their own: `SetReaderMode`, `SetCardTypeFilter`, `SetPinnedDevice`,
`SetDevicePort`, `SetRequirePairedDevice` and `SetReaderFeedback`. Nothing
persists them: a change lasts as long as the agent runs, and what it starts with
comes from `agent.Config`.

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

A build's plugins are what it imports, fixed at compile time, so one left out
takes its dependencies with it, the same way `nfc/pcsc` and the tray do.

The context carries what a plugin needs to wire itself in:

| | |
|---|---|
| `ctx.Agent` | The agent, for its configuration and for what it can be told to do |
| `ctx.Events` | What the agent reports: see [Following the agent](#following-the-agent) |
| `ctx.Use(c)` | Registers an `agent.Component`, started and stopped with the agent |
| `ctx.Systray` | The menu the plugin's entries go on |
| `ctx.Serve(srv)` | Publishes the listener the agent serves from |
| `ctx.Mount(pattern, h)` | Adds a route to it |
| `ctx.Logger()`, `ctx.Info()`, `ctx.ConfigDir()`, `ctx.Logs()` | The agent's log, identity, config directory and log ring |

`ctx.Systray` is the top level of the tray's own menu, so a plugin's entry looks
no different from one the tray declared itself. Entries land where the tray
activated the plugins, since a menu item always goes to the end of its parent. A
plugin with more than one entry groups them under a submenu of its own, with
`ctx.Systray.Section("Backups")`.

`ctx.Systray` is never nil. A headless agent hands over a menu that draws
nothing, so a plugin can add its entries without checking for a tray.

A plugin has no `Deactivate`. Anything with a lifetime is a `Component`, which
the agent starts once the reader and the servers are up and stops before taking
them down again.

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
| `agent.ServerPlugin` | The listener, everything mounted on it, and the tray's **Server URLs** submenu |
| `agent.PairingPlugin` | The pairing server on a port of its own, and the entries showing its address and PIN |
| `agent.TrustPlugin` | The entry that installs the local certificate authority |

```go
trust := &agent.TrustPlugin{Manager: rt.Certificates}
servers := &agent.ServerPlugin{
	Config:       listener.Config{CertFile: rt.CertFile, KeyFile: rt.KeyFile},
	Certificates: rt.Certificates,
}
pairing := agent.NewPairingPlugin(rt.Agent, 9472, rt.Certificates)

rt.Agent.Plugins.Add(servers, pairing, trust)
```

The server plugin goes on first. It publishes the listener with `ctx.Serve`,
which is what backs `ctx.Mount` for every plugin registered after it, and an
`agent.Mounter` is one method wide so the agent never names a server type.

What goes on that listener is an `agent.Endpoint`: a route, something with a
lifetime, a menu entry, or any combination.

```go
servers.Add(agent.Endpoint{Name: "webhooks", Pattern: "/hooks/", Handler: hooks})
servers.Add(agent.Endpoint{Name: "queue drain", Component: drain})
```

The plugin mounts what the agent is reached on first and reserves it: `/ws`,
where devices and clients both connect, and `/health` with `/api/v1/health`
beside it. An endpoint on one of those paths fails the start, as two endpoints
on one path do, rather than leaving the mux to decide.

The plugin runs the client server behind `/ws` and routes a connection by the
mode it declares. `ServeMode` replaces either handler:

```go
servers.ServeMode = map[string]http.Handler{
	server.ModeDevice: devices.Handler(remotenfc.ServerOptions{ ... }),
}
```

A build that registers no server plugin serves no HTTP and runs no client
server, which is what a program driving the readers directly wants. It still
gets every scan through `Agent.Events()`.

The listener is bound by a component the plugin registers, so it comes up once
the agent is serving and goes down before it. Give it `Certificates` and a
reissued certificate rebinds it on its own; leave it nil for one that never
changes underneath.

`PairingPlugin` and `TrustPlugin` tolerate being nil, so a build that registers
neither hands `nil` to the console and it reports both as unavailable. The
per-field details are on the types themselves: `go doc agent.ServerPlugin`,
`agent.Endpoint`, `agent.NewPairingPlugin`, `agent.TrustPlugin`.

The pairing entries follow the server, so rotating the PIN from the menu or from
the console relabels both. The trust entry is shown only while there is
something to install and hides once there is not, and `Install` blocks on the
operating system's password prompt: the menu calls it off the dispatch
goroutine, and a program calling it directly should do the same.

### The control center

The console is two endpoints of the server plugin, so it is served from the
agent's port and listed with the other addresses:

```go
c := console.New(console.Config{
	Agent:   rt.Agent,
	Logs:    rt.Logs,
	Servers: servers,
	Pairing: pairing,
	Trust:   trust,
	Quit:    app.Quit,
})
servers.Add(c.Endpoints()...)
```

The three plugins are what the console reports on and acts through: the address
it hands out is the listener's, the PIN it rotates is the pairing server's, and
the authority it installs is the trust plugin's, so a tray entry stays in step
with the same action taken from a page. `console.New` also connects to
`Events().Any`, so every change the agent reports redraws an open page. Under
`-tags nowebui` there is no console compiled in and `Endpoints` is empty, so a
program needs no build tag of its own.

`Quit` is what the console's quit control calls, since ending the program
belongs to whoever owns it. Everything else the console does goes to the agent,
and the tray redraws from the agent's events rather than being told.

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
| `agent` | The agent, and the plugins the shipped build registers: the listener, pairing and the certificate |
| `agent/console` | The control center: the privileged API, the embedded frontend, and the adapter onto the agent |
| `agent/tray` | The system tray |
| `nfc` | The reader supervisor, tag drivers, NDEF encoding and decoding |
| `nfc/pcsc` | The PC/SC hardware backend |
| `nfc/remotenfc` | Phones and WebNFC browsers: the device protocol, its WebSocket endpoint, the sessions and the tags behind them |
| `nfc/multimanager` | Several backends behind one `nfc.Manager` |
| `server` | The bridge between tag sources and clients, and the device credential check |
| `server/clientserver` | The client WebSocket endpoint, and what it performs on the tag a request names |
| `server/listener` | One HTTP listener: a port, a mux of what was mounted on it, TLS and mDNS |
| `server/wsconn` | Write-safe WebSocket wrapper shared by the servers and the device driver |
| `protocol` | The wire vocabulary both protocols share: the message envelope, the error taxonomy, NDEF input |
| `traymenu` | Declarative tray menus, with no toolkit behind them |
| `event` | The signal the agent and the menus publish their callbacks on |
| `clipboard` | Copying text to the system clipboard |
| `traymenu/fynetray` | The real tray, on `fyne.io/systray` |
| `tls`, `logbuf` | Certificates, the log ring |
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
the WebSocket API. `agent.DefaultOptions` supplies the values the flags would
otherwise provide.

```go
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/nfc/pcsc"
	"github.com/dotside-studios/davi-nfc-agent/server/listener"
)

func main() {
	opts := agent.DefaultOptions()
	opts.ConfigDir = "/var/lib/davi-nfc"
	opts.AllowedOrigins = "console.example.com"

	opts.DevicePort = 9470

	rt, err := agent.Setup(opts, pcsc.NewManager())
	if err != nil {
		log.Fatal(err)
	}

	// The listener, with nothing on it but the agent's own routes. Leave it
	// out for a service that reads cards and serves no HTTP. Setup resolved
	// the certificate; blank leaves the listener serving plain HTTP.
	servers := &agent.ServerPlugin{
		Config:       listener.Config{CertFile: rt.CertFile, KeyFile: rt.KeyFile},
		Certificates: rt.Certificates,

		// The driver's wire behind the agent's policy: the agent decides who is
		// admitted and what is allowed, the driver decides what a device says.
		ServeMode: map[string]http.Handler{
			server.ModeDevice: devices.Handler(remotenfc.ServerOptions{
				Authenticate:         rt.Agent.DeviceAuth.Check,
				CheckOrigin:          rt.Agent.CheckOrigin(),
				AllowTagModification: rt.Agent.TagModificationAllowed,
				PublicKeyPin:         rt.Agent.PublicKeyPin,
			}),
		},
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

A server plugin with no endpoints serves the agent's own routes: `/ws` and the
two health checks, with the root falling back to a plain-text banner. Building
no pairing server means no device can pair, so a phone authenticates with the
API secret instead of a credential of its own. `-tags nowebui` additionally
removes the console from the binary.

## Building without a hardware backend

Passing only the remote manager keeps `nfc/pcsc` out of the build entirely. The
result requires no `libpcsclite` at build or run time and cross-compiles to any
target.

```go
manager := remotenfc.NewManager(remotenfc.DeviceTimeout)

rt, err := agent.Setup(agent.DefaultOptions(), manager)
if err != nil {
	log.Fatal(err)
}
rt.Agent.Plugins.Add(&agent.ServerPlugin{
	Config:       listener.Config{CertFile: rt.CertFile, KeyFile: rt.KeyFile},
	Certificates: rt.Certificates,
})
```

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./cmd/your-agent
```

Phones and WebNFC browsers connect over the [Device API](api.md#device-api) and
report the tags they scan, so such a build is complete for deployments where
every reader is a phone.

## Following the agent

`rt.Agent.Events()` is what the agent reports. Connect a handler to a signal and
it runs on every emission; the connection it returns removes it again.

| Signal | Carries |
|---|---|
| `State` | Each settled lifecycle transition |
| `Preferences` | The preferences after a change, whoever made it |
| `Clients` | The number of connected clients |
| `Servers` | The port the listeners are bound on, after a restart |
| `Reader` | The reader's status: connected, and whether a card is on it |
| `Readers` | The readers that can be picked, when the set changes |
| `Devices` | The paired devices, after a pairing or a revocation |
| `Origins` | The allowlist, after an edit |
| `Blocked` | Each origin refused a connection |
| `Tag` | Every scan the agent broadcasts |
| `Any` | The kind of every change above, except scans and reader status |

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
own. Connecting and disconnecting is safe at any time, including from inside a
handler and while the agent runs.

### Observing scans

`Events().Tag` carries every scan the agent broadcasts, in the order the
connected clients receive it.

```go
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/pcsc"
)

func main() {
	rt, err := agent.Setup(agent.DefaultOptions(), pcsc.NewManager())
	if err != nil {
		log.Fatal(err)
	}

	rt.Agent.Events().Tag.Connect(func(data nfc.NFCData) {
		if data.Card == nil {
			return
		}
		log.Printf("scanned %s (%s)", data.Card.UID, data.Card.Type)
	})

	if err := rt.Agent.Start(rt.DevicePath); err != nil {
		log.Fatal(err)
	}
	defer rt.Agent.Shutdown()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
}
```

A subscriber observes rather than intercepts. The scan reaches every connected
client regardless, and what the handler returns changes nothing.

### Acting on a tag

The agent answers for every tag it can reach, on a reader it polls or on a
device that reported one, so a plugin acts on a card without reaching for the
readers behind it:

```go
device, uid, ok := rt.Agent.TagOn("")
if ok {
	_, err := rt.Agent.WriteTag(device, uid, msg, false, "check-in-42")
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
result, err := readers.WriteMessage(data.Device, msg, nfc.WriteOptions{ExpectUID: data.Card.UID})
```

Naming no reader means the only one there is, and is refused once there is more
than one rather than picking for you.

Both loops are testable without hardware: `nfc` exports `NewMockManager` and
`NewMockTag`, and `nfc/nfctest` provides an emulator. Construct cards with
`nfc.NewCard`: a `nfc.Card` assembled field by field has no tag behind it and
cannot be read from.

## Adding a reader backend

`nfc.Manager`, `nfc.Device` and `nfc.Tag` are interfaces, and `multimanager`
runs several implementations alongside one another, which is how hardware
readers and phones coexist today. Implementing them for a reader of your own is
covered in [Extending NFC support](extending-nfc-support.md).

## Build tags

| Build | Effect |
|---|---|
| *(default)* | Everything; PC/SC through goscard, without cgo |
| `-tags nowebui` | No control center: no `/control` routes, no privileged API, no tray entry, no embedded frontend |
| `-tags cgopcsc` | PC/SC through `ebfe/scard` instead, which requires cgo and `libpcsclite` |
| `CGO_ENABLED=0` | Already the default; nothing requires cgo except the tray on macOS |

`fyne.io/systray` talks to Cocoa, so `agent/tray`, and any command that imports
it, needs cgo on macOS. Every other package builds without cgo for every
supported target, so a headless build is portable.

## Related documentation

- [API reference](api.md): the device and client WebSocket protocols
- [Control Center](control-center.md): how the console attaches to the agent
- [Extending NFC support](extending-nfc-support.md): adding a reader or tag type
