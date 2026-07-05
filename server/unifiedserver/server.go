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

	s.ctx, s.cancel = context.WithCancel(context.Background())

	// Start device and client background work under the shared context. Neither
	// binds a listener; this server owns the single listener below.
	s.device.StartBackground(s.ctx)
	s.client.StartBackground(s.ctx)

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.config.Port),
		Handler: s.Handler(),
	}

	// Start HTTP server in goroutine.
	go func() {
		var err error
		if s.config.TLSEnabled() {
			log.Printf("[unified] Listening on :%d (TLS)", s.config.Port)
			err = s.httpServer.ListenAndServeTLS(s.config.CertFile, s.config.KeyFile)
		} else {
			log.Printf("[unified] Listening on :%d", s.config.Port)
			err = s.httpServer.ListenAndServe()
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
	<-s.ctx.Done()
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
		json.NewEncoder(w).Encode(map[string]interface{}{
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
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":    "ok",
			"type":      "agent",
			"timestamp": time.Now().Format("2006-01-02T15:04:05Z07:00"),
			"clients":   s.client.ClientCount(),
		})
	}))

	// Root
	mux.HandleFunc("/", enableCORS(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("NFC Agent"))
	}))

	return mux
}

// Stop stops the unified server: it shuts down mDNS, the HTTP listener, and
// cancels the shared context (which stops the device and client background
// workers running under it).
func (s *Server) Stop() {
	if s.mdnsServer != nil {
		s.mdnsServer.Shutdown()
		s.mdnsServer = nil
	}

	if s.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.httpServer.Shutdown(ctx)
	}

	if s.cancel != nil {
		s.cancel()
	}
}

// startMDNS advertises the agent over mDNS for auto-discovery. It keeps the
// device service type (_nfc-device._tcp) so existing device clients continue to
// discover the agent, now on the single unified port.
func (s *Server) startMDNS() error {
	var err error
	s.mdnsServer, err = zeroconf.Register(
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
		return fmt.Errorf("failed to register mDNS service: %w", err)
	}
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
