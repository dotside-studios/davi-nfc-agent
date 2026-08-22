package wsserver_test

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/plugin"
	"github.com/dotside-studios/davi-nfc-agent/plugins/wsserver"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

// fakeAgent is an agent with a reader and nothing else, which is all the
// servers ask for.
type fakeAgent struct {
	reader  *nfc.NFCReader
	remote  *remotenfc.Manager
	secret  string
	port    int
	changed int
}

func (f *fakeAgent) Reader() *nfc.NFCReader              { return f.reader }
func (f *fakeAgent) RemoteDevices() *remotenfc.Manager   { return f.remote }
func (f *fakeAgent) APISecret() string                   { return f.secret }
func (f *fakeAgent) PublicKeyPin() string                { return "" }
func (f *fakeAgent) TokenVerifier() server.TokenVerifier { return nil }
func (f *fakeAgent) OriginPolicy() server.OriginPolicy   { return nil }
func (f *fakeAgent) AllowedOrigins() []string            { return nil }
func (f *fakeAgent) AllowedCardTypes() map[string]bool   { return nil }
func (f *fakeAgent) RequirePairedDevice() bool           { return false }
func (f *fakeAgent) Port() int                           { return f.port }
func (f *fakeAgent) Certificates() (string, string)      { return "", "" }
func (f *fakeAgent) ClientsChanged()                     { f.changed++ }

// newAgent builds an agent with a reader over the phone driver, which needs no
// hardware, and a port nothing else is on.
func newAgent(t *testing.T) *fakeAgent {
	t.Helper()

	remote := remotenfc.NewManager(remotenfc.DeviceTimeout)
	t.Cleanup(remote.Close)

	reader, err := nfc.NewNFCReader("", remote, time.Second)
	if err != nil {
		t.Fatalf("NewNFCReader: %v", err)
	}
	t.Cleanup(reader.Stop)

	return &fakeAgent{reader: reader, remote: remote, port: freePort(t)}
}

// freePort takes a port and gives it straight back, which is the closest thing
// to reserving one.
func freePort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	return listener.Addr().(*net.TCPAddr).Port
}

func TestAddressesAreDeclaredBeforeAnythingIsServing(t *testing.T) {
	host := plugin.NewHarness(wsserver.New(wsserver.Config{Agent: &fakeAgent{}}))
	t.Cleanup(func() { _ = host.Close() })

	if err := host.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// They hold their place on a menu drawn before the listener is up, and read
	// as not running until it is.
	for _, id := range []string{wsserver.EndpointDevice, wsserver.EndpointClient} {
		endpoint, ok := host.Endpoints().Get(id)
		if !ok {
			t.Fatalf("%s was never declared", id)
		}
		if endpoint.Running() {
			t.Errorf("%s reads as running with nothing serving: %q", id, endpoint.URL)
		}
	}
}

func TestServingPublishesThePortItBound(t *testing.T) {
	agent := newAgent(t)
	servers := wsserver.New(wsserver.Config{Agent: agent, Logf: func(string, ...any) {}})

	host := plugin.NewHarness(servers)
	t.Cleanup(func() { _ = host.Close() })

	if err := host.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !servers.Serving() {
		t.Fatal("nothing is serving after a clean start")
	}
	if servers.Port() != agent.port {
		t.Fatalf("serving on %d, want the configured %d", servers.Port(), agent.port)
	}

	port := strconv.Itoa(agent.port)
	device, _ := host.Endpoints().Get(wsserver.EndpointDevice)
	client, _ := host.Endpoints().Get(wsserver.EndpointClient)

	// A device connects with the device mode on it, a client to plain /ws, and
	// both name the port that is actually bound.
	if !strings.HasSuffix(device.URL, "/ws?mode=device") || !strings.Contains(device.URL, port) {
		t.Errorf("device URL = %q", device.URL)
	}
	if !strings.HasSuffix(client.URL, "/ws") || !strings.Contains(client.URL, port) {
		t.Errorf("client URL = %q", client.URL)
	}

	// And a stop withdraws them without losing their place.
	if err := host.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if servers.Serving() {
		t.Error("still serving after a stop")
	}
	if endpoint, _ := host.Endpoints().Get(wsserver.EndpointDevice); endpoint.Running() {
		t.Errorf("a stopped listener still hands out %q", endpoint.URL)
	}
	if endpoint, _ := host.Endpoints().Get(wsserver.EndpointDevice); endpoint.Label != "Device" {
		t.Error("the entry lost its label on the way down")
	}
}

func TestWithoutAReaderNothingServes(t *testing.T) {
	host := plugin.NewHarness(wsserver.New(wsserver.Config{Agent: &fakeAgent{}}))
	t.Cleanup(func() { _ = host.Close() })

	err := host.Start()
	if err == nil || !strings.Contains(err.Error(), "reader") {
		t.Fatalf("Start returned %v, want it to say what is missing", err)
	}
}

// gate is a consumer's plugin with a page of its own and no listener for it.
type gate struct{}

func (gate) Describe() plugin.Info { return plugin.Info{ID: "turnstile", Title: "Turnstile"} }

func (gate) Routes() []plugin.Route {
	return []plugin.Route{{
		Pattern: "/turnstile/",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("gate"))
		}),
		Label: "Gate",
	}}
}

func TestAPluginsPageIsServedOnTheAgentsPort(t *testing.T) {
	agent := newAgent(t)
	servers := wsserver.New(wsserver.Config{Agent: agent, Logf: func(string, ...any) {}})

	// Registered after the server, as a consumer's plugin would be.
	host := plugin.NewHarness(servers, gate{})
	t.Cleanup(func() { _ = host.Close() })

	if err := host.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	url := "http://" + net.JoinHostPort("127.0.0.1", strconv.Itoa(agent.port)) + "/turnstile/open"
	body := get(t, url)
	if body != "gate" {
		t.Fatalf("the agent's port answered %q at the plugin's path", body)
	}

	// And its address is handed out beside the agent's own, built by whatever
	// bound the port rather than by the plugin guessing at it.
	route := plugin.Route{Pattern: "/turnstile/", Owner: "turnstile"}
	endpoint, ok := host.Endpoints().Get(route.EndpointID())
	if !ok {
		t.Fatal("the labelled route was not published")
	}
	if endpoint.Label != "Gate" {
		t.Errorf("published as %q, want the name the plugin gave it", endpoint.Label)
	}
	if !strings.HasSuffix(endpoint.URL, "/turnstile/") || !strings.Contains(endpoint.URL, strconv.Itoa(agent.port)) {
		t.Errorf("published URL = %q, want the agent's own port and the route's path", endpoint.URL)
	}
	if !strings.HasPrefix(endpoint.URL, "http://") {
		t.Errorf("published URL = %q, want the scheme the listener is serving", endpoint.URL)
	}

	// It goes down with the listener, like the agent's own addresses.
	if err := host.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if endpoint, _ := host.Endpoints().Get(route.EndpointID()); endpoint.Running() {
		t.Errorf("a stopped listener still hands out %q", endpoint.URL)
	}
}

func get(t *testing.T, url string) string {
	t.Helper()

	var last error
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err != nil {
			// The listener comes up on a goroutine of its own.
			last = err
			time.Sleep(5 * time.Millisecond)
			continue
		}
		defer resp.Body.Close()

		buf := make([]byte, 64)
		n, _ := resp.Body.Read(buf)
		return string(buf[:n])
	}

	t.Fatalf("nothing answered %s: %v", url, last)
	return ""
}
