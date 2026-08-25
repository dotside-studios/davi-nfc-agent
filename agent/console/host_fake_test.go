//go:build !nowebui

package console

import (
	"errors"
	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"sync"
	"time"
)

// fakeHost is a Host with no agent behind it, so the tests drive the real
// routes, the real gate and the real dispatcher against state they control.
type fakeHost struct {
	mu sync.Mutex

	running   bool
	configDir string

	available []string
	cardTypes []string
	cardUID   string
	cardType  string
	cardOn    bool

	port          int
	bootstrapPort int
	tls           bool
	certFile      string
	localIPs      []string
	clients       []Client

	apiSecret    string
	pairingPIN   string
	publicKeyPin string
	caInstalled  bool

	devices []PairedDevice
	allowed []string
	blocked []string
	anyOrig bool

	// settings stands in for the agent's live configuration, which is what the
	// real host answers with. The console reads a preference from here and
	// nowhere else.
	settings agent.Preferences

	// Recorded calls, so a test can assert an action reached the agent.
	started, stopped, quit, restarted int
	disconnected                      []string
	revoked                           []string
	rotatedSecret, rotatedPIN         int
	regeneratedCert                   int
	installedCA                       int
	failInstallCA                     bool

	failDisconnect bool
}

func newFakeHost() *fakeHost {
	return &fakeHost{
		running:       true,
		configDir:     "/tmp/agent",
		available:     []string{"ACS ACR1252U 01 00"},
		cardTypes:     []string{"NTAG215", "MIFARE Classic 1K"},
		port:          9470,
		bootstrapPort: 9472,
		apiSecret:     "test-secret",
		settings:      agent.Preferences{Mode: nfc.ModeReadWrite},
	}
}

func (h *fakeHost) Running() bool     { return h.running }
func (h *fakeHost) ConfigDir() string { return h.configDir }

func (h *fakeHost) StartAgent() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.started++
	h.running = true
	return nil
}

func (h *fakeHost) StopAgent() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stopped++
	h.running = false
}

func (h *fakeHost) QuitAgent() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.quit++
}

func (h *fakeHost) RestartServers() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.restarted++
	return nil
}

func (h *fakeHost) AvailableDevices() []string { return h.available }
func (h *fakeHost) AllCardTypes() []string     { return h.cardTypes }

func (h *fakeHost) CurrentCard() (string, string, bool) { return h.cardUID, h.cardType, h.cardOn }
func (h *fakeHost) RemoteDevices() (int, int)           { return 0, 0 }

func (h *fakeHost) SelectDevice(path string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.settings.DevicePath = path
	return nil
}

func (h *fakeHost) Port() int          { return h.port }
func (h *fakeHost) BootstrapPort() int { return h.bootstrapPort }
func (h *fakeHost) CertFile() string   { return h.certFile }
func (h *fakeHost) TLSEnabled() bool   { return h.tls }
func (h *fakeHost) LocalIPs() []string { return h.localIPs }
func (h *fakeHost) ClientCount() int   { return len(h.clients) }
func (h *fakeHost) Clients() []Client  { return h.clients }

func (h *fakeHost) DisconnectClient(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failDisconnect {
		return errors.New("no such client: it may have already disconnected")
	}
	h.disconnected = append(h.disconnected, id)
	return nil
}

func (h *fakeHost) APISecret() string    { return h.apiSecret }
func (h *fakeHost) PublicKeyPin() string { return h.publicKeyPin }
func (h *fakeHost) PairingPIN() string   { return h.pairingPIN }

func (h *fakeHost) RotateAPISecret() (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rotatedSecret++
	h.apiSecret = "rotated-secret"
	return h.apiSecret, nil
}

func (h *fakeHost) RotatePairingPIN() (string, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.rotatedPIN++
	h.pairingPIN = "654321"
	return h.pairingPIN, nil
}

func (h *fakeHost) CAInstalled() bool              { return h.caInstalled }
func (h *fakeHost) CAFingerprint() (string, error) { return "AA:BB", nil }

func (h *fakeHost) InstallCA() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.failInstallCA {
		return errors.New("failed to install the certificate authority: no trust store")
	}
	h.installedCA++
	h.caInstalled = true
	return nil
}

func (h *fakeHost) RegenerateCertificate() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.regeneratedCert++
	return nil
}

func (h *fakeHost) PairedDevices() []PairedDevice { return h.devices }

func (h *fakeHost) RevokeDevice(id string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.revoked = append(h.revoked, id)
	return nil
}

func (h *fakeHost) RevokeAllDevices() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.devices = nil
	return nil
}

func (h *fakeHost) AllowedOrigins() []string  { return h.allowed }
func (h *fakeHost) BlockedOrigins() []string  { return h.blocked }
func (h *fakeHost) OriginCheckDisabled() bool { return h.anyOrig }
func (h *fakeHost) SetOriginCheckDisabled(v bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.anyOrig = v
}

func (h *fakeHost) AllowOrigin(origin string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.allowed = append(h.allowed, origin)
	return nil
}

func (h *fakeHost) RevokeOrigin(origin string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.allowed[:0]
	for _, o := range h.allowed {
		if o != origin {
			out = append(out, o)
		}
	}
	h.allowed = out
	return nil
}

func (h *fakeHost) Preferences() agent.Preferences {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.settings
}

// ApplyPreferences answers with what the agent then holds, as the real host
// does. Nothing is persisted: a change lasts as long as the agent runs.
func (h *fakeHost) ApplyPreferences(mutate func(*agent.Preferences)) agent.Preferences {
	h.mu.Lock()
	defer h.mu.Unlock()

	next := h.settings
	mutate(&next)
	h.settings = next
	return next
}

// seedDevices gives the host a few paired devices.
func (h *fakeHost) seedDevices() {
	h.devices = []PairedDevice{
		{ID: "dev-1", Name: "Ned's iPhone", Platform: "iOS 17.4", PairedAt: time.Now().Add(-72 * time.Hour), Online: true},
		{ID: "dev-2", Name: "Front desk Pixel", Platform: "Android 14", PairedAt: time.Now().Add(-48 * time.Hour)},
	}
}
