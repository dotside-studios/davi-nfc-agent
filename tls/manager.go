package tls

import (
	"bufio"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jittering/truststore"
)

// Manager handles automatic TLS certificate generation and trust store installation.
type Manager struct {
	configDir  string
	tlsDir     string
	caDir      string
	caCertFile string
	certFile   string
	keyFile    string
	hostsFile  string
	logger     *log.Logger

	// Network change watching. mu guards the three fields below.
	mu                sync.Mutex
	networkChangeChan chan struct{}
	stopWatchChan     chan struct{}
	lastHosts         []string
}

// NewManager creates a new TLS manager with the given config directory.
func NewManager(configDir string) *Manager {
	tlsDir := filepath.Join(configDir, "tls")
	caDir := filepath.Join(configDir, "ca")
	return &Manager{
		configDir:  configDir,
		tlsDir:     tlsDir,
		caDir:      caDir,
		caCertFile: filepath.Join(caDir, "rootCA.pem"),
		certFile:   filepath.Join(tlsDir, "server.crt"),
		keyFile:    filepath.Join(tlsDir, "server.key"),
		hostsFile:  filepath.Join(tlsDir, "hosts.txt"),
		logger:     log.New(os.Stderr, "[tls] ", log.LstdFlags),
	}
}

// EnsureCertificates checks and generates certificates as needed.
// Returns cert and key file paths, or error.
// Installs CA if not already trusted (may prompt user for password).
func (m *Manager) EnsureCertificates() (certFile, keyFile string, err error) {
	// Ensure TLS directory exists with restrictive permissions.
	if err := os.MkdirAll(m.tlsDir, 0700); err != nil {
		return "", "", fmt.Errorf("failed to create TLS directory: %w", err)
	}
	if err := secureDir(m.tlsDir); err != nil {
		m.logger.Printf("Warning: failed to lock down TLS directory permissions: %v", err)
	}

	// Get current hosts
	hosts, err := GetAllHosts()
	if err != nil {
		m.logger.Printf("Warning: failed to get LAN IPs: %v", err)
		hosts = []string{"localhost", "127.0.0.1"}
	}

	m.logger.Printf("Hosts for certificate: %v", hosts)

	// Check if we need to generate/regenerate certificates
	needsRegeneration := false

	if !m.certsExist() {
		m.logger.Println("Certificates not found, generating...")
		needsRegeneration = true
	} else if m.hostsChanged(hosts) {
		m.logger.Println("Network configuration changed, regenerating certificates...")
		needsRegeneration = true
	}

	if needsRegeneration {
		if err := m.generateCertificates(hosts); err != nil {
			return "", "", err
		}
	} else {
		m.logger.Println("Using existing certificates")
	}

	return m.certFile, m.keyFile, nil
}

// certsExist checks if both certificate files exist.
func (m *Manager) certsExist() bool {
	_, certErr := os.Stat(m.certFile)
	_, keyErr := os.Stat(m.keyFile)
	return certErr == nil && keyErr == nil
}

// hostsChanged checks if current hosts differ from cached hosts on disk.
// Used at startup to decide whether to regenerate; the runtime watcher
// compares against in-memory state instead.
func (m *Manager) hostsChanged(hosts []string) bool {
	cachedHosts, err := m.readCachedHosts()
	if err != nil {
		return true // If we can't read cached hosts, assume they changed
	}
	return !sameHosts(cachedHosts, hosts)
}

// sameHosts reports whether two host slices contain the same entries
// (order-independent). Inputs are not mutated.
func sameHosts(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sa := append([]string(nil), a...)
	sb := append([]string(nil), b...)
	sort.Strings(sa)
	sort.Strings(sb)
	for i := range sa {
		if sa[i] != sb[i] {
			return false
		}
	}
	return true
}

// readCachedHosts reads the cached hosts from file.
func (m *Manager) readCachedHosts() ([]string, error) {
	file, err := os.Open(m.hostsFile)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var hosts []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		host := strings.TrimSpace(scanner.Text())
		if host != "" {
			hosts = append(hosts, host)
		}
	}

	return hosts, scanner.Err()
}

// writeCachedHosts writes the hosts to cache file with owner-only permissions.
func (m *Manager) writeCachedHosts(hosts []string) error {
	file, err := os.OpenFile(m.hostsFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer file.Close()

	for _, host := range hosts {
		fmt.Fprintln(file, host)
	}

	// Re-apply restrictive ACL on Windows where the mode bits are advisory.
	if err := secureFile(m.hostsFile); err != nil {
		m.logger.Printf("Warning: failed to lock down hosts file permissions: %v", err)
	}
	return nil
}

// generateCertificates generates new certificates using truststore.
func (m *Manager) generateCertificates(hosts []string) error {
	// Set CAROOT to our config directory so truststore stores CA there.
	if err := os.MkdirAll(m.caDir, 0700); err != nil {
		return fmt.Errorf("failed to create CA directory: %w", err)
	}
	if err := secureDir(m.caDir); err != nil {
		m.logger.Printf("Warning: failed to lock down CA directory permissions: %v", err)
	}
	os.Setenv("CAROOT", m.caDir)

	// Initialize truststore library (creates CA if needed)
	ml, err := truststore.NewLib()
	if err != nil {
		return fmt.Errorf("failed to initialize truststore: %w", err)
	}

	// Check if CA is installed by trying to generate a test cert first
	// If the CA isn't trusted, we need to install it
	m.logger.Println("Ensuring CA is installed in system trust store...")
	m.logger.Println("(You may be prompted for your password)")

	// Install CA - this is idempotent and will prompt for password if needed
	if err := ml.Install(); err != nil {
		return fmt.Errorf("failed to install CA: %w", err)
	}

	m.logger.Println("CA installed successfully")

	// Generate server certificate
	m.logger.Printf("Generating certificate for hosts: %v", hosts)

	cert, err := ml.MakeCert(hosts, m.tlsDir)
	if err != nil {
		return fmt.Errorf("failed to generate certificate: %w", err)
	}

	// Rename files to our expected names.
	if cert.CertFile != m.certFile {
		if err := os.Rename(cert.CertFile, m.certFile); err != nil {
			return fmt.Errorf("failed to rename cert file: %w", err)
		}
	}
	if cert.KeyFile != m.keyFile {
		if err := os.Rename(cert.KeyFile, m.keyFile); err != nil {
			return fmt.Errorf("failed to rename key file: %w", err)
		}
	}

	// Restrict ACL on the private key. The cert is public, but truststore may
	// have created the key world-readable on Windows (Unix mode bits ignored).
	if err := secureFile(m.keyFile); err != nil {
		m.logger.Printf("Warning: failed to lock down key file permissions: %v", err)
	}

	// Cache the hosts
	if err := m.writeCachedHosts(hosts); err != nil {
		m.logger.Printf("Warning: failed to cache hosts: %v", err)
	}

	m.logger.Printf("Certificate generated: %s", m.certFile)

	// Log CA fingerprint for verification
	if fingerprint, err := m.GetCAFingerprint(); err == nil {
		m.logger.Printf("CA Fingerprint (SHA256): %s", fingerprint)
	}

	return nil
}

// GetCertFile returns the path to the certificate file.
func (m *Manager) GetCertFile() string {
	return m.certFile
}

// GetKeyFile returns the path to the key file.
func (m *Manager) GetKeyFile() string {
	return m.keyFile
}

// GetCACertFile returns the path to the CA certificate file.
func (m *Manager) GetCACertFile() string {
	return m.caCertFile
}

// GetCAFingerprint returns the SHA256 fingerprint of the CA certificate.
func (m *Manager) GetCAFingerprint() (string, error) {
	certPEM, err := os.ReadFile(m.caCertFile)
	if err != nil {
		return "", fmt.Errorf("failed to read CA certificate: %w", err)
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return "", fmt.Errorf("failed to decode PEM block")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return "", fmt.Errorf("failed to parse certificate: %w", err)
	}

	fingerprint := sha256.Sum256(cert.Raw)

	// Format as colon-separated hex
	var parts []string
	for _, b := range fingerprint {
		parts = append(parts, fmt.Sprintf("%02X", b))
	}

	return strings.Join(parts, ":"), nil
}

// ReadCACert reads and returns the CA certificate PEM data.
func (m *Manager) ReadCACert() ([]byte, error) {
	return os.ReadFile(m.caCertFile)
}

// WatchNetworkChanges starts watching for network changes and returns a channel
// that signals when certificates have been regenerated due to IP changes.
// The channel receives a signal after new certificates are ready.
// Safe to call multiple times; subsequent calls return the existing channel.
func (m *Manager) WatchNetworkChanges() <-chan struct{} {
	m.mu.Lock()
	if m.networkChangeChan != nil {
		ch := m.networkChangeChan
		m.mu.Unlock()
		return ch
	}

	notifyCh := make(chan struct{}, 1)
	stopCh := make(chan struct{})
	m.networkChangeChan = notifyCh
	m.stopWatchChan = stopCh
	m.lastHosts, _ = GetAllHosts()
	m.mu.Unlock()

	// Pass channels by parameter so the goroutine doesn't race with
	// StopWatching reading/writing m.stopWatchChan.
	go m.watchNetworkLoop(stopCh, notifyCh)

	return notifyCh
}

// StopWatching stops the network change watcher. Safe to call multiple times.
func (m *Manager) StopWatching() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stopWatchChan != nil {
		close(m.stopWatchChan)
		m.stopWatchChan = nil
	}
}

// watchNetworkLoop monitors for network changes and regenerates certificates.
// Driven by native OS address-change notifications when available
// (netlink/route socket/NotifyAddrChange) with a 30s ticker as a safety net
// in case events are missed. Comparison is against in-memory state
// (lastHosts), not the on-disk hosts cache, so a transient writeCachedHosts
// failure cannot wedge the loop into regenerating every tick.
func (m *Manager) watchNetworkLoop(stopCh <-chan struct{}, notifyCh chan<- struct{}) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	addrCh := addrChangeNotifier(stopCh)

	for {
		select {
		case <-stopCh:
			return
		case <-ticker.C:
		case <-addrCh:
		}

		currentHosts, err := GetAllHosts()
		if err != nil {
			continue
		}

		m.mu.Lock()
		prev := append([]string(nil), m.lastHosts...)
		m.mu.Unlock()

		if sameHosts(prev, currentHosts) {
			continue
		}

		m.logger.Printf("Network change detected: %v -> %v", prev, currentHosts)

		if err := m.RegenerateCertificates(); err != nil {
			m.logger.Printf("Failed to regenerate certificates: %v", err)
			continue
		}

		// Only commit lastHosts after a successful regen so the next tick
		// retries if regeneration partially failed.
		m.mu.Lock()
		m.lastHosts = currentHosts
		m.mu.Unlock()

		select {
		case notifyCh <- struct{}{}:
		default:
			// Channel full, skip
		}
	}
}

// RegenerateCertificates regenerates server certificates for the current hosts.
func (m *Manager) RegenerateCertificates() error {
	hosts, err := GetAllHosts()
	if err != nil {
		m.logger.Printf("Warning: failed to get hosts: %v", err)
		hosts = []string{"localhost", "127.0.0.1"}
	}

	m.logger.Printf("Regenerating certificates for hosts: %v", hosts)

	if err := m.generateCertificates(hosts); err != nil {
		return fmt.Errorf("failed to regenerate certificates: %w", err)
	}

	m.logger.Println("Certificates regenerated successfully")
	return nil
}

// GetCurrentHosts returns the current list of hosts the certificate is valid for.
func (m *Manager) GetCurrentHosts() []string {
	hosts, _ := m.readCachedHosts()
	return hosts
}
