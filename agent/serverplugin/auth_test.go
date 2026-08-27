package serverplugin

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/gorilla/websocket"
)

// guardedEndpoint serves the real device driver behind the plugin's check, the
// way a build mounts it. It returns the device URL, the agent whose policy the
// check reads, and the credential of a device paired with it.
func guardedEndpoint(t *testing.T, strict bool) (url string, a *agent.Agent, pairedToken string) {
	t.Helper()

	return guardedEndpointWith(t, strict, false)
}

// guardedEndpointWith is guardedEndpoint with the loopback bypass decided too,
// since the test dialer connects over loopback and so sees either policy.
func guardedEndpointWith(t *testing.T, strict, allowLoopback bool) (url string, a *agent.Agent, pairedToken string) {
	t.Helper()

	devices, err := agent.NewDeviceRegistry(t.TempDir())
	if err != nil {
		t.Fatalf("agent.NewDeviceRegistry: %v", err)
	}
	if _, pairedToken, err = devices.Pair("test phone", "ios"); err != nil {
		t.Fatalf("Pair: %v", err)
	}

	a = agent.New(agent.Config{
		Manager:             nfc.NewMockManager(),
		Logger:              log.New(io.Discard, "", 0),
		APISecret:           "shared-secret",
		Devices:             devices,
		RequirePairedDevice: strict,
		AllowLoopbackBypass: allowLoopback,
		ConfigDir:           t.TempDir(),
		DevicePort:          freePort(t),
	})

	p := &Plugin{}
	if err := a.Plugins.Add(p); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}
	// Taken before Activate, as a build takes it: the handler map is assembled
	// while the plugins are still being registered.
	authenticate := p.Authenticate()
	if err := a.Activate(nil); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	m := remotenfc.NewManager(remotenfc.DeviceTimeout)
	ts := httptest.NewServer(m.Handler(remotenfc.ServerOptions{Authenticate: authenticate}))

	t.Cleanup(func() {
		ts.Close()
		m.Close()
	})

	return "ws" + strings.TrimPrefix(ts.URL, "http") + "?mode=device", a, pairedToken
}

func dialStatus(t *testing.T, url string) int {
	t.Helper()

	conn, resp, err := websocket.DefaultDialer.Dial(url, nil)
	if err == nil {
		_ = conn.Close()
		return http.StatusSwitchingProtocols
	}
	if resp == nil {
		t.Fatalf("dial failed with no response: %v", err)
	}
	return resp.StatusCode
}

// Under strict mode a paired token gets in and nothing else does: not the
// shared secret, and not the loopback bypass, which the test dialer would
// otherwise benefit from since httptest listens on 127.0.0.1.
func TestStrictModeAdmitsOnlyPairedDevices(t *testing.T) {
	url, _, token := guardedEndpointWith(t, true, true)

	if code := dialStatus(t, url+"&secret="+token); code != http.StatusSwitchingProtocols {
		t.Errorf("paired device got %d, want an upgrade", code)
	}
	if code := dialStatus(t, url+"&secret=shared-secret"); code != http.StatusUnauthorized {
		t.Errorf("shared secret got %d, want 401", code)
	}
	if code := dialStatus(t, url); code != http.StatusUnauthorized {
		t.Errorf("loopback with no credential got %d, want 401", code)
	}
}

// With strict mode off, either credential admits a device. Loopback is not one
// of them: the bypass is off unless the agent was built with it.
func TestNonStrictModeAdmitsEitherCredential(t *testing.T) {
	url, _, token := guardedEndpoint(t, false)

	if code := dialStatus(t, url); code != http.StatusUnauthorized {
		t.Errorf("loopback with no credential got %d, want 401", code)
	}
	if code := dialStatus(t, url+"&secret=shared-secret"); code != http.StatusSwitchingProtocols {
		t.Errorf("shared secret got %d, want an upgrade", code)
	}
	if code := dialStatus(t, url+"&secret="+token); code != http.StatusSwitchingProtocols {
		t.Errorf("paired token got %d, want an upgrade", code)
	}
}

// The bypass admits a bare loopback connection where the agent was built with
// it, which is what a deployment turning it back on is asking for.
func TestLoopbackBypassIsOptIn(t *testing.T) {
	url, a, _ := guardedEndpointWith(t, false, true)

	if !a.AllowLoopbackBypass() {
		t.Error("AllowLoopbackBypass did not report the setting")
	}
	if code := dialStatus(t, url); code != http.StatusSwitchingProtocols {
		t.Errorf("loopback with the bypass on got %d, want an upgrade", code)
	}

	// And it is withdrawn again by the paired-device requirement, which is the
	// stricter of the two policies.
	a.SetRequirePairedDevice(true)

	if code := dialStatus(t, url); code != http.StatusUnauthorized {
		t.Errorf("loopback under the paired-device requirement got %d, want 401", code)
	}
}

// The requirement is settable while the agent runs, so it can be tried against
// a real device without a restart.
func TestStrictModeTogglesAtRuntime(t *testing.T) {
	url, a, _ := guardedEndpoint(t, false)
	url += "&secret=shared-secret"

	if a.RequirePairedDevice() {
		t.Error("strict mode defaulted on")
	}
	if code := dialStatus(t, url); code != http.StatusSwitchingProtocols {
		t.Fatalf("the shared secret got %d before the toggle, want an upgrade", code)
	}

	a.SetRequirePairedDevice(true)

	if !a.RequirePairedDevice() {
		t.Error("RequirePairedDevice did not report the change")
	}
	if code := dialStatus(t, url); code != http.StatusUnauthorized {
		t.Errorf("the shared secret got %d after the toggle, want 401", code)
	}

	a.SetRequirePairedDevice(false)

	if code := dialStatus(t, url); code != http.StatusSwitchingProtocols {
		t.Errorf("the shared secret got %d after turning it off, want an upgrade", code)
	}
}

// A check taken from a plugin that never activated has no policy to read, so
// it admits nobody rather than leaving the endpoint open.
func TestGateWithoutAnActivatedPluginAdmitsNobody(t *testing.T) {
	p := &Plugin{}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/ws?mode=device", nil)
	req.RemoteAddr = "127.0.0.1:5555" // Not even the loopback bypass.

	if _, ok := p.Authenticate()(rec, req); ok {
		t.Error("a check with no agent behind it admitted a device")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}
