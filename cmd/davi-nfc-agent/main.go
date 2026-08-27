// Package main wires the agent together and runs it behind a system tray.
//
// It is the only place that knows about the agent, the console and the tray at
// once, the one that picks an NFC backend, and the one that touches the flag
// set and the standard logger. See docs/custom-builds.md.
package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/agent/console"
	"github.com/dotside-studios/davi-nfc-agent/agent/pairingplugin"
	"github.com/dotside-studios/davi-nfc-agent/agent/serverplugin"
	"github.com/dotside-studios/davi-nfc-agent/agent/tray"
	"github.com/dotside-studios/davi-nfc-agent/agent/trustplugin"
	"github.com/dotside-studios/davi-nfc-agent/buildinfo"
	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/multimanager"
	"github.com/dotside-studios/davi-nfc-agent/nfc/pcsc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	tlspkg "github.com/dotside-studios/davi-nfc-agent/secure/tls"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/clientserver"
	"github.com/dotside-studios/davi-nfc-agent/server/listener"
)

// What this program reports while it assembles the agent. On the agent's
// channel because that is where an operator looked for these lines before
// provisioning moved out of Setup, and the console filters the log by source.
var (
	startupLog  = logbuf.Channel("agent", logbuf.LevelInfo)
	startupWarn = logbuf.Channel("agent", logbuf.LevelWarn)
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
	//
	// Install names it to the packages reporting on their own channels, and
	// Options hands the same ring to the agent for its own log and its
	// plugins'. The standard logger keeps stderr alone: a line that reached the
	// ring both ways would be shown twice, at two levels.
	opts.Logs = logbuf.New(logbuf.DefaultCapacity)
	logbuf.Install(opts.Logs)

	// The driver serving phones. What it scans and what it holds reach the
	// agent through the manager below; its endpoint is served alongside the
	// clients, below.
	devices := remotenfc.NewManager(remotenfc.DeviceTimeout)

	// Hardware readers and phones behind one manager.
	manager := multimanager.NewMultiManager(
		multimanager.ManagerEntry{Name: nfc.ManagerTypeHardware, Manager: pcsc.NewManager()},
		multimanager.ManagerEntry{Name: nfc.ManagerTypeSmartphone, Manager: devices},
	)

	// The certificate this agent manages for itself, under the same config
	// directory the agent resolves. Provisioning it is the program's: the
	// agent neither serves it nor hands out its authority.
	if opts.ConfigDir == "" {
		opts.ConfigDir = agent.DefaultConfigDir(opts.Info.OrDefault().DirName)
	}
	var certs tlspkg.Provisioned
	if opts.AutoTLS && opts.CertFile == "" && opts.KeyFile == "" {
		provisioned, err := tlspkg.Provision(opts.ConfigDir, opts.InstallCA)
		if err != nil {
			startupWarn.Printf("Auto-TLS failed: %v (running without TLS)", err)
		} else {
			certs = provisioned
			opts.CertFile, opts.KeyFile = certs.CertFile, certs.KeyFile
			opts.PublicKeyPin = certs.PublicKeyPin
			// Native devices authenticate the agent by this value rather than
			// by a trust store, so log it where a first run will show it.
			startupLog.Printf("Agent public key pin: %s", certs.PublicKeyPin)
		}
	}

	rt, err := agent.Setup(opts, manager)
	if err != nil {
		log.Fatalf("Failed to start: %v", err)
	}

	// The entry that makes browsers accept the certificate above.
	trust := &trustplugin.Plugin{Manager: certs.Manager}

	// The listener and everything on it. Registering no server plugin leaves
	// an agent that serves nothing.
	servers := &serverplugin.Plugin{
		Config:         listener.Config{CertFile: opts.CertFile, KeyFile: opts.KeyFile},
		Certificates:   certs.Manager,
		AllowedOrigins: server.ParseAllowedOrigins(opts.AllowedOrigins),
	}

	// The two halves of /ws, both built here. The agent decides who is admitted
	// and what is allowed; each protocol decides what its own side may say.
	servers.ServeMode = map[string]http.Handler{
		server.ModeClient: clientserver.New(clientserver.Config{
			APISecret:            rt.Agent.APISecret,
			AllowLoopbackBypass:  rt.Agent.AllowLoopbackBypass,
			OriginPolicy:         servers.OriginPolicy(),
			TokenVerifier:        rt.Agent.TokenVerifier(),
			Tags:                 rt.Agent,
			AllowTagModification: rt.Agent.TagModificationAllowed,
			Scans:                &rt.Agent.Events().Tag,
			ReaderStatus:         &rt.Agent.Events().Reader,
		}),
		server.ModeDevice: devices.Handler(remotenfc.ServerOptions{
			Authenticate:         servers.Authenticate(),
			CheckOrigin:          servers.CheckOrigin(),
			AllowTagModification: rt.Agent.TagModificationAllowed,
			PublicKeyPin:         rt.Agent.PublicKeyPin,
			Revocations:          rt.Agent.Devices(),
		}),
	}

	// The pairing server, on a listener of its own, with the menu entries that
	// hand out its address and PIN.
	//
	// That listener is cleartext: it hands out the certificate authority to a
	// device that does not trust the agent's certificate yet. Pairing issues a
	// durable credential and the key pin a device recognises this agent by, so
	// it mounts on the listener already serving the certificate that pin
	// covers.
	var pairing *pairingplugin.Plugin
	if opts.BootstrapPort > 0 {
		pairing = pairingplugin.New(rt.Agent, opts.BootstrapPort, certs.Manager)
		servers.Add(serverplugin.Endpoint{
			Name:    "pairing",
			Pattern: "/pair",
			Handler: pairing.Server.Server().PairHandler(),
		})
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
