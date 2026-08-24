// Package main wires the agent together and runs it behind a system tray.
//
// It is deliberately thin. The agent itself lives in package agent, the control
// center in agent/console and the tray in agent/tray; this command is the only
// place that knows about all three, the one place that picks an NFC backend,
// and the one place that touches process-wide state — the flag set and the
// standard logger. Keeping those here is what lets the agent be embedded in a
// program with a command line and a logger of its own.
package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/agent/console"
	"github.com/dotside-studios/davi-nfc-agent/agent/tray"
	"github.com/dotside-studios/davi-nfc-agent/buildinfo"
	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/multimanager"
	"github.com/dotside-studios/davi-nfc-agent/nfc/pcsc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
)

func main() {
	opts := parseFlags()

	if opts.Version {
		fmt.Println(buildinfo.BuildInfo())
		// Which PC/SC backend this binary was built with; the first thing to
		// know when a reader is not being detected.
		fmt.Printf("  PC/SC: %s\n", pcsc.Backend)
		os.Exit(0)
	}

	// Capture log output in memory before anything else logs. Started from a
	// desktop launcher there is no stderr to read, so without this the agent's
	// diagnostics are discarded as they are produced. Installed here, before
	// Setup, so the startup sequence lands in the ring the console reads.
	opts.Logs = logbuf.New(logbuf.DefaultCapacity)
	log.SetOutput(io.MultiWriter(os.Stderr, opts.Logs))

	// The driver serving phones. Built here, because this is what decides the
	// agent should serve them at all: it is handed over as an interface, a
	// channel of scans and a handler, so the agent names no device protocol.
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

	// Hardware readers and phones behind one manager, which is what the agent
	// opens its reader from.
	manager := multimanager.NewMultiManager(
		multimanager.ManagerEntry{Name: nfc.ManagerTypeHardware, Manager: pcsc.NewManager()},
		multimanager.ManagerEntry{Name: nfc.ManagerTypeSmartphone, Manager: devices},
	)

	rt, err := agent.Setup(opts, manager)
	if err != nil {
		log.Fatalf("Failed to start: %v", err)
	}

	// The listener and everything on it. Setup does not build one: what this
	// agent serves is this program's decision, and registering no server
	// plugin at all leaves an agent that drives the reader and serves nothing.
	servers := &agent.ServerPlugin{}

	// Nil in a -tags nowebui build, where the control center is not compiled
	// in. Listing it as an endpoint is all there is to attaching one: a build
	// that wants none lists none, and the agent is no wiser either way.
	consoleServer := console.New(rt.Agent, rt.Settings, rt.Logs)
	if consoleServer != nil {
		// The control API and the console page are deliberately not wrapped in
		// CORS: one administers the agent and the other is a page, so no other
		// origin has business calling them.
		servers.Add(
			agent.Endpoint{Name: "control API", Pattern: "/control/", Handler: consoleServer.Routes()},
			agent.Endpoint{Name: "control center", Pattern: "/", Handler: consoleServer.Assets()},
		)

		// Redraw the console whenever something changes it from elsewhere.
		rt.Agent.Origins().OnChange(consoleServer.NotifyChange)
		rt.Agent.Devices().OnChange(consoleServer.NotifyChange)
		rt.Agent.OnClientsChange(consoleServer.NotifyChange)
	}

	if err := rt.Agent.Plugins.Add(servers); err != nil {
		log.Fatalf("Failed to register the server: %v", err)
	}

	app := tray.New(rt)
	app.AttachConsole(consoleServer)

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		// The pairing server is a component of the agent now, so the tray's
		// quit path takes it down with everything else.
		app.Quit()
	}()

	app.Run()
}
