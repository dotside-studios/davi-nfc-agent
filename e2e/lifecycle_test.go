package e2e

import (
	"errors"
	"net"
	"net/http"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/agent/serverplugin"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

// The tray's reader switch and the console's stop button both stop the agent
// and start it again. Whatever was serving before that has to be serving after.
func TestAnAgentRestartedInPlaceStillServesItsEndpoints(t *testing.T) {
	h := start(t, options{Tags: []nfc.Tag{presentedTag("Hello, NFC!")}})

	awaitFrame(t, h.client(t), server.WSMessageTypeTagData)

	h.Agent.Stop()
	h.reopenHardware()
	if err := h.Agent.Start(h.Runtime.DevicePath); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if h.Agent.State() != agent.StateRunning {
		t.Fatalf("State() = %s after a restart, want running", h.Agent.State())
	}

	conn, resp, err := h.dial(t, "/ws?secret="+apiSecret, nil)
	if err != nil {
		t.Fatalf("a client could not connect after the restart: %v (status %s)", err, status(resp))
	}
	awaitFrame(t, conn, server.WSMessageTypeTagData)
}

// Stopping releases the port, so a second agent can take it and a client is not
// left believing an agent that has gone is still there.
func TestStoppingTheAgentReleasesItsPort(t *testing.T) {
	h := start(t, options{})

	client := h.httpClient(t)
	resp, err := client.Get(h.Origin + "/health")
	if err != nil {
		t.Fatalf("GET /health while running: %v", err)
	}
	_ = resp.Body.Close()

	h.Agent.Stop()

	if _, err := client.Get(h.Origin + "/health"); err == nil {
		t.Error("the agent answered on its port after Stop")
	}

	listener, err := net.Listen("tcp", h.Origin[len("https://"):])
	if err != nil {
		t.Fatalf("the stopped agent still holds its port: %v", err)
	}
	_ = listener.Close()
}

// A request that arrives while nothing is serving gets an answer rather than a
// dropped connection, which is what the routes being mounted once buys.
func TestRoutesAnswerBeforeTheAgentHasStarted(t *testing.T) {
	o := agent.DefaultOptions()
	o.ConfigDir = t.TempDir()
	o.AutoTLS = false
	o.BootstrapPort = 0
	o.DevicePort = freePort(t)

	rt, err := agent.Setup(o, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}

	// The listener and its routes are what the server plugin brings, so they
	// exist from activation rather than from Start.
	servers := &serverplugin.Plugin{}
	if err := rt.Agent.Plugins.Add(servers); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}
	if err := rt.Agent.Activate(nil); err != nil {
		t.Fatalf("Activate: %v", err)
	}

	recorder := &statusRecorder{header: http.Header{}}
	request, err := http.NewRequest(http.MethodGet, "/ws", nil)
	if err != nil {
		t.Fatalf("build the request: %v", err)
	}
	servers.Listener().Handler().ServeHTTP(recorder, request)

	if recorder.status != http.StatusServiceUnavailable {
		t.Errorf("status = %d before Start, want 503", recorder.status)
	}
}

type statusRecorder struct {
	header http.Header
	status int
}

func (r *statusRecorder) Header() http.Header         { return r.header }
func (r *statusRecorder) Write(b []byte) (int, error) { return len(b), nil }
func (r *statusRecorder) WriteHeader(status int)      { r.status = status }

// Two agents cannot share a port, and the second has to say so rather than
// report itself running with nothing listening.
func TestAPortAlreadyInUseFailsTheStart(t *testing.T) {
	h := start(t, options{})

	o := agent.DefaultOptions()
	o.ConfigDir = t.TempDir()
	o.BootstrapPort = 0
	o.DevicePort = h.Agent.DevicePort()

	rt, err := agent.Setup(o, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	if err := rt.Agent.Plugins.Add(&serverplugin.Plugin{}); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}

	err = rt.Agent.Start("")
	if err == nil {
		rt.Agent.Shutdown()
		t.Fatal("a second agent bound a port already in use")
	}
	if opErr := (*net.OpError)(nil); !errors.As(err, &opErr) {
		t.Logf("start failed with %v", err)
	}
	if rt.Agent.State() != agent.StateStopped {
		t.Errorf("State() = %s after a failed start, want stopped", rt.Agent.State())
	}
}
