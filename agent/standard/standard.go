// Package standard assembles the default davi-nfc-agent: hardware readers and
// phones behind one manager, auto-TLS, device pairing, and the listener serving
// the client and device halves of /ws with the pairing endpoint on it. It is
// the wiring the shipped binary reproduced by hand and every embedder copied,
// built once here so a program takes the pieces and replaces the one it needs
// instead of maintaining its own copy — and so a field added to the assembly,
// like the raw-APDU gate on the client protocol, reaches every build at once
// rather than being forgotten in a copy.
//
// It builds neither the console nor the tray, and starts nothing: which of
// those a build runs, whether it lists a console's endpoints on [Stack.Servers],
// and when the agent starts are the program's. See [New] and
// docs/custom-builds.md.
package standard

import (
	"net/http"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/agent/pairingplugin"
	"github.com/dotside-studios/davi-nfc-agent/agent/serverplugin"
	"github.com/dotside-studios/davi-nfc-agent/agent/trustplugin"
	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/multimanager"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/secure/pairing"
	tlspkg "github.com/dotside-studios/davi-nfc-agent/secure/tls"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/clientserver"
	"github.com/dotside-studios/davi-nfc-agent/server/listener"
)

// The lines the assembly reports go on the agent's channel, where an operator
// looks for them and the console filters the log by source.
var (
	startupLog  = logbuf.Channel("agent", logbuf.LevelInfo)
	startupWarn = logbuf.Channel("agent", logbuf.LevelWarn)
)

// Stack is the assembled default agent. A program registers [Stack.Plugins] on
// the agent — after adding any console endpoints to [Stack.Servers] — builds a
// tray if it wants one, and starts the agent.
type Stack struct {
	// Runtime is the configured agent, from [agent.Setup]. Its Agent is what a
	// program starts and registers plugins on.
	Runtime *agent.Runtime

	// Devices is the smartphone / WebNFC driver, mounted behind Pairing on the
	// device half of /ws. What it scans reaches a program through
	// Runtime.Agent.Events(), not through this handle.
	Devices *remotenfc.Manager

	// Backends is the manager the agent opened its reader from: the hardware
	// manager the caller supplied and Devices, behind one multimanager.
	Backends nfc.Manager

	// Certs is the certificate the agent manages for itself, or the zero value
	// when auto-TLS was off or failed and the agent serves plain HTTP. Trust
	// installs its authority, and the listener serves the pair.
	Certs tlspkg.Provisioned

	// Pairing admits device connections and issues their credentials. It is the
	// manager the agent holds, so the readers cannot be reached without it.
	Pairing *pairing.Gate

	// Servers is the listener and everything on it: the client and device
	// halves of /ws and the /pair endpoint are already mounted. A program adds
	// its console's endpoints here, then registers this as a plugin.
	Servers *serverplugin.Plugin

	// Trust installs the local authority so browsers on this machine accept the
	// agent's certificate. Nil authority when there is no certificate.
	Trust *trustplugin.Plugin

	// Bootstrap is the cleartext CA-handoff listener a phone opens to set itself
	// up, or nil when Options.BootstrapPort is 0. Devices still pair over /pair.
	Bootstrap *pairingplugin.Plugin
}

// New assembles the stack from opts, opening its reader from hardware — the one
// part it does not build, so the shipped binary passes nfc/pcsc, a test passes a
// mock, and neither reproduces the rest. A caller that starts from
// [agent.DefaultOptions] and changes a field gets the shipped behavior with that
// one change.
//
// It resolves the config directory and provisions the certificate the same way
// the shipped binary does — writing the pair under the config directory when
// auto-TLS is on — and mutates opts.CertFile, opts.KeyFile and opts.PublicKeyPin
// to what it provisioned, so the same opts drives Setup. A failed provision is
// logged and the agent runs without TLS, as before.
//
// It does not install the process log ring (opts.Logs) or register the plugins:
// installing a global log sink and deciding what a build serves are the
// program's. Register [Stack.Plugins] once any console endpoints are on Servers.
func New(opts *agent.Options, hardware nfc.Manager) (*Stack, error) {
	if opts.ConfigDir == "" {
		opts.ConfigDir = agent.DefaultConfigDir(opts.Info.OrDefault().DirName)
	}

	// The driver serving phones. It admits nobody: its endpoint is mounted
	// behind the paired-device manager, and what that admits is what registers.
	devices := remotenfc.NewManager(remotenfc.DeviceTimeout)

	backends := multimanager.NewMultiManager(
		multimanager.ManagerEntry{Name: nfc.ManagerTypeHardware, Manager: hardware},
		multimanager.ManagerEntry{Name: nfc.ManagerTypeSmartphone, Manager: devices},
	)

	// The certificate the agent manages for itself, provisioned before Setup so
	// the same opts names it. A failure is not fatal: the agent serves plain
	// HTTP, which is what a build with no certificate does.
	var certs tlspkg.Provisioned
	if opts.AutoTLS && opts.CertFile == "" && opts.KeyFile == "" {
		provisioned, err := tlspkg.Provision(opts.ConfigDir, opts.InstallCA)
		if err != nil {
			startupWarn.Printf("Auto-TLS failed: %v (running without TLS)", err)
		} else {
			certs = provisioned
			opts.CertFile, opts.KeyFile = certs.CertFile, certs.KeyFile
			opts.PublicKeyPin = certs.PublicKeyPin
			// Native devices recognise the agent by this value rather than by a
			// trust store, so log it where a first run will show it.
			startupLog.Printf("Agent public key pin: %s", certs.PublicKeyPin)
		}
	}

	// The paired-device manager over the backends: the credential store, the
	// pairing machinery, and the check that admits a device.
	paired := pairing.New(backends, pairing.Options{
		ConfigDir:    opts.ConfigDir,
		CA:           certs.Manager,
		AppName:      opts.Info.OrDefault().DisplayName,
		PublicKeyPin: func() string { return certs.PublicKeyPin },
	})

	rt, err := agent.Setup(opts, backends)
	if err != nil {
		return nil, err
	}

	// What the manager admits on, read per use, so rotating the secret,
	// withdrawing the requirement or changing the port rebuilds nothing.
	paired.UseSecret(rt.Agent.APISecret)
	paired.Require(rt.Agent.RequirePairedDevice)
	paired.AllowLoopback(rt.Agent.AllowLoopbackBypass)
	paired.UsePort(rt.Agent.DevicePort)

	trust := &trustplugin.Plugin{Manager: certs.Manager}

	servers := &serverplugin.Plugin{
		Config:         listener.Config{CertFile: opts.CertFile, KeyFile: opts.KeyFile},
		Certificates:   certs.Manager,
		AllowedOrigins: server.ParseAllowedOrigins(opts.AllowedOrigins),
	}

	// The two halves of /ws. The agent decides who is admitted and what is
	// allowed; each protocol decides what its own side may say. The client
	// config is built here, in one place, so every gate it carries — the
	// raw-APDU channel among them — reaches the shipped binary and every
	// embedder alike.
	servers.ServeMode = map[string]http.Handler{
		server.ModeClient: clientserver.New(clientserver.Config{
			APISecret:            rt.Agent.APISecret,
			AllowLoopbackBypass:  rt.Agent.AllowLoopbackBypass,
			OriginPolicy:         servers.OriginPolicy(),
			TokenVerifier:        paired.TokenVerifier(),
			Tags:                 rt.Agent,
			AllowTagModification: rt.Agent.TagModificationAllowed,
			AllowRawTransceive:   rt.Agent.RawAPDUAllowed,
			Scans:                &rt.Agent.Events().Tag,
			ReaderStatus:         &rt.Agent.Events().Reader,
		}),
		// The driver serves the protocol; Admit decides who gets that far, and
		// names the device it admitted so the driver registers it under the
		// identity it paired with.
		server.ModeDevice: paired.Admit(devices.Handler(remotenfc.ServerOptions{
			CheckOrigin:          servers.CheckOrigin(),
			AllowTagModification: rt.Agent.TagModificationAllowed,
			PublicKeyPin:         rt.Agent.PublicKeyPin,
		})),
	}

	// Pairing issues a durable credential and the key pin a device recognises
	// the agent by, served from the listener already serving the certificate
	// that pin covers. It belongs to the paired-device manager, so it is here
	// whatever the build does about the listener.
	servers.Add(serverplugin.Endpoint{
		Name:    "pairing",
		Pattern: "/pair",
		Handler: paired.PairHandler(),
	})

	var bootstrap *pairingplugin.Plugin
	if opts.BootstrapPort > 0 {
		bootstrap = pairingplugin.New(paired, opts.BootstrapPort)
	}

	return &Stack{
		Runtime:   rt,
		Devices:   devices,
		Backends:  backends,
		Certs:     certs,
		Pairing:   paired,
		Servers:   servers,
		Trust:     trust,
		Bootstrap: bootstrap,
	}, nil
}

// Plugins returns the stack's plugins in activation order: the server first, so
// it publishes the listener the others mount on, then the bootstrap listener if
// there is one, then trust. Register them once any console endpoints are on
// [Stack.Servers], since activation is when the routes are mounted.
func (s *Stack) Plugins() []agent.Plugin {
	plugins := []agent.Plugin{s.Servers}
	if s.Bootstrap != nil {
		plugins = append(plugins, s.Bootstrap)
	}
	plugins = append(plugins, s.Trust)
	return plugins
}
