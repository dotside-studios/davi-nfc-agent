//go:build !nowebui

package console

import (
	"errors"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
)

// host adapts the agent to Host. Every reach the console makes into the
// agent is a method here. app is the tray, when there is one: actions that
// must also move the tray's menu go through it rather than straight to the
// agent.
type host struct {
	agent   *agent.Agent
	servers *agent.ServerPlugin
	pairing *agent.PairingPlugin
	trust   *agent.TrustPlugin
	app     Tray
}

var _ Host = (*host)(nil)

func (h *host) Running() bool     { return h.agent.Reader() != nil }
func (h *host) ConfigDir() string { return h.agent.ConfigDir() }

func (h *host) StartAgent() error {
	return h.agent.Start(h.agent.CurrentDevicePath())
}

func (h *host) StopAgent() {
	if h.app != nil {
		// Through the tray, so its menu state follows.
		h.app.StopAgent()
		return
	}
	h.agent.Stop()
}

func (h *host) QuitAgent() {
	if h.app != nil {
		h.app.Quit()
	}
}

func (h *host) RestartServers() error { return h.agent.RestartServers() }

func (h *host) AvailableDevices() []string {
	if h.agent.Manager() == nil {
		return nil
	}
	// The reader picker, so it offers only what can actually be one. A phone
	// appearing here reads as a reader to choose, and choosing it pins the
	// reader to a device that is never opened.
	devices, err := nfc.ListReaders(h.agent.Manager())
	if err != nil {
		return nil
	}
	return devices
}

func (h *host) AllCardTypes() []string { return nfc.GetAllCardTypes() }

func (h *host) CurrentCard() (uid, cardType string, present bool) {
	if h.agent.ClientServer == nil {
		return "", "", false
	}
	card := h.agent.ClientServer.GetLastCard()
	if card == nil {
		return "", "", false
	}
	return card.UID, card.Type, true
}

func (h *host) RemoteDevices() (total, active int) {
	mgr := h.remoteManager()
	if mgr == nil {
		return 0, 0
	}
	return mgr.GetDeviceCount(), mgr.GetActiveDeviceCount()
}

func (h *host) SelectDevice(devicePath string) error {
	if h.app == nil {
		return errors.New("device cannot be changed from here")
	}
	// Refused rather than accepted and quietly ignored: the picker does not
	// offer a phone, so one arriving here came from somewhere that should hear
	// why it cannot be the reader.
	if nfc.IsRemoteDevice(h.agent.Manager(), devicePath) {
		return errors.New("a phone reports its scans over the device bridge and cannot be selected as the reader")
	}
	h.app.SwitchDevice(devicePath)
	return nil
}

// Port is the port being served, not the one configured. A port saved in the
// console is bound only once the listener has been restarted, and until then
// the console must not hand out a URL nothing is listening on.
func (h *host) Port() int          { return h.servers.Port() }
func (h *host) BootstrapPort() int { return h.pairing.Port() }
func (h *host) CertFile() string   { return h.servers.CertFile() }
func (h *host) TLSEnabled() bool   { return h.servers.TLSEnabled() }
func (h *host) LocalIPs() []string { return agent.LocalIPs() }

func (h *host) ClientCount() int {
	if h.agent.ClientServer == nil {
		return 0
	}
	return h.agent.ClientServer.ClientCount()
}

func (h *host) Clients() []Client {
	if h.agent.ClientServer == nil {
		return nil
	}
	live := h.agent.ClientServer.Clients()
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

func (h *host) DisconnectClient(id string) error {
	if h.agent.ClientServer == nil {
		return errors.New("agent is not running")
	}
	if !h.agent.ClientServer.Disconnect(id) {
		return errors.New("no such client: it may have already disconnected")
	}
	return nil
}

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
	if mgr := h.remoteManager(); mgr != nil {
		if ids, err := mgr.ListDevices(); err == nil {
			for _, id := range ids {
				online[id] = true
			}
		}
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

func (h *host) AllowedOrigins() []string {
	if h.agent.Origins() == nil {
		return nil
	}
	return h.agent.Origins().List()
}

func (h *host) BlockedOrigins() []string {
	if h.agent.Origins() == nil {
		return nil
	}
	return h.agent.Origins().Blocked()
}

func (h *host) OriginCheckDisabled() bool {
	return h.agent.Origins() != nil && h.agent.Origins().IsSessionAllowAny()
}

func (h *host) AllowOrigin(origin string) error {
	if h.agent.Origins() == nil {
		return errors.New("no origin store")
	}
	return h.agent.Origins().Allow(origin)
}

func (h *host) RevokeOrigin(origin string) error {
	if h.agent.Origins() == nil {
		return errors.New("no origin store")
	}
	return h.agent.Origins().Revoke(origin)
}

func (h *host) SetOriginCheckDisabled(on bool) {
	if h.agent.Origins() != nil {
		h.agent.Origins().SessionAllowAny(on)
	}
}

// agent.Preferences comes from the agent, so the console shows what is in force,
// such as a mode switched from the tray.
func (h *host) Preferences() agent.Preferences {
	return h.agent.Preferences()
}

// ApplyPreferences changes the agent and answers with what it holds afterwards,
// which is not necessarily what was asked for. Nothing is persisted: a change
// lasts as long as the agent runs.
func (h *host) ApplyPreferences(mutate func(*agent.Preferences)) agent.Preferences {
	next := h.agent.Preferences()
	mutate(&next)

	h.agent.SetReaderMode(next.Mode)
	h.agent.SetCardTypeFilter(next.CardTypes)
	h.agent.SetPinnedDevice(next.DevicePath)
	h.agent.SetDevicePort(next.Port)
	h.agent.SetRequirePairedDevice(next.RequirePairedDevice)
	h.agent.SetReaderFeedback(next.ReaderFeedback)

	applied := h.agent.Preferences()
	if h.app != nil {
		h.app.SyncPreferencesToMenu(applied)
	}
	return applied
}

// remoteManager returns the remote device manager, held either directly or
// behind the multi-manager.
func (h *host) remoteManager() *remotenfc.Manager {
	if h.agent.Manager() == nil {
		return nil
	}
	if m, ok := h.agent.Manager().(*remotenfc.Manager); ok {
		return m
	}
	if mm, ok := h.agent.Manager().(interface {
		GetManager(string) (nfc.Manager, bool)
	}); ok {
		if mgr, exists := mm.GetManager(nfc.ManagerTypeSmartphone); exists {
			if m, ok := mgr.(*remotenfc.Manager); ok {
				return m
			}
		}
	}
	return nil
}
