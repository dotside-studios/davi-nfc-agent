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
	"os"
	"os/signal"
	"syscall"
	"time"

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

	// Hardware readers and smartphones behind one manager.
	manager := multimanager.NewMultiManager(
		multimanager.ManagerEntry{Name: nfc.ManagerTypeHardware, Manager: pcsc.NewManager()},
		multimanager.ManagerEntry{Name: nfc.ManagerTypeSmartphone, Manager: remotenfc.NewManager(30 * time.Second)},
	)

	rt, err := agent.Setup(opts, manager)
	if err != nil {
		log.Fatalf("Failed to start: %v", err)
	}

	// Nil in a -tags nowebui build. Assigned only after a real nil check: a
	// typed nil would satisfy agent.Console and defeat every check downstream.
	consoleServer := console.New(rt.Agent, rt.Settings, rt.Logs)
	if consoleServer != nil {
		rt.Agent.SetConsole(consoleServer)

		// Redraw the console whenever something changes it from elsewhere.
		rt.Agent.Origins().OnChange(consoleServer.NotifyChange)
		rt.Agent.Devices().OnChange(consoleServer.NotifyChange)
	}

	app := tray.New(rt)
	app.AttachConsole(consoleServer)

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		if rt.Agent.Bootstrap() != nil {
			rt.Agent.Bootstrap().Stop()
		}
		app.Quit()
	}()

	app.Run()
}
