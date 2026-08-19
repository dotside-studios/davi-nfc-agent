// Package main wires the agent together and runs it behind a system tray.
//
// It is deliberately thin. The agent itself lives in package agent, the control
// center in agent/console and the tray in agent/tray; this file is the only
// place that knows about all three, plus the one place that picks an NFC
// backend. Choosing the backend here rather than inside package agent is what
// lets the agent — and everything under nfc/ — build without one.
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/agent/console"
	"github.com/dotside-studios/davi-nfc-agent/agent/tray"
	"github.com/dotside-studios/davi-nfc-agent/buildinfo"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/multimanager"
	"github.com/dotside-studios/davi-nfc-agent/nfc/pcsc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
)

func main() {
	opts := agent.ParseFlags()

	if opts.Version {
		fmt.Println(buildinfo.BuildInfo())
		// Which PC/SC backend this binary was built with; the first thing to
		// know when a reader is not being detected.
		fmt.Printf("  PC/SC: %s\n", pcsc.Backend)
		os.Exit(0)
	}

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
		rt.Agent.Console = consoleServer

		// Redraw the console whenever something changes it from elsewhere.
		rt.Origins.OnChange(consoleServer.NotifyChange)
		rt.Devices.OnChange(consoleServer.NotifyChange)
	}

	app := tray.New(rt)
	app.AttachConsole(consoleServer)

	// Set up signal handling for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		if rt.Bootstrap != nil {
			rt.Bootstrap.Stop()
		}
		app.Quit()
	}()

	app.Run()
}
