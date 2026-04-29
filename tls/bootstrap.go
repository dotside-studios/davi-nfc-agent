package tls

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/buildinfo"
)

// BootstrapServer serves the CA certificate over plain HTTP for device setup.
type BootstrapServer struct {
	manager    *Manager
	port       int
	httpServer *http.Server
	logger     *log.Logger
}

// NewBootstrapServer creates a new bootstrap server for CA distribution.
func NewBootstrapServer(manager *Manager, port int) *BootstrapServer {
	return &BootstrapServer{
		manager: manager,
		port:    port,
		logger:  log.New(os.Stderr, "[bootstrap] ", log.LstdFlags),
	}
}

// Start starts the bootstrap HTTP server.
func (s *BootstrapServer) Start() error {
	mux := http.NewServeMux()

	// Serve CA certificate
	mux.HandleFunc("/ca.pem", s.handleCACert)
	mux.HandleFunc("/ca.crt", s.handleCACert) // Alternative extension

	// Serve installation instructions page
	mux.HandleFunc("/", s.handleInstructions)

	s.httpServer = &http.Server{
		Addr:    fmt.Sprintf(":%d", s.port),
		Handler: mux,
	}

	s.logger.Printf("CA Bootstrap server running on http://localhost:%d", s.port)

	// Log actual IPs
	if hosts, err := GetAllHosts(); err == nil {
		for _, h := range hosts {
			if h != "localhost" {
				s.logger.Printf("  http://%s/ca.pem", net.JoinHostPort(h, fmt.Sprintf("%d", s.port)))
			}
		}
	}

	// Log fingerprint
	if fingerprint, err := s.manager.GetCAFingerprint(); err == nil {
		s.logger.Printf("CA Fingerprint (SHA256):")
		s.logger.Printf("  %s", fingerprint)
	}

	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Printf("Bootstrap server error: %v", err)
		}
	}()

	return nil
}

// Stop stops the bootstrap server.
func (s *BootstrapServer) Stop() {
	if s.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.httpServer.Shutdown(ctx)
	}
}

// handleCACert serves the CA certificate file.
func (s *BootstrapServer) handleCACert(w http.ResponseWriter, r *http.Request) {
	caCert, err := s.manager.ReadCACert()
	if err != nil {
		http.Error(w, "CA certificate not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", "attachment; filename=\"davi-nfc-ca.pem\"")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write(caCert)

	s.logger.Printf("CA certificate downloaded by %s", r.RemoteAddr)
}

// handleInstructions serves the installation instructions page.
func (s *BootstrapServer) handleInstructions(w http.ResponseWriter, r *http.Request) {
	fingerprint, _ := s.manager.GetCAFingerprint()

	// Get local IPs for display
	hosts, _ := GetAllHosts()

	appName := buildinfo.DisplayName
	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
    <meta charset="utf-8">
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <title>%s - Install CA Certificate</title>
    <style>
        * { box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif;
            max-width: 600px;
            margin: 0 auto;
            padding: 20px;
            background: #f5f5f5;
        }
        .card {
            background: white;
            border-radius: 12px;
            padding: 24px;
            margin-bottom: 16px;
            box-shadow: 0 2px 8px rgba(0,0,0,0.1);
        }
        h1 { margin-top: 0; color: #333; }
        h2 { color: #666; font-size: 1.1em; margin-top: 24px; }
        .download-btn {
            display: inline-block;
            background: #007AFF;
            color: white;
            padding: 14px 28px;
            border-radius: 8px;
            text-decoration: none;
            font-weight: 600;
            font-size: 1.1em;
        }
        .download-btn:hover { background: #0056b3; }
        .fingerprint {
            font-family: monospace;
            font-size: 0.75em;
            background: #f0f0f0;
            padding: 12px;
            border-radius: 6px;
            word-break: break-all;
            color: #666;
        }
        .steps { padding-left: 20px; }
        .steps li { margin-bottom: 12px; line-height: 1.5; }
        .platform {
            display: inline-block;
            background: #e0e0e0;
            padding: 2px 8px;
            border-radius: 4px;
            font-weight: 600;
            font-size: 0.9em;
        }
        .warning {
            background: #fff3cd;
            border-left: 4px solid #ffc107;
            padding: 12px;
            margin: 16px 0;
            border-radius: 0 6px 6px 0;
        }
    </style>
</head>
<body>
    <div class="card">
        <h1>Install CA Certificate</h1>
        <p>To connect securely to %s, install this certificate authority on your device.</p>

        <p style="text-align: center; margin: 24px 0;">
            <a href="/ca.pem" class="download-btn">Download CA Certificate</a>
        </p>

        <div class="warning">
            <strong>Verify the fingerprint</strong> matches what's shown in the %s logs before trusting.
        </div>

        <h2>CA Fingerprint (SHA256)</h2>
        <div class="fingerprint">%s</div>
    </div>

    <div class="card">
        <h2><span class="platform">iOS</span> Installation</h2>
        <ol class="steps">
            <li>Tap the download button above</li>
            <li>Go to <strong>Settings → Profile Downloaded</strong></li>
            <li>Tap <strong>Install</strong> and enter your passcode</li>
            <li>Go to <strong>Settings → General → About → Certificate Trust Settings</strong></li>
            <li>Enable trust for "mkcert" certificate</li>
        </ol>
    </div>

    <div class="card">
        <h2><span class="platform">Android</span> Installation</h2>
        <ol class="steps">
            <li>Tap the download button above</li>
            <li>Go to <strong>Settings → Security → Encryption & credentials</strong></li>
            <li>Tap <strong>Install a certificate → CA certificate</strong></li>
            <li>Select the downloaded file</li>
            <li>Confirm installation</li>
        </ol>
    </div>

    <div class="card">
        <h2>Download URLs</h2>
        <p style="font-family: monospace; font-size: 0.9em;">
            http://%s/ca.pem<br>
%s        </p>
    </div>
</body>
</html>`, appName, appName, appName, fingerprint, net.JoinHostPort("localhost", fmt.Sprintf("%d", s.port)), formatIPLinks(hosts, s.port))

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(html))
}

// formatIPLinks formats IP addresses as HTML links.
func formatIPLinks(hosts []string, port int) string {
	portStr := fmt.Sprintf("%d", port)
	var links []string
	for _, h := range hosts {
		if h == "localhost" || h == "127.0.0.1" || h == "::1" {
			continue
		}
		// Check if it's a valid IP (not a hostname)
		if ip := net.ParseIP(h); ip != nil {
			links = append(links, fmt.Sprintf("            http://%s/ca.pem<br>", net.JoinHostPort(h, portStr)))
		}
	}
	return strings.Join(links, "\n")
}
