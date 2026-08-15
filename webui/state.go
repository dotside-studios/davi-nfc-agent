package webui

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"net"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/settings"
)

// State is the whole picture the console renders from. Sent as a
// snapshot rather than deltas, so the console can never show a half-applied
// combination of settings.
type State struct {
	Agent    AgentInfo         `json:"agent"`
	Reader   ReaderInfo        `json:"reader"`
	Server   ServerInfo        `json:"server"`
	Security SecurityInfo      `json:"security"`
	Settings settings.Settings `json:"settings"`
	Devices  []DeviceInfo      `json:"devices"`
	Clients  []Client          `json:"clients"`
	Origins  OriginsInfo       `json:"origins"`
	Capture  CaptureInfo       `json:"capture"`
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

// buildState assembles the snapshot from the host.
func (c *Server) buildState() State {
	state := State{
		Agent: AgentInfo{
			Name:      c.name,
			Version:   c.version,
			Dev:       c.dev,
			Running:   c.host.Running(),
			StartedAt: c.startedAt,
			UptimeSec: int64(time.Since(c.startedAt).Seconds()),
			ConfigDir: c.host.ConfigDir(),
			Platform:  runtime.GOOS + "/" + runtime.GOARCH,
		},
		Settings: c.host.Settings(),
	}

	state.Reader = c.buildReaderInfo()
	state.Server = c.buildServerInfo()
	state.Security = c.buildSecurityInfo()
	state.Devices = c.buildDeviceInfo()
	state.Clients = c.buildClientInfo()
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

func (c *Server) buildReaderInfo() ReaderInfo {
	info := ReaderInfo{
		Mode:         c.host.ReaderMode(),
		DevicePath:   c.host.DevicePath(),
		Available:    orEmpty(c.host.AvailableDevices()),
		AllCardTypes: orEmpty(c.host.AllCardTypes()),
	}

	info.CardUID, info.CardType, info.CardPresent = c.host.CurrentCard()
	info.RemoteDevices, info.RemoteActive = c.host.RemoteDevices()
	return info
}

func (c *Server) buildServerInfo() ServerInfo {
	secure := c.host.TLSEnabled()
	port := c.host.Port()

	scheme, httpScheme := "ws", "http"
	if secure {
		scheme, httpScheme = "wss", "https"
	}

	info := ServerInfo{
		Port:          port,
		BootstrapPort: c.host.BootstrapPort(),
		TLS:           secure,
		LocalIPs:      orEmpty(c.host.LocalIPs()),
		Clients:       c.host.ClientCount(),
		ClientURL:     fmt.Sprintf("%s://%s/ws", scheme, net.JoinHostPort("localhost", strconv.Itoa(port))),
		DeviceURL:     fmt.Sprintf("%s://%s/ws?mode=device", scheme, net.JoinHostPort("localhost", strconv.Itoa(port))),
	}

	// Must name an address a phone can reach, so not loopback.
	if bp := c.host.BootstrapPort(); bp > 0 {
		host := "localhost"
		if ips := info.LocalIPs; len(ips) > 0 {
			host = ips[0]
		}
		info.PairingURL = fmt.Sprintf("%s://%s/pair", httpScheme, net.JoinHostPort(host, strconv.Itoa(bp)))
		if pin := c.host.PairingPIN(); pin != "" {
			info.PairingURL += "?pin=" + pin
		}
	}

	return info
}

func (c *Server) buildSecurityInfo() SecurityInfo {
	info := SecurityInfo{
		APISecret:           c.host.APISecret(),
		PairingPIN:          c.host.PairingPIN(),
		PublicKeyPin:        c.host.PublicKeyPin(),
		RequirePairedDevice: c.host.RequirePairedDevice(),
		CAInstalled:         c.host.CAInstalled(),
		ControlSessions:     c.auth.SessionCount(),
	}

	if info.CAInstalled {
		if fp, err := c.host.CAFingerprint(); err == nil {
			info.CAFingerprint = fp
		}
	}

	if path := c.host.CertFile(); path != "" {
		if cert, err := readCertInfo(path); err == nil {
			info.Cert = cert
		}
	}

	return info
}

func (c *Server) buildDeviceInfo() []DeviceInfo {
	paired := c.host.PairedDevices()
	out := make([]DeviceInfo, 0, len(paired))
	for _, d := range paired {
		out = append(out, DeviceInfo{
			ID:       d.ID,
			Name:     d.Name,
			Platform: d.Platform,
			PairedAt: d.PairedAt,
			LastSeen: d.LastSeen,
			Online:   d.Online,
		})
	}
	return out
}

// buildClientInfo lists the applications currently connected to the client
// endpoint. Until now only their count was visible, which cannot answer the
// question an operator actually has: what is driving the reader.
func (c *Server) buildClientInfo() []Client {
	clients := c.host.Clients()
	if clients == nil {
		return []Client{}
	}
	return clients
}

func (c *Server) buildOriginsInfo() OriginsInfo {
	return OriginsInfo{
		Allowed:  orEmpty(c.host.AllowedOrigins()),
		Blocked:  orEmpty(c.host.BlockedOrigins()),
		AllowAny: c.host.OriginCheckDisabled(),
	}
}

// orEmpty keeps a nil slice out of the JSON, so the console can map over every
// list without guarding each one.
func orEmpty(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
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
