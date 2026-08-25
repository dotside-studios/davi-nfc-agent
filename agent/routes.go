package agent

import (
	"net/http"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

// Route is one of the agent's own routes, for whatever serves it to mount.
type Route struct {
	Pattern string
	Handler http.Handler
}

// Routes are the agent's own: the WebSocket endpoint both devices and clients
// connect to, and the two health checks. Whatever serves the agent mounts
// these, and should mount them before anything of its own, so that nothing can
// displace them.
//
// The handlers resolve what they need per request rather than capturing it, so
// they can be mounted before the agent starts and keep working across a restart
// that rebuilds the servers behind them.
//
// CORS is applied here rather than by the listener, because the answer differs
// per route: these are called cross-origin by web apps, while a control API or
// a console page mounted alongside them should not be.
func (a *Agent) Routes() []Route {
	return []Route{
		{Pattern: "/ws", Handler: server.CORS(a.wsHandler())},
		{Pattern: "/health", Handler: server.CORS(a.healthHandler())},
		{Pattern: "/api/v1/health", Handler: server.CORS(a.healthHandler())},
	}
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

// DeviceEndpointOptions is what the agent decides about device connections,
// handed to whatever builds the endpoint. It mirrors the driver's own options
// without naming them.
type DeviceEndpointOptions struct {
	// Authenticate admits or rejects a device, writing its own rejection.
	Authenticate func(w http.ResponseWriter, r *http.Request) bool

	// CheckOrigin admits or rejects an upgrade by Origin.
	CheckOrigin func(r *http.Request) bool

	// AllowTagModification reports whether writes, locks and raw exchanges are
	// currently permitted. Read-only mode gates every route to a tag, not just
	// the hardware one.
	AllowTagModification func() bool

	// PublicKeyPin is reported at registration so a device can recognise this
	// agent later without a certificate authority.
	//
	// Asked for when a device registers rather than when the endpoint is
	// built, like the three above: the pin comes from certificate material,
	// which need not be settled by the time the endpoint exists.
	PublicKeyPin func() string
}

// TagModificationAllowed reports whether the agent's mode currently permits
// writes, locks and raw exchanges.
//
// Read when the operation happens rather than when the endpoint was built: the
// endpoint outlives any particular reader, and the mode changes while running.
func (a *Agent) TagModificationAllowed() bool {
	reader := a.reader.Load()
	if reader == nil {
		return true
	}
	return reader.GetMode() != nfc.ModeReadOnly
}
