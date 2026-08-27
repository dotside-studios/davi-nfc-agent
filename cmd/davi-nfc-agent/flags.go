package main

import (
	"flag"

	"github.com/dotside-studios/davi-nfc-agent/agent"
)

// parseFlags defines the agent's command line and parses it into the options
// Setup takes.
//
// The flags live here rather than in package agent because registering them
// writes to flag.CommandLine, which belongs to the program: a library that did
// it would collide with the flags of anything embedding it. Package agent
// exposes the Options struct instead, and this is the one caller that fills it
// from a command line.
func parseFlags() (opts *agent.Options, showVersion bool) {
	opts = &agent.Options{}

	flag.BoolVar(&showVersion, "version", false, "Print version information and exit")
	flag.StringVar(&opts.DevicePath, "device", "", "Path to NFC device (optional)")
	flag.IntVar(&opts.DevicePort, "device-port", agent.DefaultDevicePort, "Port for the agent server (NFC devices and web clients share this port)")
	flag.IntVar(&opts.BootstrapPort, "bootstrap-port", agent.DefaultBootstrapPort, "Port for CA bootstrap server (0 to disable)")
	flag.StringVar(&opts.APISecret, "api-secret", "", "API secret for session handshake (optional)")
	flag.StringVar(&opts.CertFile, "cert", "", "Path to TLS certificate file (enables HTTPS/WSS)")
	flag.StringVar(&opts.KeyFile, "key", "", "Path to TLS private key file (enables HTTPS/WSS)")
	flag.BoolVar(&opts.AutoTLS, "auto-tls", true, "Automatically generate and manage TLS certificates")
	flag.BoolVar(&opts.RequirePairedDevice, "require-paired-devices", false, "Admit only devices that have paired, withdrawing the shared secret and any loopback bypass for device connections. Browser clients are unaffected")
	flag.BoolVar(&opts.AllowLoopbackBypass, "allow-loopback-bypass", false, "Admit connections from this host with no API secret. Off by default: loopback names the host, so every account, local proxy and port forward on it is admitted too")
	flag.BoolVar(&opts.InstallCA, "install-ca", false, "Install a local certificate authority into the system trust store so browsers trust this agent. Not needed for phones, readers, or an externally provisioned certificate")
	flag.StringVar(&opts.ConfigDir, "config-dir", "", "Config directory (default: platform-specific)")
	flag.StringVar(&opts.AllowedOrigins, "allowed-origins", "", "Comma-separated browser origins allowed to connect (host:port), e.g. \"app.example.com,localhost:3002\". Use \"*\" to disable the check (not recommended)")
	flag.Parse()

	return opts, showVersion
}
