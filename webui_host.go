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

func (h *webuiHost) AvailableDevices() []string {
	if h.agent.Manager == nil {
		return nil
	}
	// The reader picker, so it offers only what can actually be one. A phone
	// appearing here reads as a reader to choose, and choosing it pins the
	// reader to a device that is never opened.
	devices, err := nfc.ListReaders(h.agent.Manager)
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
	// Refused rather than accepted and quietly ignored: the picker no longer
	// offers a phone, so one arriving here came from somewhere that should hear
	// why it cannot be the reader.
	if nfc.IsRemoteDevice(h.agent.Manager, devicePath) {
		return errors.New("a phone reports its scans over the device bridge and cannot be selected as the reader")
	}
	h.app.switchDevice(devicePath)
	return nil
}

// ---- server ----

// Port is the port being served, not the one configured. A port saved in the
// console is bound only once the listener has been restarted, and until then
// the console must not hand out a URL nothing is listening on.
func (h *webuiHost) Port() int          { return h.agent.ServingPort() }
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

func (h *webuiHost) InstallCA() error {
	if h.agent.TLSManager == nil {
		return errors.New("agent is not managing its own certificates")
	}
	if err := h.agent.TLSManager.InstallCA(); err != nil {
		return err
	}
	if h.app != nil {
		// The tray offers the same action; it has nothing left to offer now.
		h.app.refreshTrustMenu()
	}
	return nil
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

// Settings comes from the agent rather than from the file, so the console shows
// what is in force, such as a mode switched from the tray or a pairing
// requirement the command line will not let a preference withdraw.
func (h *webuiHost) Settings() settings.Settings { return h.agent.Settings() }

// RequirePairedDeviceLocked reports a requirement the console may not withdraw.
func (h *webuiHost) RequirePairedDeviceLocked() bool { return h.agent.PairingRequirementLocked() }

// SaveSettings persists the change and answers with what the agent then holds.
// It applies nothing itself. The store's change hook is the one path from a
// saved preference to the running agent, so a save made here and one made from
// the tray land the same way.
func (h *webuiHost) SaveSettings(mutate func(*settings.Settings)) (settings.Settings, error) {
	if _, err := h.settings.Update(mutate); err != nil {
		return h.agent.Settings(), err
	}
	return h.agent.Settings(), nil
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
