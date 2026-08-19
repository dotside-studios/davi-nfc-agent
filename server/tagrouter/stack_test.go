package tagrouter_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/tagrouter"
)

// stack is what the agent composes: the driver's device endpoint behind the
// credential check, and the router draining the bridge behind both. The tests
// build it the same way so they exercise the composition, not a stand-in.
type stack struct {
	URL    string
	Bridge *server.ServerBridge
	Auth   *server.DeviceAuth
	Remote *remotenfc.Manager
}

type stackConfig struct {
	Reader        *nfc.NFCReader
	APISecret     string
	TokenVerifier server.TokenVerifier
	RequirePaired bool
	PublicKeyPin  string

	// NoDriver omits the device driver, leaving nothing to serve a device.
	NoDriver bool
}

func newStack(t *testing.T, cfg stackConfig) *stack {
	t.Helper()

	bridge := server.NewServerBridge()
	auth := server.NewDeviceAuth(cfg.APISecret, cfg.TokenVerifier, cfg.RequirePaired)

	var remote *remotenfc.Manager
	endpoint := http.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no device driver configured", http.StatusServiceUnavailable)
	}))
	if !cfg.NoDriver {
		remote = remotenfc.NewManager(30 * time.Second)
		endpoint = remote.Handler(remotenfc.ServerOptions{
			AllowTagModification: tagModificationPolicy(cfg.Reader),
			PublicKeyPin:         cfg.PublicKeyPin,
		})
	}

	router := tagrouter.New(tagrouter.Config{Reader: cfg.Reader, Remote: remote}, bridge)

	ctx, cancel := context.WithCancel(context.Background())
	router.Start(ctx)
	if remote != nil {
		go server.PumpTagData(ctx, remote.Data(), bridge)
	}

	ts := httptest.NewServer(auth.Wrap(endpoint))

	t.Cleanup(func() {
		ts.Close()
		cancel()
		router.Stop()
		bridge.Close()
		if remote != nil {
			remote.Close()
		}
		if cfg.Reader != nil {
			cfg.Reader.Stop()
		}
	})

	return &stack{
		URL:    "ws" + strings.TrimPrefix(ts.URL, "http") + "?mode=device",
		Bridge: bridge,
		Auth:   auth,
		Remote: remote,
	}
}

// tagModificationPolicy captures the reader's mode as a predicate, so the
// driver can refuse a modifying operation the agent's mode forbids.
func tagModificationPolicy(reader *nfc.NFCReader) func() bool {
	if reader == nil {
		return nil
	}
	return func() bool { return reader.GetMode() != nfc.ModeReadOnly }
}
