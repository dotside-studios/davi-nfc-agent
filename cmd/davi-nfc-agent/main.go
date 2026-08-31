// Package main wires the agent together and runs it behind a system tray.
//
// The default stack — readers and phones behind one manager, auto-TLS, pairing,
// the listener and its protocols — is assembled by agent/standard; this program
// picks the NFC backend, adds the console and the tray, and owns the flag set
// and the standard logger. See docs/custom-builds.md.
package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/dotside-studios/davi-nfc-agent/agent/console"
	"github.com/dotside-studios/davi-nfc-agent/agent/standard"
	"github.com/dotside-studios/davi-nfc-agent/agent/tray"
	"github.com/dotside-studios/davi-nfc-agent/buildinfo"
	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/nfc/pcsc"
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
	// stderr to read, and the console reads this ring. Install names it to the
	// packages reporting on their own channels, and Options hands the same ring
	// to the agent for its own log and its plugins'. The standard logger keeps
	// stderr alone: a line that reached the ring both ways would be shown twice.
	opts.Logs = logbuf.New(logbuf.DefaultCapacity)
	logbuf.Install(opts.Logs)

	// The default stack, opening its reader from nfc/pcsc: hardware readers and
	// phones behind one manager, auto-TLS, pairing, and the listener serving the
	// client and device protocols with /pair on it.
	stack, err := standard.New(opts, pcsc.NewManager())
	if err != nil {
		log.Fatalf("Failed to start: %v", err)
	}

	app := tray.New(stack.Runtime)

	// The control center, served from the same listener. Nil in a -tags nowebui
	// build, where Endpoints is empty.
	controlCenter := console.New(console.Config{
		Agent:         stack.Runtime.Agent,
		Logs:          stack.Runtime.Logs,
		Servers:       stack.Servers,
		Pairing:       stack.Pairing,
		BootstrapPort: opts.BootstrapPort,
		Certificates:  stack.Certs.Manager,
		Quit:          app.Quit,
	})
	stack.Servers.Add(controlCenter.Endpoints()...)

	if err := stack.Runtime.Agent.Plugins.Add(stack.Plugins()...); err != nil {
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
