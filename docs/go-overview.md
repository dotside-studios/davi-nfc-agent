# Go overview

The agent is a regular Go module. Importing it gives you the same pieces the
shipped binary is built from, so a custom build is a `main.go` of your own
rather than a fork.

```bash
go get github.com/dotside-studios/davi-nfc-agent
```

Pin a version. The packages below follow the agent's releases and carry no
compatibility guarantee yet.

## Getting started

This is the shipped agent, in full — flags, TLS, pairing, the WebSocket API,
the control center and the tray:

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
	// Hardware readers and smartphones behind one manager. Nothing below this
	// line knows which is which.
	manager := multimanager.NewMultiManager(
		multimanager.ManagerEntry{Name: nfc.ManagerTypeHardware, Manager: pcsc.NewManager()},
		multimanager.ManagerEntry{Name: nfc.ManagerTypeSmartphone, Manager: remotenfc.NewManager(30 * time.Second)},
	)

	rt, err := agent.Setup(agent.ParseFlags(), manager)
	if err != nil {
		log.Fatal(err)
	}

	// nil in a -tags nowebui build. Assign only after a real nil check: a typed
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

`agent.Setup` does the work the flags imply: resolves the config directory,
generates or loads the TLS certificate and the API secret, loads the paired
devices and the origin allowlist, starts the pairing server, and applies stored
settings. It returns a `*agent.Runtime` holding the configured agent and the
stores a front end needs.

The one thing it does *not* do is choose an NFC backend. That is the argument
you pass, and it is what lets everything below `main` build without one.

## The packages

| Package | What it is |
|---|---|
| `agent` | The agent: readers, servers, TLS, pairing, flags, config |
| `agent/console` | The control center, adapting the agent to `webui.Host` |
| `agent/tray` | The system tray |
| `nfc` | Readers, tag drivers, NDEF encode/decode |
| `nfc/pcsc` | The PC/SC hardware backend |
| `nfc/remotenfc` | Phones and WebNFC browsers, over the device bridge |
| `nfc/multimanager` | Several backends behind one `nfc.Manager` |
| `server/…` | The WebSocket device and client endpoints |
| `tls`, `settings`, `logbuf` | Certificates, persisted preferences, the log ring |

Dependencies run one way: `agent/console` and `agent/tray` import `agent`, never
the reverse, and nothing below `main` imports both. Two consequences worth
knowing before you wire anything up:

- **`agent` pulls in no GUI.** `fyne.io/systray` arrives only with `agent/tray`.
- **`agent` pulls in no NFC backend.** `nfc/pcsc` arrives only where you import
  it, which for the shipped binary is `main.go`.

So a headless build is not a stripped-down agent. It is the same agent, minus
two imports.

## A headless agent

No tray, no console — a service that reads cards and serves the WebSocket API.
`agent.DefaultOptions` gives you the same defaults the flags would:

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

	// "" auto-selects the first reader, and waits if none is attached yet.
	if err := rt.Agent.Start(rt.DevicePath); err != nil {
		log.Fatal(err)
	}
	defer rt.Agent.Stop()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
}
```

Leaving `agent.Console` nil is all it takes to drop the control center at
runtime; `-tags nowebui` drops it from the binary as well.

## An agent with no hardware readers

Pass only the remote manager and the PC/SC backend never enters the build. The
result needs no `libpcsclite` at build or run time, and cross-compiles to any
target:

```go
manager := remotenfc.NewManager(30 * time.Second)

rt, err := agent.Setup(agent.DefaultOptions(), manager)
```

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build .
```

Phones and WebNFC browsers connect over the [Device API](api.md#device-api) and
report what they scan, so this is a complete agent for tap-your-phone
deployments — not a degraded one.

## Reacting to tags in your own code

Register an observer before starting the agent and it sees every scan, in the
same order the connected clients do:

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

	// Register before Start: the servers read the observers once, when they
	// are built.
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

`OnTag` observes; it does not intercept. The scan is broadcast to every
connected client either way, and what the observer returns changes nothing.

It runs on the goroutine that feeds those clients, so it must not block — for
anything slow, push onto a channel of your own and handle it elsewhere. And do
not read `Agent.Bridge.TagData` directly: that channel has exactly one
consumer, so a second reader takes scans away from the browsers instead of
copying them. `OnTag` exists precisely so you never need to.

### Without the servers

If you want no WebSocket API at all, skip the agent and drive a reader:

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

	// A turnstile never writes. This also puts LockCard, which is
	// irreversible, out of reach.
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

`nfc` exports mocks (`NewMockManager`, `NewMockTag`) and the `nfc/nfctest`
emulator, so either loop is testable with no hardware attached. Build cards with
`nfc.NewCard`; a `nfc.Card` composed field-by-field has no tag behind it and
cannot be read from.

## Custom readers

`nfc.Manager`, `nfc.Device` and `nfc.Tag` are interfaces, and `multimanager`
runs several implementations side by side — which is how hardware and phones
coexist today. Implementing them for a reader of your own is covered in
[Extending NFC support](extending-nfc-support.md).

## Build tags

| Build | Effect |
|---|---|
| *(default)* | Everything, PC/SC through goscard, no cgo |
| `-tags nowebui` | No control center: no `/control` routes, no privileged API, no tray entry, no embedded frontend |
| `-tags cgopcsc` | PC/SC through `ebfe/scard` instead, which needs cgo and `libpcsclite` |
| `CGO_ENABLED=0` | The default already; nothing in the agent requires cgo except the tray on macOS |

The tray needs cgo on macOS, because `fyne.io/systray` talks to Cocoa. Every
other package — the agent, the console, `nfc/…`, `server/…` — builds cgo-free
for every target, which is what makes a headless build portable.

## Next

- [API reference](api.md) — the device and client WebSocket protocols
- [Control Center](control-center.md) — how the console plugs into the agent
- [Extending NFC support](extending-nfc-support.md) — adding a reader or tag type
