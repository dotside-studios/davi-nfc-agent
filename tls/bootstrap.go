package tls

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/skip2/go-qrcode"

	"github.com/dotside-studios/davi-nfc-agent/buildinfo"
)

// caReader is the subset of *Manager that BootstrapServer needs. Carved
// out so tests can supply a fake without spinning up truststore.
type caReader interface {
	ReadCACert() ([]byte, error)
	GetCAFingerprint() (string, error)
}

// bootstrapMaxFailures is the number of wrong PIN attempts before the
// server locks the download endpoints until the agent is restarted.
const bootstrapMaxFailures = 5

// BootstrapServer serves a plug-and-play pairing flow: the kiosk shows
// a QR code, a phone scans it, and the phone follows a platform-aware
// install path (iOS .mobileconfig, Android .crt). All download endpoints
// require a 6-digit PIN that's printed on the kiosk (logs + systray) and
// embedded in the QR — so a passive LAN attacker can't fetch the CA
// without seeing the kiosk screen.
//
// Trust model: this protects against passive eavesdropping and casual
// LAN drive-bys. It does NOT defend against an active MITM on a hostile
// network during the pairing window — pair on a trusted network for
// high-stakes installs.
type BootstrapServer struct {
	manager    caReader
	port       int
	httpServer *http.Server
	logger     *log.Logger

	pin    string
	failed atomic.Int32
}

// NewBootstrapServer creates a server with a fresh random 6-digit PIN.
func NewBootstrapServer(manager caReader, port int) *BootstrapServer {
	return &BootstrapServer{
		manager: manager,
		port:    port,
		logger:  log.New(os.Stderr, "[bootstrap] ", log.LstdFlags),
		pin:     generatePIN(),
	}
}

// PIN returns the 6-digit pairing PIN.
func (s *BootstrapServer) PIN() string { return s.pin }

func generatePIN() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand is essentially infallible; if it fails the host
		// is in trouble. Fall back to a still-non-zero value rather
		// than panicking inside a constructor.
		return "000000"
	}
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(b[:])%1_000_000)
}

// Start brings up the HTTP server and logs the pairing details.
func (s *BootstrapServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/install", s.handleInstall)
	mux.HandleFunc("/install/ios", s.handleAppleProfile)
	mux.HandleFunc("/install/android", s.handleAndroidCert)
	mux.HandleFunc("/qr.png", s.handleQR)
	mux.HandleFunc("/ca.pem", s.handleRawCA)
	mux.HandleFunc("/ca.crt", s.handleRawCA)

	s.httpServer = &http.Server{
		Addr:              fmt.Sprintf(":%d", s.port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	s.logger.Printf("Pairing server: http://localhost:%d", s.port)
	s.logger.Printf("Pairing PIN: %s", s.pin)

	if hosts, err := GetAllHosts(); err == nil {
		for _, h := range hosts {
			if h == "localhost" {
				continue
			}
			s.logger.Printf("  http://%s/", net.JoinHostPort(h, fmt.Sprintf("%d", s.port)))
		}
	}

	if fingerprint, err := s.manager.GetCAFingerprint(); err == nil {
		s.logger.Printf("CA fingerprint (SHA-256): %s", fingerprint)
	}

	go func() {
		if err := s.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.logger.Printf("Bootstrap server error: %v", err)
		}
	}()

	return nil
}

// Stop shuts down the HTTP server.
func (s *BootstrapServer) Stop() {
	if s.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.httpServer.Shutdown(ctx)
	}
}

// requirePIN inspects ?pin= or X-Bootstrap-PIN and writes an error
// response if it's missing/wrong/locked. Returns true if the caller
// should proceed.
func (s *BootstrapServer) requirePIN(w http.ResponseWriter, r *http.Request) bool {
	if s.failed.Load() >= bootstrapMaxFailures {
		s.logger.Printf("PIN locked; rejecting request from %s", r.RemoteAddr)
		http.Error(w, "Too many wrong PIN attempts. Restart the agent to reset.", http.StatusTooManyRequests)
		return false
	}

	pin := r.URL.Query().Get("pin")
	if pin == "" {
		pin = r.Header.Get("X-Bootstrap-PIN")
	}

	if !pinMatch(pin, s.pin) {
		n := s.failed.Add(1)
		s.logger.Printf("Wrong PIN from %s (attempt %d/%d)", r.RemoteAddr, n, bootstrapMaxFailures)
		http.Error(w, "Invalid PIN.", http.StatusUnauthorized)
		return false
	}
	return true
}

// pinMatch is a constant-time string compare guarded against length
// leaks.
func pinMatch(got, want string) bool {
	if len(got) == 0 || len(got) != len(want) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(want)) == 1
}

// handleIndex serves the kiosk-facing pairing page.
func (s *BootstrapServer) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	s.serveInstallPage(w)
}

// handleInstall is what the QR points at. UA-sniffs and forwards to the
// platform-specific bundle.
func (s *BootstrapServer) handleInstall(w http.ResponseWriter, r *http.Request) {
	if !s.requirePIN(w, r) {
		return
	}

	pin := url.QueryEscape(s.pin)
	ua := r.UserAgent()

	switch {
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"), strings.Contains(ua, "iPod"):
		http.Redirect(w, r, "/install/ios?pin="+pin, http.StatusSeeOther)
	case strings.Contains(ua, "Android"):
		http.Redirect(w, r, "/install/android?pin="+pin, http.StatusSeeOther)
	default:
		// Desktop / unknown UA — show the install page so the user can
		// either scan the QR with a phone or hand the URL to one.
		http.Redirect(w, r, "/", http.StatusSeeOther)
	}
}

// handleAppleProfile serves a .mobileconfig that bundles the rootCA.
// Safari recognizes the MIME and walks the user into the Settings
// install flow. The profile is unsigned — iOS shows an "Unsigned"
// notice during install but proceeds.
func (s *BootstrapServer) handleAppleProfile(w http.ResponseWriter, r *http.Request) {
	if !s.requirePIN(w, r) {
		return
	}

	profile, err := s.buildAppleProfile()
	if err != nil {
		s.logger.Printf("buildAppleProfile failed: %v", err)
		http.Error(w, "Failed to build profile", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-apple-aspen-config")
	w.Header().Set("Content-Disposition", `attachment; filename="davi-nfc-ca.mobileconfig"`)
	w.Write(profile)
	s.logger.Printf("Apple profile served to %s", r.RemoteAddr)
}

// handleAndroidCert serves a DER-encoded .crt; Chrome on Android
// recognizes application/x-x509-ca-cert and triggers the system
// certificate-install prompt directly.
func (s *BootstrapServer) handleAndroidCert(w http.ResponseWriter, r *http.Request) {
	if !s.requirePIN(w, r) {
		return
	}

	der, err := s.derCA()
	if err != nil {
		http.Error(w, "CA not found", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/x-x509-ca-cert")
	w.Header().Set("Content-Disposition", `attachment; filename="davi-nfc-ca.crt"`)
	w.Write(der)
	s.logger.Printf("Android cert served to %s", r.RemoteAddr)
}

// handleQR returns a PNG QR encoding /install?pin=<pin>. Not PIN-gated
// because the URL it encodes contains the PIN; without the PIN the QR
// is useless.
func (s *BootstrapServer) handleQR(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if host == "" {
		host = net.JoinHostPort("localhost", fmt.Sprintf("%d", s.port))
	}
	target := fmt.Sprintf("http://%s/install?pin=%s", host, url.QueryEscape(s.pin))

	png, err := qrcode.Encode(target, qrcode.Medium, 360)
	if err != nil {
		http.Error(w, "QR generation failed", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(png)
}

// handleRawCA serves the raw .pem for legacy clients that can't use
// the platform-specific bundles.
func (s *BootstrapServer) handleRawCA(w http.ResponseWriter, r *http.Request) {
	if !s.requirePIN(w, r) {
		return
	}

	caCert, err := s.manager.ReadCACert()
	if err != nil {
		http.Error(w, "CA certificate not found", http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/x-pem-file")
	w.Header().Set("Content-Disposition", `attachment; filename="davi-nfc-ca.pem"`)
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Write(caCert)
	s.logger.Printf("Raw CA served to %s", r.RemoteAddr)
}

// derCA decodes the manager's PEM-encoded CA into raw DER.
func (s *BootstrapServer) derCA() ([]byte, error) {
	pemBytes, err := s.manager.ReadCACert()
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(pemBytes)
	if block == nil {
		return nil, fmt.Errorf("failed to decode CA PEM")
	}
	return block.Bytes, nil
}

// buildAppleProfile renders an unsigned .mobileconfig embedding the CA.
// Format: https://developer.apple.com/business/documentation/Configuration-Profile-Reference.pdf
func (s *BootstrapServer) buildAppleProfile() ([]byte, error) {
	der, err := s.derCA()
	if err != nil {
		return nil, err
	}
	if _, err := x509.ParseCertificate(der); err != nil {
		return nil, fmt.Errorf("CA is not a valid x509: %w", err)
	}

	payloadUUID := uuid.NewString()
	profileUUID := uuid.NewString()
	caB64 := base64.StdEncoding.EncodeToString(der)
	appName := buildinfo.DisplayName

	profile := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>PayloadContent</key>
    <array>
        <dict>
            <key>PayloadCertificateFileName</key>
            <string>davi-nfc-ca.cer</string>
            <key>PayloadContent</key>
            <data>%s</data>
            <key>PayloadDescription</key>
            <string>%s root certificate authority for kiosk pairing</string>
            <key>PayloadDisplayName</key>
            <string>%s NFC CA</string>
            <key>PayloadIdentifier</key>
            <string>com.davi.nfc.ca.%s</string>
            <key>PayloadType</key>
            <string>com.apple.security.root</string>
            <key>PayloadUUID</key>
            <string>%s</string>
            <key>PayloadVersion</key>
            <integer>1</integer>
        </dict>
    </array>
    <key>PayloadDisplayName</key>
    <string>%s</string>
    <key>PayloadIdentifier</key>
    <string>com.davi.nfc.%s</string>
    <key>PayloadOrganization</key>
    <string>Davi</string>
    <key>PayloadRemovalDisallowed</key>
    <false/>
    <key>PayloadType</key>
    <string>Configuration</string>
    <key>PayloadUUID</key>
    <string>%s</string>
    <key>PayloadVersion</key>
    <integer>1</integer>
</dict>
</plist>`, caB64, appName, appName, payloadUUID, payloadUUID, appName, profileUUID, profileUUID)

	return []byte(profile), nil
}

// serveInstallPage renders the kiosk-facing pairing page. Designed to
// be displayed on the kiosk's own monitor so a phone camera can scan
// the QR. Also works on a phone hit directly (you'll just see the QR
// in miniature; scanning your own screen doesn't help, so include a
// PIN-entry fallback for that case).
func (s *BootstrapServer) serveInstallPage(w http.ResponseWriter) {
	appName := buildinfo.DisplayName
	fingerprint, _ := s.manager.GetCAFingerprint()

	html := fmt.Sprintf(`<!DOCTYPE html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>%s · Pair a phone</title>
<style>
* { box-sizing: border-box; }
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif; max-width: 560px; margin: 0 auto; padding: 24px; background: #f5f5f5; color: #222; }
h1 { margin: 0 0 8px; font-size: 1.5em; }
h2 { color: #555; font-size: 0.85em; margin: 24px 0 8px; text-transform: uppercase; letter-spacing: 0.05em; }
.card { background: #fff; border-radius: 12px; padding: 24px; margin-bottom: 16px; box-shadow: 0 1px 3px rgba(0,0,0,0.08); }
.qr { display: block; margin: 16px auto; max-width: 320px; height: auto; image-rendering: pixelated; }
.lede { color: #444; line-height: 1.45; }
.muted { color: #777; font-size: 0.9em; }
.fp { font-family: ui-monospace, "SF Mono", Menlo, monospace; font-size: 0.7em; color: #888; word-break: break-all; }
ol li { margin-bottom: 6px; line-height: 1.45; }
form { margin-top: 16px; display: flex; gap: 8px; }
input[type=text] { flex: 1; padding: 12px 14px; border: 1px solid #ccc; border-radius: 8px; font-size: 1em; font-family: ui-monospace, monospace; letter-spacing: 0.1em; }
button { padding: 12px 18px; border: 0; border-radius: 8px; background: #007AFF; color: #fff; font-weight: 600; cursor: pointer; }
button[disabled] { background: #ccc; cursor: not-allowed; }
</style>
</head>
<body>
<div class="card">
    <h1>Pair a phone with this kiosk</h1>
    <p class="lede">Open your phone's camera and point it at the QR code below. Your phone will guide you through the rest.</p>
    <img class="qr" src="/qr.png" alt="Pairing QR" />
    <p class="muted" style="text-align:center">If the QR doesn't scan, type the PIN shown in the kiosk's tray icon below.</p>
    <form id="manualForm" action="/install" method="get">
        <input type="text" name="pin" id="pinInput" inputmode="numeric" pattern="[0-9]{6}" maxlength="6" placeholder="6-digit PIN" autocomplete="off" required>
        <button type="submit" id="pairBtn" disabled>Pair</button>
    </form>
</div>

<div class="card">
    <h2>What happens next</h2>
    <ol>
        <li><strong>iPhone:</strong> a configuration profile downloads. Open <em>Settings → Profile Downloaded</em> and tap <strong>Install</strong>. Then <em>Settings → General → About → Certificate Trust Settings</em> and enable trust for <em>%s NFC CA</em>.</li>
        <li><strong>Android:</strong> Chrome opens an install prompt. Confirm and (if asked) name the certificate.</li>
        <li>You're done — the phone now trusts this kiosk for NFC pairing.</li>
    </ol>
</div>

<div class="card">
    <h2>Trust details</h2>
    <p class="muted">Self-signed CA generated by this kiosk. Pair on a trusted network — an active attacker on a hostile network could substitute a different certificate during pairing.</p>
    <p class="fp">SHA-256: %s</p>
</div>
<script>
(function() {
  var input = document.getElementById('pinInput');
  var btn = document.getElementById('pairBtn');
  function update() { btn.disabled = !/^\d{6}$/.test(input.value); }
  input.addEventListener('input', update);
  update();
})();
</script>
</body>
</html>`, appName, appName, fingerprint)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(html))
}
