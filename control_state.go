package main

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/buildinfo"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// ControlState is the whole picture the console renders from. Sent as a
// snapshot rather than deltas, so the console can never show a half-applied
// combination of settings.
type ControlState struct {
	Agent    AgentInfo    `json:"agent"`
	Reader   ReaderInfo   `json:"reader"`
	Server   ServerInfo   `json:"server"`
	Security SecurityInfo `json:"security"`
	Settings Settings     `json:"settings"`
	Devices  []DeviceInfo `json:"devices"`
	Origins  OriginsInfo  `json:"origins"`
	Capture  CaptureInfo  `json:"capture"`
}

// AgentInfo covers identity and lifecycle.
type AgentInfo struct {
	Name      string    `json:"name"`
	Version   string    `json:"version"`
	Dev       bool      `json:"dev"`
	Running   bool      `json:"running"`
	StartedAt time.Time `json:"startedAt"`
	UptimeSec int64     `json:"uptimeSec"`
	ConfigDir string    `json:"configDir"`
	Platform  string    `json:"platform"`
}

// ReaderInfo covers the NFC reader and the card currently on it.
type ReaderInfo struct {
	Mode          string   `json:"mode"`
	DevicePath    string   `json:"devicePath"`
	Available     []string `json:"available"`
	CardPresent   bool     `json:"cardPresent"`
	CardUID       string   `json:"cardUID,omitempty"`
	CardType      string   `json:"cardType,omitempty"`
	AllCardTypes  []string `json:"allCardTypes"`
	RemoteDevices int      `json:"remoteDevices"`
	RemoteActive  int      `json:"remoteActive"`
}

// ServerInfo covers the listener and the URLs a client would use.
type ServerInfo struct {
	Port          int      `json:"port"`
	BootstrapPort int      `json:"bootstrapPort"`
	TLS           bool     `json:"tls"`
	ClientURL     string   `json:"clientURL"`
	DeviceURL     string   `json:"deviceURL"`
	PairingURL    string   `json:"pairingURL,omitempty"`
	LocalIPs      []string `json:"localIPs"`
	Clients       int      `json:"clients"`
}

// SecurityInfo covers the credentials and the certificate. The API secret is
// included in full: the console's gate is a higher bar than reading the file it
// comes from.
type SecurityInfo struct {
	APISecret           string    `json:"apiSecret"`
	PairingPIN          string    `json:"pairingPIN,omitempty"`
	PublicKeyPin        string    `json:"publicKeyPin,omitempty"`
	RequirePairedDevice bool      `json:"requirePairedDevice"`
	CAInstalled         bool      `json:"caInstalled"`
	CAFingerprint       string    `json:"caFingerprint,omitempty"`
	Cert                *CertInfo `json:"cert,omitempty"`
	ControlSessions     int       `json:"controlSessions"`
}

// CertInfo describes the certificate being served. Expiry and covered names are
// the two things that break a deployment and neither is visible elsewhere.
type CertInfo struct {
	Subject     string    `json:"subject"`
	Issuer      string    `json:"issuer"`
	NotBefore   time.Time `json:"notBefore"`
	NotAfter    time.Time `json:"notAfter"`
	ExpiresInHr int64     `json:"expiresInHr"`
	Expired     bool      `json:"expired"`
	SelfSigned  bool      `json:"selfSigned"`
	Hosts       []string  `json:"hosts"`
	Fingerprint string    `json:"fingerprint"`
}

// DeviceInfo is a paired device as the console lists it.
type DeviceInfo struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Platform string    `json:"platform"`
	PairedAt time.Time `json:"pairedAt"`
	LastSeen time.Time `json:"lastSeen,omitempty"`
	Online   bool      `json:"online"`
}

// OriginsInfo is the browser allowlist and what it has recently refused.
type OriginsInfo struct {
	Allowed  []string `json:"allowed"`
	Blocked  []string `json:"blocked"`
	AllowAny bool     `json:"allowAny"`
}

// CaptureInfo reports the log ring's state.
type CaptureInfo struct {
	LogEntries int `json:"logEntries"`
	LogSeq     int `json:"logSeq"`
}

// buildState assembles the snapshot from live objects.
func (c *ControlServer) buildState() ControlState {
	agent := c.agent

	state := ControlState{
		Agent: AgentInfo{
			Name:      buildinfo.Name,
			Version:   buildinfo.FullVersion(),
			Dev:       buildinfo.IsDev(),
			Running:   agent.Reader != nil,
			StartedAt: c.startedAt,
			UptimeSec: int64(time.Since(c.startedAt).Seconds()),
			ConfigDir: agent.ConfigDir,
			Platform:  platformName(),
		},
		Settings: c.settings.Get(),
	}

	state.Reader = c.buildReaderInfo()
	state.Server = c.buildServerInfo()
	state.Security = c.buildSecurityInfo()
	state.Devices = c.buildDeviceInfo()
	state.Origins = c.buildOriginsInfo()

	if c.logs != nil {
		entries := c.logs.Entries()
		state.Capture.LogEntries = len(entries)
		if n := len(entries); n > 0 {
			state.Capture.LogSeq = int(entries[n-1].Seq)
		}
	}

	return state
}

func (c *ControlServer) buildReaderInfo() ReaderInfo {
	agent := c.agent

	info := ReaderInfo{
		Mode:         FormatMode(nfc.ModeReadWrite),
		DevicePath:   agent.CurrentDevicePath(),
		AllCardTypes: nfc.GetAllCardTypes(),
	}

	if agent.Reader != nil {
		info.Mode = FormatMode(agent.Reader.GetMode())
	}

	if agent.Manager != nil {
		if devices, err := agent.Manager.ListDevices(); err == nil {
			info.Available = devices
		}
	}
	if info.Available == nil {
		info.Available = []string{}
	}

	if agent.ClientServer != nil {
		if card := agent.ClientServer.GetLastCard(); card != nil {
			info.CardPresent = true
			info.CardUID = card.UID
			info.CardType = card.Type
		}
	}

	if mgr := c.remoteManager(); mgr != nil {
		info.RemoteDevices = mgr.GetDeviceCount()
		info.RemoteActive = mgr.GetActiveDeviceCount()
	}

	return info
}

func (c *ControlServer) buildServerInfo() ServerInfo {
	agent := c.agent
	secure := agent.CertFile != "" && agent.KeyFile != ""

	scheme := "ws"
	httpScheme := "http"
	if secure {
		scheme = "wss"
		httpScheme = "https"
	}

	info := ServerInfo{
		Port:          agent.DevicePort,
		BootstrapPort: c.bootstrapPort,
		TLS:           secure,
		LocalIPs:      getLocalIPs(),
		ClientURL:     fmt.Sprintf("%s://%s/ws", scheme, hostPort("localhost", agent.DevicePort)),
		DeviceURL:     fmt.Sprintf("%s://%s/ws?mode=device", scheme, hostPort("localhost", agent.DevicePort)),
	}
	if info.LocalIPs == nil {
		info.LocalIPs = []string{}
	}

	// Must name an address a phone can reach, so not loopback.
	if c.bootstrap != nil && c.bootstrapPort > 0 {
		host := "localhost"
		if ips := info.LocalIPs; len(ips) > 0 {
			host = ips[0]
		}
		if pin := c.bootstrap.PIN(); pin != "" {
			info.PairingURL = fmt.Sprintf("%s://%s/pair?pin=%s", httpScheme, hostPort(host, c.bootstrapPort), pin)
		} else {
			info.PairingURL = fmt.Sprintf("%s://%s/pair", httpScheme, hostPort(host, c.bootstrapPort))
		}
	}

	if agent.ClientServer != nil {
		info.Clients = agent.ClientServer.ClientCount()
	}

	return info
}

func (c *ControlServer) buildSecurityInfo() SecurityInfo {
	agent := c.agent

	info := SecurityInfo{
		APISecret:           agent.APISecret,
		PublicKeyPin:        agent.PublicKeyPin,
		RequirePairedDevice: agent.RequirePairedDevice,
		ControlSessions:     c.auth.SessionCount(),
	}

	if c.bootstrap != nil {
		info.PairingPIN = c.bootstrap.PIN()
	}

	if agent.TLSManager != nil {
		info.CAInstalled = agent.TLSManager.CAInstalled()
		if info.CAInstalled {
			if fp, err := agent.TLSManager.GetCAFingerprint(); err == nil {
				info.CAFingerprint = fp
			}
		}
	}

	if agent.CertFile != "" {
		if cert, err := readCertInfo(agent.CertFile); err == nil {
			info.Cert = cert
		}
	}

	return info
}

func (c *ControlServer) buildDeviceInfo() []DeviceInfo {
	if c.agent.Devices == nil {
		return []DeviceInfo{}
	}

	paired := c.agent.Devices.List()
	out := make([]DeviceInfo, 0, len(paired))

	// Paired is a stored credential; online is a live session. The console
	// shows both so an absent device reads as absent rather than broken.
	online := make(map[string]bool)
	if mgr := c.remoteManager(); mgr != nil {
		if ids, err := mgr.ListDevices(); err == nil {
			for _, id := range ids {
				online[id] = true
			}
		}
	}

	for _, d := range paired {
		out = append(out, DeviceInfo{
			ID:       d.ID,
			Name:     d.Name,
			Platform: d.Platform,
			PairedAt: d.PairedAt,
			LastSeen: d.LastSeen,
			Online:   online[d.ID],
		})
	}
	return out
}

func (c *ControlServer) buildOriginsInfo() OriginsInfo {
	info := OriginsInfo{Allowed: []string{}, Blocked: []string{}}
	if c.agent.Origins == nil {
		return info
	}

	if allowed := c.agent.Origins.List(); allowed != nil {
		info.Allowed = allowed
	}
	if blocked := c.agent.Origins.Blocked(); blocked != nil {
		info.Blocked = blocked
	}
	info.AllowAny = c.agent.Origins.IsSessionAllowAny()
	return info
}

// readCertInfo parses the served certificate for display.
func readCertInfo(path string) (*CertInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// A chain file leads with the leaf, which is what governs connection.
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block in %s", path)
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, err
	}

	sum := sha256.Sum256(cert.Raw)
	parts := make([]string, 0, len(sum))
	for _, b := range sum {
		parts = append(parts, fmt.Sprintf("%02X", b))
	}

	hosts := append([]string(nil), cert.DNSNames...)
	for _, ip := range cert.IPAddresses {
		hosts = append(hosts, ip.String())
	}

	return &CertInfo{
		Subject:     cert.Subject.CommonName,
		Issuer:      cert.Issuer.CommonName,
		NotBefore:   cert.NotBefore,
		NotAfter:    cert.NotAfter,
		ExpiresInHr: int64(time.Until(cert.NotAfter).Hours()),
		Expired:     time.Now().After(cert.NotAfter),
		SelfSigned:  cert.Issuer.String() == cert.Subject.String(),
		Hosts:       hosts,
		Fingerprint: strings.Join(parts, ":"),
	}, nil
}
