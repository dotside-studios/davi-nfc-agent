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

	// ControlHandler serves the control center's privileged API under
	// /control/. It is mounted ahead of everything else and, unlike the client
	// and device endpoints, is deliberately not wrapped in CORS: it administers
	// the agent rather than serving applications, so no other origin has any
	// business calling it. Nil disables the control surface entirely.
	ControlHandler http.Handler

	// UIHandler serves the control center's static assets at the root. Nil
	// leaves the root as the plain-text agent banner.
	UIHandler http.Handler

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
// the device or client handler. The underlying deviceserver/clientserver
// instances retain all NFC and client-fanout logic; this type only fronts them
// with one listener, one mDNS advertisement, and shared health/CORS handling.
type Server struct {
	config Config
	device *deviceserver.Server
	client *clientserver.Server

	// mu guards the lifecycle fields below. Start runs on its own goroutine
	// and blocks until Stop cancels it, so the two overlap by design: without
	// this they raced on httpServer, and a Stop arriving before Start had
	// finished binding could miss the listener entirely.
	mu         sync.Mutex
	stopped    bool
	httpServer *http.Server
	ctx        context.Context
	cancel     context.CancelFunc

	mdnsServer *zeroconf.Server
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

	s.mu.Lock()
	if s.stopped {
		// Stop landed first. Binding now would leave a listener nobody holds.
		s.mu.Unlock()
		log.Printf("[unified] Stopped before startup completed; not binding")
		return nil
	}

	s.ctx, s.cancel = context.WithCancel(context.Background())
	ctx := s.ctx

	// Start device and client background work under the shared context. Neither
	// binds a listener; this server owns the single listener below.
	s.device.StartBackground(ctx)
	s.client.StartBackground(ctx)

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.config.Port),
		Handler: s.Handler(),
	}
	httpServer := s.httpServer
	s.mu.Unlock()

	// Start HTTP server in goroutine. It reads the captured server, not the
	// field, which Stop may already have cleared.
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
	if err := s.startMDNS(); err != nil {
		log.Printf("[unified] Warning: Failed to start mDNS: %v", err)
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

	// Control center API. Registered first and without the CORS wrapper: these
	// routes are privileged, and the permissive headers the client endpoints
	// need would invite a browser to call them cross-site and read the replies.
	if s.config.ControlHandler != nil {
		mux.Handle("/control/", s.config.ControlHandler)
	}

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

	// Root: the control center when it is built in, otherwise the banner that
	// has always been here.
	//
	// The UI is served without CORS for the same reason the control API is. It
	// is a page, not an API — nothing should be embedding or fetching it from
	// another origin.
	if s.config.UIHandler != nil {
		mux.Handle("/", s.config.UIHandler)
	} else {
		mux.HandleFunc("/", enableCORS(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte("NFC Agent"))
		}))
	}

	return mux
}

// Stop stops the unified server: it shuts down mDNS, the HTTP listener, and
// cancels the shared context (which stops the device and client background
// workers running under it).
func (s *Server) Stop() {
	// Take what needs shutting down under the lock, then do the shutting down
	// outside it: Shutdown blocks, and Start must not be held up behind it.
	s.mu.Lock()
	s.stopped = true
	mdns := s.mdnsServer
	s.mdnsServer = nil
	httpServer := s.httpServer
	cancel := s.cancel
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
