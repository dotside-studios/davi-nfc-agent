// Package main wires the agent together and runs it behind a system tray.
//
// It is the only place that knows about the agent, the console and the tray at
// once, the one that picks an NFC backend, and the one that touches the flag
// set and the standard logger. See docs/custom-builds.md.
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
	"github.com/dotside-studios/davi-nfc-agent/server/listener"
)

func main() {
	opts, showVersion := parseFlags()

	if showVersion {
		fmt.Println(buildinfo.BuildInfo())
		// The first thing to know when a reader is not being detected.
		fmt.Printf("  PC/SC: %s\n", pcsc.Backend)
		os.Exit(0)
	}

	// Before anything else logs: started from a desktop launcher there is no
	// stderr to read, and the console reads this ring.
	opts.Logs = logbuf.New(logbuf.DefaultCapacity)
	log.SetOutput(io.MultiWriter(os.Stderr, opts.Logs))

	// The driver serving phones. What it scans and what it holds reach the
	// agent through the manager below; only its endpoint is handed over.
	devices := remotenfc.NewManager(remotenfc.DeviceTimeout)
	opts.DeviceEndpoint = func(o agent.DeviceEndpointOptions) http.Handler {
		return devices.Handler(remotenfc.ServerOptions{
			Authenticate:         o.Authenticate,
			CheckOrigin:          o.CheckOrigin,
			AllowTagModification: o.AllowTagModification,
			PublicKeyPin:         o.PublicKeyPin,
		})
	}

	// Hardware readers and phones behind one manager.
	manager := multimanager.NewMultiManager(
		multimanager.ManagerEntry{Name: nfc.ManagerTypeHardware, Manager: pcsc.NewManager()},
		multimanager.ManagerEntry{Name: nfc.ManagerTypeSmartphone, Manager: devices},
	)

	rt, err := agent.Setup(opts, manager)
	if err != nil {
		log.Fatalf("Failed to start: %v", err)
	}

	// The certificate this agent serves, and the entry that makes browsers
	// accept it.
	trust := &agent.TrustPlugin{Manager: rt.Certificates}

	// The listener and everything on it. Setup resolved which certificate to
	// serve; registering no server plugin leaves an agent that serves nothing.
	servers := &agent.ServerPlugin{
		Config:       listener.Config{CertFile: rt.CertFile, KeyFile: rt.KeyFile},
		Certificates: rt.Certificates,
	}

	// The pairing server, on a listener of its own, with the menu entries that
	// hand out its address and PIN.
	var pairing *agent.PairingPlugin
	if opts.BootstrapPort > 0 {
		pairing = agent.NewPairingPlugin(rt.Agent, opts.BootstrapPort, rt.Certificates)
	}

	app := tray.New(rt)

	// The control center, served from the same listener. Nil in a -tags nowebui
	// build, where Endpoints is empty.
	controlCenter := console.New(console.Config{
		Agent:   rt.Agent,
		Logs:    rt.Logs,
		Servers: servers,
		Pairing: pairing,
		Trust:   trust,
		Quit:    app.Quit,
	})
	servers.Add(controlCenter.Endpoints()...)

	// The server goes on first: it publishes the listener the rest mount on,
	// and activation order is the order their entries appear in the tray.
	plugins := []agent.Plugin{servers}
	if pairing != nil {
		plugins = append(plugins, pairing)
	}
	plugins = append(plugins, trust)

	if err := rt.Agent.Plugins.Add(plugins...); err != nil {
		log.Fatalf("Failed to register a plugin: %v", err)
	}

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		// Quitting the tray stops the agent and every component with it.
		app.Quit()
	}()

	app.Run()
}
