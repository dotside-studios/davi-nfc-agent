# Custom Builds

The agent is a Go module, and the binary in `cmd/davi-nfc-agent` is an ordinary
program built from packages this repository exports. Changing what it does is
therefore a `main.go` of your own rather than a fork.

```bash
go get github.com/dotside-studios/davi-nfc-agent
```

Pin the version you build against. These packages follow the agent's releases
and do not yet carry a compatibility guarantee.

## The shipped agent as a program

`cmd/davi-nfc-agent/main.go` is the whole binary: flags, TLS, pairing, the
WebSocket API, the control center and the tray.

```go
package main

import (
	"log"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/agent/console"
	"github.com/dotside-studios/davi-nfc-agent/agent/tray"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/multimanager"
	"github.com/dotside-studios/davi-nfc-agent/nfc/pcsc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
)

func main() {
	// Hardware readers and smartphones behind one manager. Nothing downstream
	// distinguishes them.
	manager := multimanager.NewMultiManager(
		multimanager.ManagerEntry{Name: nfc.ManagerTypeHardware, Manager: pcsc.NewManager()},
		multimanager.ManagerEntry{Name: nfc.ManagerTypeSmartphone, Manager: remotenfc.NewManager(30 * time.Second)},
	)

	rt, err := agent.Setup(agent.ParseFlags(), manager)
	if err != nil {
		log.Fatal(err)
	}

	// Nil in a -tags nowebui build. Assign only after a real nil check: a typed
	// nil satisfies agent.Console and would defeat every check downstream.
	c := console.New(rt.Agent, rt.Settings, rt.Logs)
	if c != nil {
		rt.Agent.Console = c
		rt.Origins.OnChange(c.NotifyChange)
		rt.Devices.OnChange(c.NotifyChange)
	}

	app := tray.New(rt)
	app.AttachConsole(c)
	app.Run()
}
```

`agent.Setup` performs the work the flags imply: it resolves the config
directory, loads or generates the TLS certificate and the API secret, reads the
paired devices and the origin allowlist, starts the pairing server, and applies
stored settings. It returns an `*agent.Runtime` holding the configured agent
alongside the stores a front end needs.

It does not choose an NFC backend. That is the second argument, and passing it
in is what allows every package beneath `main` to build without one.

## Package layout

| Package | Contents |
|---|---|
| `agent` | The agent: readers, servers, TLS, pairing, flags, configuration |
| `agent/console` | The control center, adapting the agent to `webui.Host` |
| `agent/tray` | The system tray |
| `nfc` | Readers, tag drivers, NDEF encoding and decoding |
| `nfc/pcsc` | The PC/SC hardware backend |
| `nfc/remotenfc` | Phones and WebNFC browsers, over the device bridge |
| `nfc/multimanager` | Several backends behind one `nfc.Manager` |
| `server/…` | The WebSocket device and client endpoints |
| `tls`, `settings`, `logbuf` | Certificates, persisted preferences, the log ring |

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
	opts.DevicePort = 9470
	opts.BootstrapPort = 0 // no pairing server
	opts.AllowedOrigins = "console.example.com"

	rt, err := agent.Setup(opts, pcsc.NewManager())
	if err != nil {
		log.Fatal(err)
	}

	// An empty device path selects the first reader, and waits if none is
	// attached yet.
	if err := rt.Agent.Start(rt.DevicePath); err != nil {
		log.Fatal(err)
	}
	defer rt.Agent.Stop()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
}
```

Leaving `agent.Console` nil disables the control center at runtime.
`-tags nowebui` additionally removes it from the binary.

## Building without a hardware backend

Passing only the remote manager keeps `nfc/pcsc` out of the build entirely. The
result requires no `libpcsclite` at build or run time and cross-compiles to any
target.

```go
manager := remotenfc.NewManager(30 * time.Second)

rt, err := agent.Setup(agent.DefaultOptions(), manager)
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
	defer rt.Agent.Stop()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
}
```

`OnTag` observes rather than intercepts. The scan reaches every connected client
regardless, and the observer's return value changes nothing.

Observers run on the goroutine that feeds those clients, so they must not block.
Work that may take time belongs on a channel of your own.

> Do not read `Agent.Bridge.TagData` directly. That channel has a single
> consumer, so a second reader removes scans from the broadcast rather than
> copying them.

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
every supported target, which is what makes a headless build portable.

## Related documentation

- [API reference](api.md) — the device and client WebSocket protocols
- [Control Center](control-center.md) — how the console attaches to the agent
- [Extending NFC support](extending-nfc-support.md) — adding a reader or tag type
