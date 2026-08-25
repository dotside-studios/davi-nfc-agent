// Package main wires the agent together and runs it behind a system tray.
//
// It is deliberately thin. The agent itself lives in package agent, the control
// center in agent/console and the tray in agent/tray; this command is the only
// place that knows about all three, the one place that picks an NFC backend,
// and the one place that touches process-wide state: the flag set and the
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
	"github.com/dotside-studios/davi-nfc-agent/server/unifiedserver"
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

	// The certificate this agent serves, and the entry that makes browsers
	// accept it. Whatever needs a certificate is given this rather than
	// reaching for one: the agent holds none.
	trust := &agent.TrustPlugin{Manager: rt.Certificates}

	// The listener and everything on it. Setup does not build one: what this
	// agent serves is this program's decision, and registering no server
	// plugin at all leaves an agent that drives the reader and serves nothing.
	// A certificate named on the command line is served ahead of the managed
	// one, which -cert and -key turn off.
	servers := &agent.ServerPlugin{
		Trust:  trust,
		Config: unifiedserver.Config{CertFile: opts.CertFile, KeyFile: opts.KeyFile},
	}

	// The pairing server, on a listener of its own, with the menu entries that
	// hand out its address and PIN. The agent does not hold one, so this is
	// where it is built and where it is handed to the console.
	var pairing *agent.PairingPlugin
	if opts.BootstrapPort > 0 {
		pairing = agent.NewPairingPlugin(rt.Agent, opts.BootstrapPort, trust)
	}

	// The control center, served from the same listener. Nil in a -tags nowebui
	// build, where there is none compiled in and Endpoints is empty, so this
	// program needs no build tag of its own.
	controlCenter := console.New(console.Config{
		Agent:   rt.Agent,
		Logs:    rt.Logs,
		Servers: servers,
		Pairing: pairing,
		Trust:   trust,
	})
	servers.Add(controlCenter.Endpoints()...)

	// The server goes on first: it publishes the listener the rest mount on,
	// and plugins are activated in the order they were added, which is also the
	// order their entries appear in the tray.
	plugins := []agent.Plugin{servers}
	if pairing != nil {
		plugins = append(plugins, pairing)
	}
	plugins = append(plugins, trust)

	if err := rt.Agent.Plugins.Add(plugins...); err != nil {
		log.Fatalf("Failed to register a plugin: %v", err)
	}

	app := tray.New(rt)
	app.AttachConsole(controlCenter)

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		// The pairing server is a component the plugin registered, so the
		// tray's quit path takes it down with everything else.
		app.Quit()
	}()

	app.Run()
}
