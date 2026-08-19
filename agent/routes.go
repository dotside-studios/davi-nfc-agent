package agent

import (
	"net/http"

	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/unifiedserver"
)

// MountOn registers the agent's own routes on a server: the WebSocket endpoint
// both devices and clients connect to, and the two health checks.
//
// Called for you when Config.Server is set. Call it yourself when building the
// server by hand, before starting either.
//
// The handlers resolve what they need per request rather than capturing it, so
// they can be mounted before the agent starts and keep working across a restart
// that rebuilds the servers behind them.
//
// CORS is applied here rather than by the listener, because the answer differs
// per route: these are called cross-origin by web apps, while a control API or
// a console page mounted alongside them should not be.
func (a *Agent) MountOn(srv *unifiedserver.Server) error {
	if err := srv.Mount("/ws", server.CORS(a.wsHandler())); err != nil {
		return err
	}
	if err := srv.Mount("/health", server.CORS(a.healthHandler())); err != nil {
		return err
	}
	return srv.Mount("/api/v1/health", server.CORS(a.healthHandler()))
}

// wsHandler routes a connection to the device driver or the client server by
// the mode it declares.
func (a *Agent) wsHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		client, device := a.endpoints()
		if client == nil {
			http.Error(w, "agent is not running", http.StatusServiceUnavailable)
			return
		}

		byMode := map[string]http.Handler{}
		if device != nil {
			byMode[server.ModeDevice] = device
		}
		server.RouteByMode(client, byMode).ServeHTTP(w, r)
	})
}

// endpoints reads the handlers the running agent is serving from, which the
// lifecycle replaces on every start.
//
// Read without a lock, deliberately. Stop holds the lifecycle lock while
// http.Server.Shutdown waits for in-flight requests to finish, so a handler
// that took that lock would wait for a Stop that was waiting for it.
func (a *Agent) endpoints() (client, device http.Handler) {
	e := a.serving.Load()
	if e == nil {
		return nil, nil
	}
	return e.client, e.device
}

// endpoints is the pair the routes dispatch to, swapped as a whole so a request
// never sees a client from one start and a device from the next.
type endpoints struct {
	client http.Handler
	device http.Handler
}

// buildDeviceEndpoint makes the driver's handler, behind the agent's credential
// check. Nil when no driver serves devices.
func (a *Agent) buildDeviceEndpoint(remote *remotenfc.Manager) http.Handler {
	if remote == nil {
		return nil
	}
	return remote.Handler(remotenfc.ServerOptions{
		Authenticate:         a.DeviceAuth.Check,
		CheckOrigin:          a.checkOrigin(),
		AllowTagModification: a.tagModificationPolicy(),
		PublicKeyPin:         a.publicKeyPin,
	})
}
