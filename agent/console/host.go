//go:build !nowebui

package console

import (
	"errors"

	"github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/remotenfc"
	"github.com/dotside-studios/davi-nfc-agent/settings"
	"github.com/dotside-studios/davi-nfc-agent/webui"
)

// host adapts the agent to webui.Host. Every reach the console makes into the
// agent is a method here. app is the tray, when there is one: actions that
// must also move the tray's menu go through it rather than straight to the
// agent.
type host struct {
	agent    *agent.Agent
	settings *settings.Store
	pairing  *agent.PairingServer
	app      Tray
}

var _ webui.Host = (*host)(nil)

// ---- identity and lifecycle ----

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

// ---- reader ----

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
	// Refused rather than accepted and quietly ignored: the picker no longer
	// offers a phone, so one arriving here came from somewhere that should hear
	// why it cannot be the reader.
	if nfc.IsRemoteDevice(h.agent.Manager(), devicePath) {
		return errors.New("a phone reports its scans over the device bridge and cannot be selected as the reader")
	}
	h.app.SwitchDevice(devicePath)
	return nil
}

// ---- server ----

// Port is the port being served, not the one configured. A port saved in the
// console is bound only once the listener has been restarted, and until then
// the console must not hand out a URL nothing is listening on.
func (h *host) Port() int          { return h.agent.ServingPort() }
func (h *host) BootstrapPort() int { return h.pairing.Port() }
func (h *host) CertFile() string   { return h.agent.CertFile() }
func (h *host) TLSEnabled() bool   { return h.agent.CertFile() != "" && h.agent.KeyFile() != "" }
func (h *host) LocalIPs() []string { return agent.LocalIPs() }

func (h *host) ClientCount() int {
	if h.agent.ClientServer == nil {
		return 0
	}
	return h.agent.ClientServer.ClientCount()
}

func (h *host) Clients() []webui.Client {
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

func (h *host) DisconnectClient(id string) error {
	if h.agent.ClientServer == nil {
		return errors.New("agent is not running")
	}
	if !h.agent.ClientServer.Disconnect(id) {
		return errors.New("no such client — it may have already disconnected")
	}
	return nil
}

// ---- credentials and trust ----

func (h *host) APISecret() string    { return h.agent.APISecret() }
func (h *host) PublicKeyPin() string { return h.agent.PublicKeyPin() }

func (h *host) RotateAPISecret() (string, error) { return h.agent.RotateAPISecret() }

func (h *host) PairingPIN() string { return h.pairing.PIN() }

func (h *host) RotatePairingPIN() (string, error) {
	if h.pairing == nil {
		return "", errors.New("pairing server is disabled")
	}
	return h.pairing.RotatePIN(), nil
}

func (h *host) CAInstalled() bool {
	return h.agent.TLSManager() != nil && h.agent.TLSManager().CAInstalled()
}

func (h *host) CAFingerprint() (string, error) {
	if h.agent.TLSManager() == nil {
		return "", errors.New("no certificate authority")
	}
	return h.agent.TLSManager().GetCAFingerprint()
}

func (h *host) InstallCA() error {
	if h.agent.TLSManager() == nil {
		return errors.New("agent is not managing its own certificates")
	}
	if err := h.agent.TLSManager().InstallCA(); err != nil {
		return err
	}
	if h.app != nil {
		// The tray offers the same action; it has nothing left to offer now.
		h.app.RefreshTrustMenu()
	}
	return nil
}

func (h *host) RegenerateCertificate() error {
	if h.agent.TLSManager() == nil {
		return errors.New("agent is not managing its own certificates")
	}
	return h.agent.TLSManager().RegenerateCertificates()
}

// ---- paired devices ----

func (h *host) PairedDevices() []webui.PairedDevice {
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

// ---- browser origins ----

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

// ---- settings ----

// Settings comes from the agent rather than from the file, so the console shows
// what is in force, such as a mode switched from the tray or a pairing
// requirement the command line will not let a preference withdraw.
func (h *host) Settings() settings.Settings { return h.agent.Settings() }

// Explicit reports the settings the launcher holds, which the console shows as
// disabled controls rather than accepting a change it would have to discard.
func (h *host) Explicit() settings.Explicit { return h.agent.Explicit() }

// SaveSettings persists the change and answers with what the agent then holds.
// It applies nothing itself: the store's change hook is the one path from a
// saved preference to the running agent, so a save made here and one made from
// the tray land the same way.
func (h *host) SaveSettings(mutate func(*settings.Settings)) (settings.Settings, error) {
	explicit := h.agent.Explicit()
	if _, err := h.settings.Update(func(next *settings.Settings) {
		prev := *next
		mutate(next)
		// A field the launcher holds is left as the file had it. The agent
		// would refuse the change, and a file saying otherwise is read back as
		// fact on the next start.
		explicit.Keep(next, prev)
	}); err != nil {
		return h.agent.Settings(), err
	}
	return h.agent.Settings(), nil
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
