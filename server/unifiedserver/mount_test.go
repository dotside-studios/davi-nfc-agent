package unifiedserver_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/clientserver"
	"github.com/dotside-studios/davi-nfc-agent/server/deviceserver"
	"github.com/dotside-studios/davi-nfc-agent/server/unifiedserver"
)

// mounted builds the routing with the given mounts and nothing listening.
func mounted(t *testing.T, mounts ...unifiedserver.Mount) http.Handler {
	t.Helper()

	bridge := server.NewServerBridge()
	t.Cleanup(bridge.Close)

	device := deviceserver.New(deviceserver.Config{}, bridge)
	client := clientserver.New(clientserver.Config{}, bridge)

	return unifiedserver.New(unifiedserver.Config{
		Mounts: mounts,
		Logf:   func(string, ...any) {},
	}, device, client).Handler()
}

func TestMountsAreServed(t *testing.T) {
	routes := mounted(t, unifiedserver.Mount{
		Pattern: "/turnstile/",
		Owner:   "turnstile",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("gate " + r.URL.Path))
		}),
	})

	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/turnstile/open", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want the mounted handler to answer", rec.Code)
	}
	if got := rec.Body.String(); got != "gate /turnstile/open" {
		t.Fatalf("body = %q, want the whole subtree to reach the handler", got)
	}
}

func TestTheRootCanBeClaimed(t *testing.T) {
	// The banner is what answers the root while nothing else wants it. The
	// control center is what usually does.
	routes := mounted(t, unifiedserver.Mount{
		Pattern: "/",
		Owner:   "console",
		Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("console"))
		}),
	})

	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	if got := rec.Body.String(); got != "console" {
		t.Fatalf("root answered %q, want the mount that claimed it", got)
	}

	// The endpoints the agent serves itself are untouched by it.
	rec = httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK || rec.Body.String() == "console" {
		t.Fatalf("/health answered %d %q", rec.Code, rec.Body.String())
	}
}

func TestAMountCannotTakeOverTheAgentsOwnRoutes(t *testing.T) {
	// The agent's own endpoints are not a plugin's to replace, and a duplicate
	// pattern would otherwise panic http.ServeMux as the agent starts.
	for _, pattern := range []string{"/ws", "/health", "/api/v1/health"} {
		routes := mounted(t, unifiedserver.Mount{
			Pattern: pattern,
			Owner:   "greedy",
			Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("hijacked"))
			}),
		})

		for _, path := range []string{"/health", "/"} {
			rec := httptest.NewRecorder()
			routes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Body.String() == "hijacked" {
				t.Fatalf("a mount on %q answered %s", pattern, path)
			}
		}
	}
}

func TestIncompleteMountsAreIgnored(t *testing.T) {
	// Building the routes at all is the assertion: either would panic
	// http.ServeMux if it were registered.
	routes := mounted(t,
		unifiedserver.Mount{Pattern: "", Handler: http.NotFoundHandler()},
		unifiedserver.Mount{Pattern: "/nothing/", Handler: nil},
	)

	rec := httptest.NewRecorder()
	routes.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("the agent's own routes stopped working: %d", rec.Code)
	}
}
