package unifiedserver_test

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/server/unifiedserver"
)

// freePort reserves a port by binding it and letting it go.
func freePort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatalf("release the reserved port: %v", err)
	}
	return port
}

// The agent stops and starts its server whenever the reader changes or the
// certificate is reissued. A server that could only be started once left those
// restarts with a dead port, and the routes mounted on it are the caller's, so
// they cannot be rebuilt.
func TestAStoppedServerStartsAgainOnItsRoutes(t *testing.T) {
	port := freePort(t)
	srv := unifiedserver.New(unifiedserver.Config{Port: port})

	if err := srv.Mount("/probe", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("mounted"))
	})); err != nil {
		t.Fatalf("mount /probe: %v", err)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/probe", port)

	for _, run := range []string{"first", "second"} {
		if err := srv.Start(); err != nil {
			t.Fatalf("%s start: %v", run, err)
		}

		resp, err := http.Get(url)
		if err != nil {
			t.Fatalf("%s run: GET %s: %v", run, url, err)
		}
		body, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			t.Fatalf("%s run: read the response: %v", run, err)
		}
		if string(body) != "mounted" {
			t.Errorf("%s run: body = %q, want the mounted route", run, body)
		}

		srv.Stop()

		if _, err := http.Get(url); err == nil {
			t.Errorf("%s run: the server answered after Stop", run)
		}
	}
}

// Starting a server that is already serving is a mistake rather than a second
// listener, and it has to be reported as one.
func TestStartingATwiceRunningServerIsRefused(t *testing.T) {
	srv := unifiedserver.New(unifiedserver.Config{Port: freePort(t)})

	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer srv.Stop()

	if err := srv.Start(); err == nil {
		t.Error("a running server accepted a second Start")
	}
}

// Mounting stays closed once the mux has been built, including after a stop:
// the route would not be served by the restart either.
func TestMountingIsRefusedAfterAStop(t *testing.T) {
	srv := unifiedserver.New(unifiedserver.Config{Port: freePort(t)})

	if err := srv.Start(); err != nil {
		t.Fatalf("start: %v", err)
	}
	srv.Stop()

	if err := srv.Mount("/late", http.NotFoundHandler()); err == nil {
		t.Error("a route mounted after a stop was accepted, and nothing would serve it")
	}
}
