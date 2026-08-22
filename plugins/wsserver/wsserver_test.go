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

func (f *fakeAgent) Reader() *nfc.NFCReader            { return f.reader }
func (f *fakeAgent) RemoteDevices() *remotenfc.Manager { return f.remote }
func (f *fakeAgent) APISecret() string                 { return f.secret }

func (f *fakeAgent) RotateAPISecret() (string, error) {
	f.secret = "fresh12345678secret"
	return f.secret, nil
}

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

func TestTheMenuIsDrawnBeforeAnythingIsServing(t *testing.T) {
	host := plugin.NewHarness(wsserver.New(wsserver.Config{Agent: &fakeAgent{}}))
	t.Cleanup(func() { _ = host.Close() })

	if err := host.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// The addresses are this plugin's own menu, drawn before the listener is
	// up and reading as not running until it is.
	for _, title := range []string{"Device: Not running", "Client: Not running"} {
		if host.Tray.Find("Server URLs", title) == nil {
			t.Fatalf("%q is not on the menu:\n%s", title, host.Render())
		}
	}
}

func TestTheSecretIsHandedOutWithTheAddressesThatAskForIt(t *testing.T) {
	agent := newAgent(t)
	agent.secret = "abcd12345678wxyz"

	host := plugin.NewHarness(wsserver.New(wsserver.Config{Agent: agent, Logf: func(string, ...any) {}}))
	t.Cleanup(func() { _ = host.Close() })

	if err := host.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Redacted on the menu, whole on the clipboard: a tray is read over
	// someone's shoulder.
	label := host.Tray.Find("Server URLs", "API Secret: abcd…wxyz")
	if label == nil {
		t.Fatalf("the secret is not on the menu:\n%s", host.Render())
	}

	host.Tray.Find("Server URLs", "Copy API Secret").Deliver()
	waitFor(t, "the copy", func() bool { return len(host.Copied()) == 1 })
	if got := host.Copied()[0].Value; got != agent.secret {
		t.Errorf("copied %q, want the whole secret", got)
	}

	host.Tray.Find("Server URLs", "Regenerate API Secret").Deliver()
	waitFor(t, "the rotation", func() bool { return label.Title() != "API Secret: abcd…wxyz" })
}

func TestWithNoSecretConfiguredNothingOffersOne(t *testing.T) {
	host := plugin.NewHarness(wsserver.New(wsserver.Config{Agent: &fakeAgent{}}))
	t.Cleanup(func() { _ = host.Close() })

	if err := host.Init(); err != nil {
		t.Fatalf("Init: %v", err)
	}

	for _, title := range []string{"API Secret: not set", "Copy API Secret", "Regenerate API Secret"} {
		item := host.Tray.Find("Server URLs", title)
		if item == nil {
			t.Fatalf("%q is missing from the menu:\n%s", title, host.Render())
		}
		if item.Visible() {
			t.Errorf("%q is offered with no secret behind it", title)
		}
	}
}

func TestServingShowsThePortItBound(t *testing.T) {
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
	client, device := addressRows(t, host)

	// A device connects with the device mode on it, a client to plain /ws, and
	// both name the port that is actually bound.
	if !strings.HasSuffix(device, "/ws?mode=device") || !strings.Contains(device, port) {
		t.Errorf("device row = %q", device)
	}
	if !strings.HasSuffix(client, "/ws") || !strings.Contains(client, port) {
		t.Errorf("client row = %q", client)
	}

	// A row is its own copy entry, so what is copied cannot drift from what is
	// read.
	host.Tray.Find("Server URLs", "Device: "+device).Deliver()
	waitFor(t, "the copy", func() bool { return len(host.Copied()) == 1 })
	if got := host.Copied()[0].Value; got != device {
		t.Errorf("copied %q, want the address the row is showing", got)
	}

	// And a stop leaves the rows where they were, saying so.
	if err := host.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if servers.Serving() {
		t.Error("still serving after a stop")
	}
	if host.Tray.Find("Server URLs", "Device: Not running") == nil {
		t.Errorf("a stopped listener still hands out an address:\n%s", host.Render())
	}
}

// addressRows reads the device and client addresses off the menu.
func addressRows(t *testing.T, host *plugin.Harness) (client, device string) {
	t.Helper()

	for _, item := range host.Tray.Find("Server URLs").Children() {
		switch {
		case strings.HasPrefix(item.Title(), "Device: "):
			device = strings.TrimPrefix(item.Title(), "Device: ")
		case strings.HasPrefix(item.Title(), "Client: "):
			client = strings.TrimPrefix(item.Title(), "Client: ")
		}
	}
	if client == "" || device == "" {
		t.Fatalf("the menu is missing an address:\n%s", host.Render())
	}
	return client, device
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

func (gate) Routes() []wsserver.Route {
	return []wsserver.Route{{
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

	// And its address is listed on this server's menu, beside the endpoints the
	// server answers itself — built by whatever bound the port rather than by
	// the plugin guessing at the scheme, the host and the port.
	var listed string
	for _, item := range host.Tray.Find("Server URLs").Children() {
		if strings.HasPrefix(item.Title(), "Gate: ") {
			listed = strings.TrimPrefix(item.Title(), "Gate: ")
		}
	}
	if listed == "" {
		t.Fatalf("the labelled route is not on the menu:\n%s", host.Render())
	}
	if !strings.HasPrefix(listed, "http://") || !strings.HasSuffix(listed, "/turnstile/") ||
		!strings.Contains(listed, strconv.Itoa(agent.port)) {
		t.Errorf("listed as %q, want the agent's own scheme, port and the route's path", listed)
	}

	// It goes down with the listener, like the server's own addresses.
	if err := host.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if host.Tray.Find("Server URLs", "Gate: Not running") == nil {
		t.Errorf("a stopped listener still hands out a mounted page's address:\n%s", host.Render())
	}
}

// waitFor polls until cond holds, for a click delivered the way the platform
// delivers one: down the item's channel, on the menu's own goroutine.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
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
