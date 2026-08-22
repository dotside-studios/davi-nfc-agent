// Package unifiedserver serves the device and client WebSocket endpoints from a
// single HTTP listener on one port.
//
// Historically the agent ran two servers on two ports: a device server (for NFC
// readers/phones) and a client server (for web apps). They already share the
// same WebSocket path (/ws), auth, origin, and CORS handling and communicate
// in-process through a server.ServerBridge — the two ports were a conceptual
// boundary, not a technical requirement. This package collapses them onto one
// listener and routes each incoming /ws connection to the device or client
// handler based on the same discriminator the device server already used
// (deviceserver.IsDeviceConnection: the ?mode=device query param or the
// X-Device-Mode header).
package unifiedserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/clientserver"
	"github.com/dotside-studios/davi-nfc-agent/server/deviceserver"
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

	// Mounts are paths served on this listener on someone else's behalf: the
	// control center's privileged API, its console, or a plugin with a page of
	// its own, which then needs no port, no certificate and no trust of its own
	// and is reachable wherever the agent already is.
	//
	// They are deliberately not wrapped in CORS, unlike the device and client
	// endpoints. A mount is a page or an administrative API rather than
	// something applications call, so no other origin has any business
	// fetching it and reading the reply.
	//
	// A mount asking for a path this server serves itself is refused rather
	// than replacing it: an agent whose /ws answers something other than the
	// WebSocket endpoint is a broken agent, however it was configured. The one
	// exception is the root, which is the agent's banner only while nothing
	// else wants it.
	Mounts []Mount

	// Logf reports a refused mount. Nil means the standard logger.
	Logf func(format string, args ...any)
}

// Mount is one path served on another's behalf.
type Mount struct {
	// Pattern is an http.ServeMux pattern, so a trailing slash takes the whole
	// subtree.
	Pattern string

	// Handler answers it.
	Handler http.Handler

	// Owner names who asked for it, for the log line when it cannot be given.
	Owner string
}

// TLSEnabled returns true if TLS is configured.
func (c Config) TLSEnabled() bool {
	return c.CertFile != "" && c.KeyFile != ""
}

// Server owns the single HTTP listener and dispatches WebSocket connections to
// the device or client handler. The underlying deviceserver/clientserver
// instances retain all NFC and client-fanout logic; this type only fronts them
// with one listener, one mDNS advertisement, and shared health/CORS handling.
type Server struct {
	config Config
	device *deviceserver.Server
	client *clientserver.Server

	// mu guards what Start builds and Stop takes down. The two run on
	// different goroutines by design — Start blocks until Stop cancels it — so
	// a stop arriving while the listener is still being built has to find
	// either all of it or none of it.
	mu         sync.Mutex
	httpServer *http.Server
	cancel     context.CancelFunc
	mdnsServer *zeroconf.Server
	stopped    bool
}

// New creates a unified server fronting the given device and client servers.
func New(config Config, device *deviceserver.Server, client *clientserver.Server) *Server {
	return &Server{
		config: config,
		device: device,
		client: client,
	}
}

// Start starts the unified server. It launches the device and client background
// workers, binds a single HTTP listener, advertises the service over mDNS, and
// blocks until the server context is cancelled (via Stop).
func (s *Server) Start() error {
	log.Printf("[unified] Starting NFC Agent server on port %d (device + client)...", s.config.Port)

	ctx, cancel := context.WithCancel(context.Background())

	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", s.config.Port),
		Handler: s.Handler(),
	}

	s.mu.Lock()
	if s.stopped {
		// Stopped before it was up. Nothing has been bound yet, so there is
		// nothing to take down but the context.
		s.mu.Unlock()
		cancel()
		return nil
	}
	s.httpServer, s.cancel = httpServer, cancel
	s.mu.Unlock()

	// Start device and client background work under the shared context. Neither
	// binds a listener; this server owns the single listener below.
	s.device.StartBackground(ctx)
	s.client.StartBackground(ctx)

	// Start HTTP server in goroutine.
	go func() {
		var err error
		if s.config.TLSEnabled() {
			log.Printf("[unified] Listening on :%d (TLS)", s.config.Port)
			err = httpServer.ListenAndServeTLS(s.config.CertFile, s.config.KeyFile)
		} else {
			log.Printf("[unified] Listening on :%d", s.config.Port)
			err = httpServer.ListenAndServe()
		}
		if err != nil && err != http.ErrServerClosed {
			log.Printf("[unified] HTTP server error: %v", err)
		}
	}()

	// Advertise over mDNS on the single port.
	if mdns, err := s.startMDNS(); err != nil {
		log.Printf("[unified] Warning: Failed to start mDNS: %v", err)
	} else {
		s.mu.Lock()
		if s.stopped {
			// Stopped while it was being registered, so its shutdown is this
			// goroutine's to do.
			s.mu.Unlock()
			mdns.Shutdown()
		} else {
			s.mdnsServer = mdns
			s.mu.Unlock()
		}
	}

	// Block until shutdown.
	<-ctx.Done()
	log.Printf("[unified] Server context cancelled, shutting down...")

	return nil
}

// Handler builds the HTTP routes for the unified server: a single /ws endpoint
// that dispatches to the device or client handler, both health endpoints, and
// the root. It is exported so the routing can be exercised in tests without
// binding a listener.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// Single WebSocket endpoint. Device connections (?mode=device or the
	// X-Device-Mode header) route to the device handler; everything else routes
	// to the client handler. Each handler performs its own API-secret and origin
	// checks, so auth is not duplicated here.
	mux.HandleFunc("/ws", enableCORS(func(w http.ResponseWriter, r *http.Request) {
		if deviceserver.IsDeviceConnection(r) {
			s.device.ServeWS(w, r)
			return
		}
		s.client.ServeWS(w, r)
	}))

	// Device-style health check (kept for backward compatibility).
	mux.HandleFunc("/health", enableCORS(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "ok",
			"type":    "agent",
			"clients": s.client.ClientCount(),
		})
	}))

	// Client-style health check (kept for backward compatibility).
	mux.HandleFunc("/api/v1/health", enableCORS(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodOptions {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "ok",
			"type":      "agent",
			"timestamp": time.Now().Format("2006-01-02T15:04:05Z07:00"),
			"clients":   s.client.ClientCount(),
		})
	}))

	// What was mounted on this listener's behalf. Checked against the routes
	// above: http.ServeMux panics on a duplicate pattern, which would take the
	// agent down at startup over one plugin's typo.
	taken := map[string]bool{"/ws": true, "/health": true, "/api/v1/health": true}
	var root http.Handler
	for _, mount := range s.config.Mounts {
		if mount.Pattern == "" || mount.Handler == nil {
			continue
		}
		if taken[mount.Pattern] {
			s.logf("unifiedserver: %s cannot serve %s: the agent serves that itself", mountOwner(mount), mount.Pattern)
			continue
		}
		taken[mount.Pattern] = true

		// The root is claimed rather than registered, so the banner below can
		// stand down for it.
		if mount.Pattern == "/" {
			root = mount.Handler
			continue
		}
		mux.Handle(mount.Pattern, mount.Handler)
	}

	// Root: whoever claimed it — the control center, where it is built in —
	// otherwise the banner that has always been here.
	if root != nil {
		mux.Handle("/", root)
	} else {
		mux.HandleFunc("/", enableCORS(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("NFC Agent"))
		}))
	}

	return mux
}

// logf reports something worth knowing about the routing.
func (s *Server) logf(format string, args ...any) {
	if s.config.Logf != nil {
		s.config.Logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// mountOwner names who asked for a mount, for a log line that says where to go
// and fix it.
func mountOwner(mount Mount) string {
	if mount.Owner == "" {
		return "a plugin"
	}
	return mount.Owner
}

// Stop stops the unified server: it shuts down mDNS, the HTTP listener, and
// cancels the shared context (which stops the device and client background
// workers running under it).
// Port is the port this server binds. It is what a client should be told to
// connect to, which is not necessarily what the agent is configured with: a
// port changed in the settings takes effect only on a fresh listener.
func (s *Server) Port() int { return s.config.Port }

func (s *Server) Stop() {
	s.mu.Lock()
	// Marked first, so a Start still on its way up finds this and takes itself
	// back down rather than binding after the stop.
	s.stopped = true
	mdns, httpServer, cancel := s.mdnsServer, s.httpServer, s.cancel
	s.mdnsServer, s.httpServer = nil, nil
	s.mu.Unlock()

	if mdns != nil {
		mdns.Shutdown()
	}

	if httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(ctx)
	}

	if cancel != nil {
		cancel()
	}
}

// startMDNS advertises the agent over mDNS for auto-discovery. It keeps the
// device service type (_nfc-device._tcp) so existing device clients continue to
// discover the agent, now on the single unified port.
func (s *Server) startMDNS() (*zeroconf.Server, error) {
	mdns, err := zeroconf.Register(
		server.MDNSDeviceServiceName,
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
		return nil, fmt.Errorf("failed to register mDNS service: %w", err)
	}
	log.Printf("[unified] mDNS service registered: %s on port %d", server.MDNSDeviceServiceType, s.config.Port)
	return mdns, nil
}

// enableCORS adds permissive CORS headers, matching the device/client handlers'
// previous behavior.
func enableCORS(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", server.CORSAllowOrigin)
		w.Header().Set("Access-Control-Allow-Methods", server.CORSAllowMethods)
		w.Header().Set("Access-Control-Allow-Headers", server.CORSAllowHeaders)

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next(w, r)
	}
}
