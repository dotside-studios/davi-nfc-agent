package standard_test

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/agent/standard"
	"github.com/dotside-studios/davi-nfc-agent/logbuf"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// freePort reserves a port by binding it and letting it go, so a test binds a
// listener without racing the default port a real agent uses.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

// testOptions is DefaultOptions pinned for a headless test: a temp config
// directory, a free port, no cleartext bootstrap listener to bind, and plain
// HTTP so no certificate is provisioned.
func testOptions(t *testing.T) *agent.Options {
	t.Helper()
	opts := agent.DefaultOptions()
	opts.ConfigDir = t.TempDir()
	opts.DevicePort = freePort(t)
	opts.BootstrapPort = 0
	opts.AutoTLS = false
	opts.Logs = logbuf.New(64)
	return opts
}

func TestNewAssemblesTheDefaultStack(t *testing.T) {
	opts := testOptions(t)

	stack, err := standard.New(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// The pieces a program builds on.
	if stack.Runtime == nil || stack.Servers == nil || stack.Pairing == nil ||
		stack.Trust == nil || stack.Devices == nil || stack.Backends == nil {
		t.Fatalf("New left a stack field nil: %+v", stack)
	}

	// Both halves of /ws are wired.
	for _, mode := range []string{server.ModeClient, server.ModeDevice} {
		if _, ok := stack.Servers.ServeMode[mode]; !ok {
			t.Errorf("ServeMode is missing %q", mode)
		}
	}

	// No cleartext listener without a port; the plugins are the server then trust.
	if stack.Bootstrap != nil {
		t.Error("Bootstrap was built with BootstrapPort 0")
	}
	if got := len(stack.Plugins()); got != 2 {
		t.Fatalf("Plugins() = %d, want 2 (server, trust)", got)
	}

	// It serves: activate it and confirm the assembly's routes are mounted.
	if err := stack.Runtime.Agent.Plugins.Add(stack.Plugins()...); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}
	menu := traymenu.New(traymenu.NewFake())
	t.Cleanup(menu.Close)
	if err := stack.Runtime.Agent.Activate(menu); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	t.Cleanup(stack.Runtime.Agent.Shutdown)

	handler := stack.Servers.Listener().Handler()
	for _, path := range []string{"/pair", "/health"} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code == http.StatusNotFound {
			t.Errorf("GET %s = 404; the assembly did not mount it", path)
		}
	}
}

func TestNewBuildsBootstrapWhenPortIsSet(t *testing.T) {
	opts := testOptions(t)
	opts.BootstrapPort = freePort(t)

	stack, err := standard.New(opts, nfc.NewMockManager())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if stack.Bootstrap == nil {
		t.Fatal("Bootstrap was not built with BootstrapPort set")
	}
	if got := len(stack.Plugins()); got != 3 {
		t.Errorf("Plugins() = %d, want 3 (server, bootstrap, trust)", got)
	}
}
