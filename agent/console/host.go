//go:build !nowebui

package console

import (
	"errors"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/agent/pairingplugin"
	"github.com/dotside-studios/davi-nfc-agent/agent/serverplugin"
	"github.com/dotside-studios/davi-nfc-agent/agent/trustplugin"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/server/netinfo"
)

// host adapts the agent to Host. Every reach the console makes into the agent
// is a method here.
type host struct {
	agent   *agent.Agent
	servers *serverplugin.Plugin
	pairing *pairingplugin.Plugin
	trust   *trustplugin.Plugin

	// quit ends the program the agent runs in, supplied by whoever owns it.
	quit func()
}

var _ Host = (*host)(nil)

func (h *host) Running() bool     { return h.agent.Running() }
func (h *host) ConfigDir() string { return h.agent.ConfigDir() }

func (h *host) StartAgent() error {
	return h.agent.Start(h.agent.CurrentDevicePath())
}

func (h *host) StopAgent() { h.agent.Stop() }

// QuitAgent ends the program the agent runs in, which is the host's to end. A
// build that supplied no way out stops the agent instead.
func (h *host) QuitAgent() {
	if h.quit == nil {
		h.agent.Shutdown()
		return
	}
	h.quit()
}

// AvailableDevices is the reader picker, so it lists what the agent reads from.
// A phone reports what it scans for itself and is never read from here, so it
// belongs in the paired devices rather than among the readers.
func (h *host) AvailableDevices() []string { return h.agent.Readers() }

func (h *host) AllCardTypes() []string { return nfc.GetAllCardTypes() }

func (h *host) CurrentCard() (uid, cardType string, present bool) {
	card := h.agent.LastCard()
	if card == nil {
		return "", "", false
	}
	return card.UID, card.Type, true
}

// Port is the port being served, not the one configured. A port saved in the
// console reaches the listener only when one is next built, and until then the
// console must not hand out a URL nothing is listening on.
func (h *host) Port() int          { return h.servers.Port() }
func (h *host) BootstrapPort() int { return h.pairing.Port() }
func (h *host) CertFile() string   { return h.servers.CertFile() }
func (h *host) TLSEnabled() bool   { return h.servers.TLSEnabled() }
func (h *host) LocalIPs() []string { return netinfo.LocalIPs() }

func (h *host) ClientCount() int { return h.servers.ClientCount() }

func (h *host) Clients() []Client {
	live := h.servers.Clients()

	out := make([]Client, 0, len(live))
	for _, c := range live {
		out = append(out, Client{
			ID:          c.ID,
			Origin:      c.Origin,
			RemoteAddr:  c.RemoteAddr,
			UserAgent:   c.UserAgent,
			ConnectedAt: c.ConnectedAt,
			Writes:      c.Writes,
			Locks:       c.Locks,
		})
	}
	return out
}

func (h *host) DisconnectClient(id string) error { return h.servers.DisconnectClient(id) }

func (h *host) APISecret() string    { return h.agent.APISecret() }
func (h *host) PublicKeyPin() string { return h.agent.PublicKeyPin() }

func (h *host) RotateAPISecret() (string, error) { return h.agent.RotateAPISecret() }

func (h *host) PairingPIN() string { return h.pairing.PIN() }

// RotatePairingPIN goes through the plugin rather than the server, so the tray
// entries showing the PIN follow a rotation done in the console.
func (h *host) RotatePairingPIN() (string, error) {
	if h.pairing == nil {
		return "", errors.New("pairing server is disabled")
	}
	return h.pairing.RotatePIN(), nil
}

func (h *host) CAInstalled() bool { return h.trust.Installed() }

func (h *host) CAFingerprint() (string, error) {
	if !h.managesCertificates() {
		return "", errors.New("no certificate authority")
	}
	return h.trust.Fingerprint()
}

// InstallCA and RegenerateCertificate go through the trust plugin, so the tray
// entry offering the same action follows an install done here.
func (h *host) InstallCA() error {
	if !h.managesCertificates() {
		return errors.New("agent is not managing its own certificates")
	}
	return h.trust.Install()
}

func (h *host) RegenerateCertificate() error {
	if !h.managesCertificates() {
		return errors.New("agent is not managing its own certificates")
	}
	return h.trust.Regenerate()
}

// managesCertificates reports whether there is a certificate this agent can act
// on. Without one the trust plugin does nothing, and saying so is better than
// reporting success for work that never happened.
func (h *host) managesCertificates() bool { return h.trust.Manages() }

func (h *host) PairedDevices() []PairedDevice {
	if h.agent.Devices() == nil {
		return nil
	}

	// Paired is a stored credential; online is a live session. The console
	// shows both so an absent device reads as absent rather than broken.
	online := make(map[string]bool)
	for _, id := range h.agent.OnlineDevices() {
		online[id] = true
	}

	paired := h.agent.Devices().List()
	out := make([]PairedDevice, 0, len(paired))
	for _, d := range paired {
		out = append(out, PairedDevice{
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

func (h *host) RevokeDevice(id string) error {
	if h.agent.Devices() == nil {
		return errors.New("no device registry")
	}
	return h.agent.Devices().Revoke(id)
}

func (h *host) RevokeAllDevices() error {
	if h.agent.Devices() == nil {
		return errors.New("no device registry")
	}
	return h.agent.Devices().RevokeAll()
}

// The allowlist belongs to what serves the connections it admits, so these ask
// the server plugin. A build with none has no origins to show.
func (h *host) origins() *server.OriginStore {
	if h.servers == nil {
		return nil
	}
	return h.servers.Origins
}

func (h *host) AllowedOrigins() []string {
	if h.origins() == nil {
		return nil
	}
	return h.origins().List()
}

func (h *host) BlockedOrigins() []string {
	if h.origins() == nil {
		return nil
	}
	return h.origins().Blocked()
}

func (h *host) OriginCheckDisabled() bool {
	return h.origins() != nil && h.origins().IsSessionAllowAny()
}

func (h *host) AllowOrigin(origin string) error {
	if h.origins() == nil {
		return errors.New("no origin store")
	}
	return h.origins().Allow(origin)
}

func (h *host) RevokeOrigin(origin string) error {
	if h.origins() == nil {
		return errors.New("no origin store")
	}
	return h.origins().Revoke(origin)
}

func (h *host) SetOriginCheckDisabled(on bool) {
	if h.origins() != nil {
		h.origins().SessionAllowAny(on)
	}
}

// agent.Preferences comes from the agent, so the console shows what is in force,
// such as a mode switched from the tray.
func (h *host) Preferences() agent.Preferences {
	return h.agent.Preferences()
}

// ApplyPreferences changes the agent and answers with what it holds afterwards,
// which is not necessarily what was asked for.
func (h *host) ApplyPreferences(mutate func(*agent.Preferences)) agent.Preferences {
	return h.agent.ApplyPreferences(mutate)
}
