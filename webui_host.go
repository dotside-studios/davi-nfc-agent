//go:build !nowebui

package main

import (
	"errors"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/settings"
	"github.com/dotside-studios/davi-nfc-agent/webui"
)

// webuiHost adapts the agent to webui.Host. Every reach the console makes into
// the agent is a method here.
type webuiHost struct {
	agent    *Agent
	settings *settings.Store
	app      *SystrayApp
}

var _ webui.Host = (*webuiHost)(nil)

// ---- identity and lifecycle ----

func (h *webuiHost) Running() bool     { return h.agent.Reader != nil }
func (h *webuiHost) ConfigDir() string { return h.agent.ConfigDir }

func (h *webuiHost) StartAgent() error {
	return h.agent.Start(h.agent.CurrentDevicePath())
}

func (h *webuiHost) StopAgent() {
	if h.app != nil {
		// Through the tray, so its menu state follows.
		h.app.handleStopAgent()
		return
	}
	h.agent.Stop()
}

func (h *webuiHost) QuitAgent() {
	if h.app != nil {
		h.app.Quit()
	}
}

func (h *webuiHost) RestartServers() error { return h.agent.RestartServers() }

// ---- reader ----

func (h *webuiHost) ReaderMode() string {
	if h.agent.Reader == nil {
		return settings.ModeReadWrite
	}
	return settings.FormatMode(h.agent.Reader.GetMode())
}

func (h *webuiHost) DevicePath() string { return h.agent.CurrentDevicePath() }

func (h *webuiHost) AvailableDevices() []string {
	if h.agent.Manager == nil {
		return nil
	}
	devices, err := h.agent.Manager.ListDevices()
	if err != nil {
		return nil
	}
	return devices
}

func (h *webuiHost) AllCardTypes() []string { return nfc.GetAllCardTypes() }

func (h *webuiHost) CurrentCard() (uid, cardType string, present bool) {
	if h.agent.ClientServer == nil {
		return "", "", false
	}
	card := h.agent.ClientServer.GetLastCard()
	if card == nil {
		return "", "", false
	}
	return card.UID, card.Type, true
}

func (h *webuiHost) RemoteDevices() (total, active int) {
	mgr := h.remoteManager()
	if mgr == nil {
		return 0, 0
	}
	return mgr.GetDeviceCount(), mgr.GetActiveDeviceCount()
}

func (h *webuiHost) SelectDevice(devicePath string) error {
	if h.app == nil {
		return errors.New("device cannot be changed from here")
	}
	h.app.switchDevice(devicePath)
	return nil
}

// ---- server ----

func (h *webuiHost) Port() int          { return h.agent.DevicePort }
func (h *webuiHost) BootstrapPort() int { return h.agent.BootstrapPort }
func (h *webuiHost) CertFile() string   { return h.agent.CertFile }
func (h *webuiHost) TLSEnabled() bool   { return h.agent.CertFile != "" && h.agent.KeyFile != "" }
func (h *webuiHost) LocalIPs() []string { return getLocalIPs() }

func (h *webuiHost) ClientCount() int {
	if h.agent.ClientServer == nil {
		return 0
	}
	return h.agent.ClientServer.ClientCount()
}

func (h *webuiHost) Clients() []webui.Client {
	if h.agent.ClientServer == nil {
		return nil
	}
	live := h.agent.ClientServer.Clients()
	out := make([]webui.Client, 0, len(live))
	for _, c := range live {
		out = append(out, webui.Client{
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

func (h *webuiHost) DisconnectClient(id string) error {
	if h.agent.ClientServer == nil {
		return errors.New("agent is not running")
	}
	if !h.agent.ClientServer.Disconnect(id) {
		return errors.New("no such client — it may have already disconnected")
	}
	return nil
}

// ---- credentials and trust ----

func (h *webuiHost) APISecret() string    { return h.agent.APISecret }
func (h *webuiHost) PublicKeyPin() string { return h.agent.PublicKeyPin }

func (h *webuiHost) RotateAPISecret() (string, error) { return h.agent.RotateAPISecret() }

func (h *webuiHost) PairingPIN() string {
	if h.agent.Bootstrap == nil {
		return ""
	}
	return h.agent.Bootstrap.PIN()
}

func (h *webuiHost) RotatePairingPIN() (string, error) {
	if h.agent.Bootstrap == nil {
		return "", errors.New("pairing server is disabled")
	}
	return h.agent.Bootstrap.RotatePIN(), nil
}

func (h *webuiHost) CAInstalled() bool {
	return h.agent.TLSManager != nil && h.agent.TLSManager.CAInstalled()
}

func (h *webuiHost) CAFingerprint() (string, error) {
	if h.agent.TLSManager == nil {
		return "", errors.New("no certificate authority")
	}
	return h.agent.TLSManager.GetCAFingerprint()
}

func (h *webuiHost) RegenerateCertificate() error {
	if h.agent.TLSManager == nil {
		return errors.New("agent is not managing its own certificates")
	}
	return h.agent.TLSManager.RegenerateCertificates()
}

// ---- paired devices ----

func (h *webuiHost) PairedDevices() []webui.PairedDevice {
	if h.agent.Devices == nil {
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

	paired := h.agent.Devices.List()
	out := make([]webui.PairedDevice, 0, len(paired))
	for _, d := range paired {
		out = append(out, webui.PairedDevice{
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

func (h *webuiHost) RevokeDevice(id string) error {
	if h.agent.Devices == nil {
		return errors.New("no device registry")
	}
	return h.agent.Devices.Revoke(id)
}

func (h *webuiHost) RevokeAllDevices() error {
	if h.agent.Devices == nil {
		return errors.New("no device registry")
	}
	return h.agent.Devices.RevokeAll()
}

func (h *webuiHost) RequirePairedDevice() bool { return h.agent.RequirePairedDevice }

// ---- browser origins ----

func (h *webuiHost) AllowedOrigins() []string {
	if h.agent.Origins == nil {
		return nil
	}
	return h.agent.Origins.List()
}

func (h *webuiHost) BlockedOrigins() []string {
	if h.agent.Origins == nil {
		return nil
	}
	return h.agent.Origins.Blocked()
}

func (h *webuiHost) OriginCheckDisabled() bool {
	return h.agent.Origins != nil && h.agent.Origins.IsSessionAllowAny()
}

func (h *webuiHost) AllowOrigin(origin string) error {
	if h.agent.Origins == nil {
		return errors.New("no origin store")
	}
	return h.agent.Origins.Allow(origin)
}

func (h *webuiHost) RevokeOrigin(origin string) error {
	if h.agent.Origins == nil {
		return errors.New("no origin store")
	}
	return h.agent.Origins.Revoke(origin)
}

func (h *webuiHost) SetOriginCheckDisabled(on bool) {
	if h.agent.Origins != nil {
		h.agent.Origins.SessionAllowAny(on)
	}
}

// ---- settings ----

func (h *webuiHost) Settings() settings.Settings { return h.settings.Get() }

func (h *webuiHost) SaveSettings(mutate func(*settings.Settings)) (settings.Settings, error) {
	saved, err := h.settings.Update(mutate)
	if err != nil {
		return saved, err
	}

	applySettings(h.agent, saved)
	if h.app != nil {
		h.app.syncSettingsToMenu(saved)
	}
	return saved, nil
}

// remoteManager returns the remote device manager, held either directly or
// behind the multi-manager.
func (h *webuiHost) remoteManager() *remotenfc.Manager {
	if h.agent.Manager == nil {
		return nil
	}
	if m, ok := h.agent.Manager.(*remotenfc.Manager); ok {
		return m
	}
	if mm, ok := h.agent.Manager.(interface {
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
