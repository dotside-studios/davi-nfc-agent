# Custom Builds

The agent is a Go module, and the binary in `cmd/davi-nfc-agent` is an ordinary
program built from packages this repository exports. Changing what it does is
therefore a `main.go` of your own rather than a fork.

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
)

func main() {
	opts := agent.DefaultOptions()

	// The console reads its log from this ring. Installing it as the log sink
	// before Setup is what captures the startup sequence; the agent never
	// touches the process logger itself.
	opts.Logs = logbuf.New(logbuf.DefaultCapacity)
	log.SetOutput(io.MultiWriter(os.Stderr, opts.Logs))

	// The driver serving phones. Built here because this is what decides the
	// agent should serve them: it is handed over as an interface, a channel of
	// scans and a handler builder, so the agent names no device protocol.
	devices := remotenfc.NewManager(remotenfc.DeviceTimeout)
	opts.RemoteOps = devices
	opts.RemoteScans = devices.Data()
	opts.DeviceEndpoint = func(o agent.DeviceEndpointOptions) http.Handler {
		return devices.Handler(remotenfc.ServerOptions{
			Authenticate:         o.Authenticate,
			CheckOrigin:          o.CheckOrigin,
			AllowTagModification: o.AllowTagModification,
			PublicKeyPin:         o.PublicKeyPin,
		})
	}

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

	// The certificate, and the tray entry that makes browsers accept it.
	// Whatever needs a certificate is given this rather than reaching for one:
	// the agent holds none.
	trust := &agent.TrustPlugin{Manager: rt.Certificates}

	// The listener and everything on it. Setup does not build one: what this
	// agent serves is the program's decision.
	servers := &agent.ServerPlugin{Trust: trust}

	// Pairing: a listener of its own, and the tray entries that hand out its
	// address and PIN. The agent does not hold one, so this is where it is
	// built and where it is handed to the console.
	pairing := agent.NewPairingPlugin(rt.Agent, opts.BootstrapPort, trust)

	// The control center, served from the same listener and listed with the
	// other addresses. A -tags nowebui build has none, and Endpoints is empty,
	// so this program needs no build tag of its own.
	c := console.New(console.Config{
		Agent:    rt.Agent,
		Settings: rt.Settings,
		Logs:     rt.Logs,
		Servers:  servers,
		Pairing:  pairing,
		Trust:    trust,
	})
	servers.Add(c.Endpoints()...)

	// The server goes on first: it publishes the listener the rest mount on,
	// and plugins are activated in the order they were added, which is also the
	// order their entries appear in the tray.
	if err := rt.Agent.Plugins.Add(servers, pairing, trust); err != nil {
		log.Fatal(err)
	}

	app := tray.New(rt)
	app.AttachConsole(c)
	app.Run()
}
```

`agent.Setup` performs the work the flags imply: it resolves the config
directory, loads or generates the TLS certificate and the API secret, and reads
the paired devices and the origin allowlist. It returns
an `*agent.Runtime` holding the configured agent alongside the stores a front
end needs.

It builds neither the listener nor the pairing server nor the control center.
Each is a plugin the program registers, and an agent with none of them drives
the reader and serves no HTTP, which is a build rather than a broken one.
Nothing binds until the agent starts, so a route is declared before the port it
will be served from is bound. See [Plugins](#plugins).

The certificate is `agent.TrustPlugin`, wrapping the `*tls.Manager` that
`Setup` returns as `rt.Certificates`. It holds the files a listener serves, the
authority a pairing device is given, and the tray entry that installs that
authority so browsers on this machine accept the agent. Whatever needs a
certificate takes this plugin: `ServerPlugin.Trust`, `NewPairingPlugin` and
`console.Config.Trust` all read it, so the certificate is configured once. A
build serving a certificate it does not manage leaves `Manager` nil, and every
method answers as a build with no certificate should: no files, no authority,
and no entry offering to install one.

Pairing is `agent.PairingPlugin`, which runs the pairing server and owns the
menu entries that hand out its address and PIN.
`agent.NewPairingPlugin(a, port, trust)` takes the rest from the agent: the
device registry, the key pin and the name, so nothing already given to `Setup`
is repeated. Register none and the build pairs no devices; the console is handed
`nil` and reports pairing as disabled. A build wanting the listener without the
menu registers the component on its own instead, with
`agent.PairingFor(a, port, ca)` and `ctx.Use` or an `agent.Endpoint`.

It does not choose an NFC backend. That is the second argument, and passing it
in is what allows every package beneath `cmd` to build without one.

Nor does it reach into that backend. Serving phones takes three values the
caller supplies from a driver it built: `RemoteOps` to route operations,
`RemoteScans` to receive what they scan, and `DeviceEndpoint` to build their
handler. Supply none and the agent serves its own reader and nothing else. The
agent names no device protocol, and cannot find one you did not give it.

Nor does it define flags or redirect the standard logger. Both belong to the
program: registering flags writes to `flag.CommandLine`, which would collide
with the flags of anything embedding the agent. The shipped command adds its
own flag set on top of `Options` in
[`cmd/davi-nfc-agent/flags.go`](../cmd/davi-nfc-agent/flags.go), and installs
the log ring itself, as above.

`Setup` is the convenient path, not the only one. It reads and writes a config
directory, which a program with its own configuration may not want; `agent.New`
takes an `agent.Config` and builds the agent from values you already hold,
leaving the certificate, secret and store loading to you. Either way the
configuration is fixed once the agent exists: it is read back through methods,
so nothing can rebind the port or withdraw the pairing requirement behind the
running servers. The preferences that may legitimately change while running have
methods of their own: `SetReaderMode`, `SetCardTypeFilter`, `SetPinnedDevice`,
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

Nothing is loaded at run time and nothing is discovered. Which plugins a build
has is decided by what it imports, so one left out takes its dependencies with
it, the same way `nfc/pcsc` and the tray do.

The context carries what a plugin needs to wire itself in:

| | |
|---|---|
| `ctx.Agent` | The agent, for its configuration and for hooks such as `OnTag` and `OnStateChange` |
| `ctx.Use(c)` | Registers an `agent.Component`, started and stopped with the agent |
| `ctx.Systray` | The menu the plugin's entries go on |
| `ctx.Serve(srv)` | Publishes the listener the agent serves from |
| `ctx.Mount(pattern, h)` | Adds a route to it |
| `ctx.Logger()`, `ctx.Info()`, `ctx.ConfigDir()`, `ctx.Settings()`, `ctx.Logs()` | The agent's log, identity, config directory, preference store and log ring |

`ctx.Systray` is the top level of the tray's own menu, so a plugin's entry is
not marked out from one the tray declared itself: the shipped tray is one
composition of a menu, and a custom build composes its own. Entries land where
the tray activated the plugins, since a menu item always goes to the end of its
parent. A plugin with more than one entry groups them under a submenu of its
own, with `ctx.Systray.Section("Backups")`.

`ctx.Systray` is never nil. A headless agent hands over a menu that draws
nothing, so a plugin adds its entries without asking whether anyone is looking.

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

### The server plugin

`agent.ServerPlugin` owns the `*unifiedserver.Server` and mounts everything on
it: the agent's own routes first, then what is listed here. An endpoint is a
route, something with a lifetime, a menu entry, or any combination:

```go
servers := &agent.ServerPlugin{Endpoints: []agent.Endpoint{
	{Name: "metrics", Pattern: "/metrics", Handler: metrics},
	{Name: "queue drain", Component: drain},
}}
servers.Add(agent.Endpoint{Name: "webhooks", Pattern: "/hooks/", Handler: hooks})

rt.Agent.Plugins.Add(servers)
```

The plugin also owns the tray's **Server URLs** submenu, since what is served
from a port is what the thing holding the port knows: the device and client
addresses, the API secret a client presents to them, and a line per endpoint
that asks for one.

```go
{
	Name:    "control center",
	Pattern: "/",
	Handler: assets,
	Menu: func(menu traymenu.Container, url string) {
		menu.Add("Control Center: "+url, traymenu.Disabled())
		menu.Add("  Copy Control Center URL", traymenu.OnClick(func() { copy(url) }))
	},
}
```

An endpoint is listed only if it sets `Menu`: a route nobody opens by hand is
noise beside the addresses worth copying. A plugin with an `Activate` of its own
can mount a route with `ctx.Mount` instead.

Registered with no `Config`, it serves on the port and name the agent was set up
with, and on the certificate `Trust` holds. Set `Config` for a listener that
differs from them, which is where a certificate provisioned outside the agent
goes, or `Server` to hand over one built elsewhere:

```go
&agent.ServerPlugin{Config: unifiedserver.Config{Port: 9480, CertFile: cert, KeyFile: key}}
```

`agent.Routes` is what the agent serves of its own: `/ws`, where devices and
clients both connect, and `/health` with `/api/v1/health` beside it. The agent
holds no listener and mounts nothing itself; it hands those over and whatever
serves it puts them on, ahead of anything of its own. An endpoint on one of
their paths fails the start, as two endpoints on one path do, rather than
leaving the mux to decide.

`ctx.Serve` is how a plugin says it is what the agent is served from, which is
what backs `ctx.Mount` for the plugins registered after it. It takes an
`agent.Mounter`, one method wide, so the agent never names a server type.

The listener is bound by a component the plugin registers, so it comes up once
the agent is serving and goes down before it. It watches `Trust` for a reissued
certificate and calls `Rebind`, which stops and starts the listener so the new
one is served. Nothing else has to: installing a certificate authority or
reissuing a certificate reports itself, and the listener follows. Set
`Certificates` to watch something other than `Trust`, and `Rebind` is there for
a program that has some other reason to bind again.

### The control center

The console is two endpoints of the server plugin, so it is served from the
agent's port and listed with the other addresses:

```go
c := console.New(console.Config{
	Agent:    rt.Agent,
	Settings: rt.Settings,
	Logs:     rt.Logs,
	Servers:  servers,
	Pairing:  pairing,
	Trust:    trust,
})
servers.Add(c.Endpoints()...)
```

The three plugins are what the console reports on and acts through: the address
it hands out is the listener's, the PIN it rotates is the pairing server's, and
the authority it installs is the trust plugin's, so the tray entry offering the
same install follows one done from a page. `console.New` also follows what
redraws an open page: an origin allowed, a device revoked, a client connecting,
a listener rebound. Under `-tags nowebui` there is no console
compiled in and `Endpoints` is empty, so a program needs no build tag of its own.

`tray.App.AttachConsole` is the one thing left to the program. It runs both
ways: the tray acts through the console, and the console acts through the tray,
so a device switched in the browser moves the tray's menu too.

### The pairing plugin

`agent.PairingPlugin` wraps the pairing server, registers it as a component, and
owns the entries that show its address and PIN.

```go
pairing := agent.NewPairingPlugin(rt.Agent, 9472, trust)
rt.Agent.Plugins.Add(pairing)
```

Its entries go under a `Pairing` submenu of their own, beside the tray's own
top-level entries, and the labels follow the server: rotating the PIN from the
menu or from the control center relabels both. `Port`, `PIN` and `RotatePIN`
tolerate a nil plugin, so a build that registers none hands `nil` to the console
and everything reports pairing as disabled.

### The trust plugin

`agent.TrustPlugin` is the smallest of the three: it holds the certificate the
others are configured from, and adds the entry that installs the local authority
behind it.

```go
trust := &agent.TrustPlugin{Manager: rt.Certificates}
rt.Agent.Plugins.Add(trust)
```

The entry is offered only while there is something to install and hides itself
once there is not, whether the install came from the menu or from the control
center. Installing reissues the certificate, which the listener follows on its
own. `Install` blocks on the operating system's password prompt; the menu calls
it off the dispatch goroutine, and a program calling it directly should do the
same.

## Naming your build

A build that keeps the agent's identity also keeps its configuration directory,
and two programs sharing that directory share their certificates and paired
devices. `Options.Info` — or `Config.Info` when building the agent directly —
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
| `agent/console` | The control center, adapting the agent to `webui.Host` |
| `agent/tray` | The system tray |
| `nfc` | Readers, tag drivers, NDEF encoding and decoding |
| `nfc/pcsc` | The PC/SC hardware backend |
| `nfc/remotenfc` | Phones and WebNFC browsers: the device protocol, its WebSocket endpoint, the sessions and the tags behind them |
| `nfc/multimanager` | Several backends behind one `nfc.Manager` |
| `server` | The bridge between tag sources and clients, and the device credential check |
| `server/clientserver` | The client WebSocket endpoint |
| `server/tagrouter` | Picks the reader or a device for each client request |
| `server/unifiedserver` | One HTTP listener: a port, a mux of what was mounted on it, TLS and mDNS |
| `protocol` | The wire vocabulary both protocols share: the message envelope, the error taxonomy, NDEF input |
| `traymenu` | Declarative tray menus, with no toolkit behind them |
| `clipboard` | Copying text to the system clipboard |
| `traymenu/fynetray` | The real tray, on `fyne.io/systray` |
| `tls`, `logbuf` | Certificates, the log ring |
| `e2e` | Tests only: an agent wired as on this page, driven over its protocols |

Dependencies run in one direction. `agent/console` and `agent/tray` import
`agent`; neither is imported by it, and no package below `cmd` imports both.
Two properties follow, and both matter when deciding what to include:

- `agent` depends on no GUI toolkit. `fyne.io/systray` arrives only with
  `agent/tray`.
- `agent` depends on no NFC backend. `nfc/pcsc` arrives only where it is
  imported, which in the shipped binary is `cmd/davi-nfc-agent/main.go`.

A build that omits the tray or the hardware backend is therefore the same agent
with fewer imports, not a reduced version of it.

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
	// out for a service that reads cards and serves no HTTP. Trust holds the
	// certificate Setup generated; without it the listener serves plain HTTP.
	trust := &agent.TrustPlugin{Manager: rt.Certificates}
	if err := rt.Agent.Plugins.Add(&agent.ServerPlugin{Trust: trust}, trust); err != nil {
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
no pairing server leaves a build that pairs no devices, so a phone reaches it
through the API secret rather than a credential of its own. `-tags nowebui`
additionally removes the console from the binary.

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
trust := &agent.TrustPlugin{Manager: rt.Certificates}
rt.Agent.Plugins.Add(&agent.ServerPlugin{Trust: trust}, trust)
```

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build ./cmd/your-agent
```

Phones and WebNFC browsers connect over the [Device API](api.md#device-api) and
report the tags they scan, so such a build is complete for deployments where
every reader is a phone.

## Observing scans

An observer registered before the agent starts receives every scan, in the order
the connected clients receive it.

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

	// Register before Start. The servers read the set of observers once, when
	// they are constructed.
	rt.Agent.OnTag(func(data nfc.NFCData) {
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

`OnTag` observes rather than intercepts. The scan reaches every connected client
regardless, and the observer's return value changes nothing.

Observers run on the goroutine that feeds those clients, so they must not block.
Work that may take time belongs on a channel of your own.

## Driving a reader directly

A program that needs no WebSocket API at all can skip the agent and use a reader
on its own.

```go
package main

import (
	"log"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/pcsc"
)

func main() {
	manager := pcsc.NewManager()

	devices, err := manager.ListDevices()
	if err != nil || len(devices) == 0 {
		log.Fatalf("no reader: %v", err)
	}

	reader, err := nfc.NewNFCReader(devices[0], manager, 5*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	defer reader.Close()

	// Read-only also puts the write path out of reach, including LockCard,
	// which cannot be undone.
	reader.SetMode(nfc.ModeReadOnly)
	reader.Start()

	for data := range reader.Data() {
		if data.Err != nil {
			log.Printf("scan error: %v", data.Err)
			continue
		}
		if data.Card != nil {
			log.Printf("scanned %s (%s)", data.Card.UID, data.Card.Type)
		}
	}
}
```

Both loops are testable without hardware: `nfc` exports `NewMockManager` and
`NewMockTag`, and `nfc/nfctest` provides an emulator. Construct cards with
`nfc.NewCard` — a `nfc.Card` assembled field by field has no tag behind it and
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

`fyne.io/systray` talks to Cocoa, so `agent/tray` — and any command that
imports it — needs cgo on macOS. Every other package builds without cgo for
every supported target, so a headless build is portable.

## Related documentation

- [API reference](api.md) — the device and client WebSocket protocols
- [Control Center](control-center.md) — how the console attaches to the agent
- [Extending NFC support](extending-nfc-support.md) — adding a reader or tag type
