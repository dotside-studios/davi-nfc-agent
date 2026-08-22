// Package unifiedserver serves the device and client WebSocket endpoints from a
// single HTTP listener on one port.
//
// Historically the agent ran two servers on two ports: a device server (for NFC
// readers/phones) and a client server (for web apps). They already share the
// same WebSocket path (/ws), auth, origin, and CORS handling and communicate
// in-process by direct call -- the two ports were a conceptual
// boundary, not a technical requirement. This package collapses them onto one
// listener and routes each incoming /ws connection to the device or client
// handler based on the same discriminator the device server already used
// (remotenfc.IsDeviceConnection: the ?mode=device query param or the
// X-Device-Mode header).
package unifiedserver

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/grandcat/zeroconf"
)

// Config holds configuration for the unified server.
type Config struct {
	// Port is the single HTTP/WebSocket port to listen on.
	Port int

	// TLS configuration (optional). When both are set the listener serves
	// HTTPS/WSS.
	CertFile string
	KeyFile  string

	// MDNSServiceName is the name advertised over mDNS. Empty uses the agent's
	// own, so a program built on the agent can announce itself under its own
	// name rather than this one.
	MDNSServiceName string
}

// mdnsServiceName returns the name to advertise, falling back to the agent's.
func (c Config) mdnsServiceName() string {
	if c.MDNSServiceName != "" {
		return c.MDNSServiceName
	}
	return server.MDNSDeviceServiceName
}

// TLSEnabled returns true if TLS is configured.
func (c Config) TLSEnabled() bool {
	return c.CertFile != "" && c.KeyFile != ""
}

// Server owns the single HTTP listener and dispatches WebSocket connections to
// the device or client handler. Those retain all NFC and client-fanout logic;
// this type only fronts them with one listener, one mDNS advertisement, and
// shared health/CORS handling.
type Server struct {
	config Config
	// mux is built from the mounts at Start. Nothing is served until then, so
	// a route registered afterwards would never be reached; Mount says so
	// rather than accepting it.
	mounts []mount

	// mu guards the lifecycle fields below. Serving continues on a goroutine
	// after Start returns, so Stop overlaps it by design: without this they
	// raced on httpServer.
	mu sync.Mutex

	// started closes Mount: a route registered after the mux was built would
	// never be served. It stays set once stopped, since a stopped server is
	// started again with the routes it already has.
	started bool

	// stopped reports that the server is not serving. Read by startMDNS, which
	// registers after Start has released the lock.
	stopped    bool
	httpServer *http.Server
	ctx        context.Context
	cancel     context.CancelFunc

	mdnsServer *zeroconf.Server
}

// New creates a unified server. It serves nothing until routes are mounted.
func New(config Config) *Server {
	return &Server{config: config}
}

// mount is one route.
type mount struct {
	pattern string
	handler http.Handler
}

// Mount registers a handler at a pattern, as http.ServeMux would.
//
// Whoever mounts a route decides what stands in front of it. That is where
// authentication and CORS go, because the answer differs per route: the client
// endpoint and the health checks are called cross-origin by web apps, while the
// control API and the console are deliberately reachable only same-origin.
//
// Routes must be mounted before Start. Mounting afterwards returns an error
// rather than being accepted and never served, which is the trap the console
// used to fall into: the agent reported having one while the listener had never
// heard of it.
func (s *Server) Mount(pattern string, handler http.Handler) error {
	if pattern == "" {
		return fmt.Errorf("unifiedserver: empty mount pattern")
	}
	if handler == nil {
		return fmt.Errorf("unifiedserver: nil handler for %q", pattern)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.started {
		return fmt.Errorf("unifiedserver: cannot mount %q once started", pattern)
	}
	for _, m := range s.mounts {
		if m.pattern == pattern {
			return fmt.Errorf("unifiedserver: %q is already mounted", pattern)
		}
	}
	s.mounts = append(s.mounts, mount{pattern: pattern, handler: handler})
	return nil
}

// Start binds the listener and serves on it.
//
// It binds before returning, so a port already in use is an error the caller
// sees rather than a message in a log: the agent used to launch this on a
// goroutine and drop the error, reporting itself running with nothing
// listening. Serving continues on a goroutine until Stop.
//
// A stopped server starts again on the routes it already carries. The agent
// stops and starts one whenever the reader changes or the certificate is
// reissued, and rebuilding it there would lose whatever else was mounted on the
// port, a control center included.
func (s *Server) Start() error {
	log.Printf("[unified] Starting NFC Agent server on port %d (device + client)...", s.config.Port)

	s.mu.Lock()
	if s.httpServer != nil {
		s.mu.Unlock()
		return fmt.Errorf("unifiedserver: already serving on port %d", s.config.Port)
	}
	addr := fmt.Sprintf(":%d", s.config.Port)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		// Nothing is closed off by a start that never bound: the caller can
		// mount, fix the port and try again.
		s.mu.Unlock()
		return fmt.Errorf("unifiedserver: listen on %s: %w", addr, err)
	}

	s.started = true
	s.stopped = false

	s.ctx, s.cancel = context.WithCancel(context.Background())
	s.httpServer = &http.Server{Addr: addr, Handler: s.handlerLocked()}
	httpServer := s.httpServer
	s.mu.Unlock()

	// Reads the captured server, not the field, which Stop may already have
	// cleared.
	go func() {
		var err error
		if s.config.TLSEnabled() {
			log.Printf("[unified] Listening on :%d (TLS)", s.config.Port)
			err = httpServer.ServeTLS(listener, s.config.CertFile, s.config.KeyFile)
		} else {
			log.Printf("[unified] Listening on :%d", s.config.Port)
			err = httpServer.Serve(listener)
		}
		if err != nil && err != http.ErrServerClosed {
			log.Printf("[unified] HTTP server error: %v", err)
		}
	}()

	if err := s.startMDNS(); err != nil {
		log.Printf("[unified] Warning: Failed to start mDNS: %v", err)
	}

	return nil
}

// Handler builds the mux from the mounted routes. Exported so the routing can
// be exercised without binding a listener.
//
// A build that mounts nothing at the root gets the plain-text banner that has
// always been served there.
func (s *Server) Handler() http.Handler {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.handlerLocked()
}

// handlerLocked builds the mux with s.mu already held, which is how Start uses
// it: taking the lock again there would deadlock against itself.
func (s *Server) handlerLocked() http.Handler {
	mounts := s.mounts

	mux := http.NewServeMux()
	rooted := false
	for _, m := range mounts {
		mux.Handle(m.pattern, m.handler)
		if m.pattern == "/" {
			rooted = true
		}
	}

	if !rooted {
		mux.Handle("/", server.CORS(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("NFC Agent"))
		})))
	}

	return mux
}

// Stop stops the unified server: it shuts down mDNS, the HTTP listener, and
// cancels the shared context (which stops the device and client background
// workers running under it).
// Port is the port this server binds. It is what a client should be told to
// connect to, which is not necessarily what the agent is configured with: a
// port changed in the settings takes effect only on a fresh listener.
func (s *Server) Port() int { return s.config.Port }

func (s *Server) Stop() {
	// Take what needs shutting down under the lock, then do the shutting down
	// outside it: Shutdown blocks, and Start must not be held up behind it.
	s.mu.Lock()
	s.stopped = true
	mdns := s.mdnsServer
	s.mdnsServer = nil
	httpServer := s.httpServer
	s.httpServer = nil
	cancel := s.cancel
	s.cancel = nil
	s.mu.Unlock()

	if mdns != nil {
		mdns.Shutdown()
	}

	if httpServer != nil {
		ctx, cancelTimeout := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancelTimeout()
		_ = httpServer.Shutdown(ctx)
	}

	if cancel != nil {
		cancel()
	}
}

// startMDNS advertises the agent over mDNS for auto-discovery. It keeps the
// device service type (_nfc-device._tcp) so existing device clients continue to
// discover the agent, now on the single unified port.
func (s *Server) startMDNS() error {
	// Nothing to advertise if Stop already ran. Checked before registering as
	// well as after: registering and then immediately withdrawing trips a race
	// inside zeroconf, whose shutdown writes state its own receive goroutines
	// are still reading.
	s.mu.Lock()
	stopped := s.stopped
	s.mu.Unlock()
	if stopped {
		return nil
	}

	registered, err := zeroconf.Register(
		s.config.mdnsServiceName(),
		server.MDNSDeviceServiceType,
		server.MDNSDomain,
		s.config.Port,
		[]string{
			"version=1.0",
			"protocol=websocket",
			"path=/ws",
			"type=agent",
		},
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to register mDNS service: %w", err)
	}

	// Registration happens on the Start goroutine, which overlaps Stop. Publish
	// the handle under the lock, and withdraw it immediately if Stop already
	// ran -- otherwise the advertisement outlives the listener it points at.
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		registered.Shutdown()
		return nil
	}
	s.mdnsServer = registered
	s.mu.Unlock()
	log.Printf("[unified] mDNS service registered: %s on port %d", server.MDNSDeviceServiceType, s.config.Port)
	return nil
}
