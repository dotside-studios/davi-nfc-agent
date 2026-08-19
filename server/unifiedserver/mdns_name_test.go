package unifiedserver

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/buildinfo"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

// TestMDNSServiceNameFallback pins the name an unconfigured agent advertises.
// The agent now supplies it from its own identity, so this guards against the
// two drifting apart and quietly renaming the service every device looks for.
func TestMDNSServiceNameFallback(t *testing.T) {
	if got := (Config{}).mdnsServiceName(); got != server.MDNSDeviceServiceName {
		t.Errorf("empty Config advertises %q, want %q", got, server.MDNSDeviceServiceName)
	}

	// What the agent passes for a default build must be that same name.
	fromIdentity := buildinfo.Default().DisplayName + " Device"
	if fromIdentity != server.MDNSDeviceServiceName {
		t.Errorf("identity-derived name %q != historical %q", fromIdentity, server.MDNSDeviceServiceName)
	}
}

// TestMDNSServiceNameOverride is the point of the field: a program built on the
// agent announces itself under its own name.
func TestMDNSServiceNameOverride(t *testing.T) {
	c := Config{MDNSServiceName: "Gate Reader Device"}
	if got := c.mdnsServiceName(); got != "Gate Reader Device" {
		t.Errorf("mdnsServiceName() = %q, want the override", got)
	}
}
