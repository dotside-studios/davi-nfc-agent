package webui

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/dotside-studios/davi-nfc-agent/settings"
)

// dispatch routes a console action to the code that performs it. Named actions
// rather than REST verbs: most of these are operations, not resource edits.
func (c *Server) dispatch(req action) (any, error) {
	switch req.Action {

	// ---- agent lifecycle ----

	case "agent.start":
		return nil, c.host.StartAgent()

	case "agent.stop":
		c.host.StopAgent()
		return nil, nil

	case "agent.restartServers":
		return nil, c.host.RestartServers()

	case "agent.quit":
		// Deferred so the response reaches the console before the process exits.
		go c.host.QuitAgent()
		return nil, nil

	// ---- reader ----

	case "reader.selectDevice":
		var params struct {
			DevicePath string `json:"devicePath"`
		}
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		if err := c.host.SelectDevice(params.DevicePath); err != nil {
			return nil, err
		}
		_, err := c.host.SaveSettings(func(s *settings.Settings) { s.DevicePath = params.DevicePath })
		return nil, err

	case "reader.setMode":
		var params struct {
			Mode string `json:"mode"`
		}
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		switch params.Mode {
		case settings.ModeReadWrite, settings.ModeReadOnly, settings.ModeWriteOnly:
		default:
			return nil, fmt.Errorf("unknown mode %q", params.Mode)
		}
		return nil, c.saveSettings(func(s *settings.Settings) { s.Mode = params.Mode })

	case "reader.setCardTypes":
		var params struct {
			CardTypes []string `json:"cardTypes"`
		}
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return nil, c.saveSettings(func(s *settings.Settings) { s.CardTypes = params.CardTypes })

	// ---- settings ----

	case "settings.save":
		// The whole snapshot, as the console received it from the agent and
		// edited one field of. Replacing rather than merging is what makes an
		// unticked card type stick.
		var params settings.Settings
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return nil, c.saveSettings(func(s *settings.Settings) { *s = params })

	case "clients.disconnect":
		var params struct {
			ID string `json:"id"`
		}
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		// A client is free to reconnect immediately; this ends the session it
		// has, it does not bar it. Removing its origin is what bars it.
		return nil, c.host.DisconnectClient(params.ID)

	// ---- paired devices ----

	case "devices.revoke":
		var params struct {
			ID string `json:"id"`
		}
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return nil, c.host.RevokeDevice(params.ID)

	case "devices.revokeAll":
		return nil, c.host.RevokeAllDevices()

	case "devices.setRequirePaired":
		var params struct {
			Enabled bool `json:"enabled"`
		}
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		// Persisted, unlike the tray's session-only version of this toggle.
		return nil, c.saveSettings(func(s *settings.Settings) { s.RequirePairedDevice = params.Enabled })

	// ---- origins ----

	case "origins.allow":
		var params struct {
			Origin string `json:"origin"`
		}
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return nil, c.host.AllowOrigin(params.Origin)

	case "origins.revoke":
		var params struct {
			Origin string `json:"origin"`
		}
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return nil, c.host.RevokeOrigin(params.Origin)

	case "origins.setAllowAny":
		var params struct {
			Enabled bool `json:"enabled"`
		}
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		if params.Enabled {
			// Never persisted, matching the tray.
			log.Printf("Warning: origin checking disabled for this session from the control center; any site the operator visits can drive the reader")
		}
		c.host.SetOriginCheckDisabled(params.Enabled)
		return nil, nil

	// ---- security ----

	case "security.rotateAPISecret":
		secret, err := c.host.RotateAPISecret()
		if err != nil {
			return nil, err
		}
		return map[string]any{"apiSecret": secret}, nil

	case "security.rotatePairingPIN":
		pin, err := c.host.RotatePairingPIN()
		if err != nil {
			return nil, err
		}
		return map[string]any{"pin": pin}, nil

	case "security.installCA":
		if err := c.host.InstallCA(); err != nil {
			return nil, err
		}
		// The new certificate only reaches a browser on a fresh listener.
		return nil, c.host.RebindListener()

	case "security.regenerateCertificate":
		if err := c.host.RegenerateCertificate(); err != nil {
			return nil, err
		}
		// Takes effect only on a fresh listener.
		return nil, c.host.RebindListener()

	case "security.revokeControlSessions":
		// Includes the caller's own session, which is the point.
		c.auth.RevokeAll()
		return nil, nil

	default:
		return nil, fmt.Errorf("unknown action %q", req.Action)
	}
}

// saveSettings persists a change. Applying it is the host's business, and what
// the agent holds afterwards arrives in the next snapshot.
func (c *Server) saveSettings(mutate func(*settings.Settings)) error {
	_, err := c.host.SaveSettings(mutate)
	return err
}

// decodeParams unpacks an action's parameters; absent params are not an error.
func decodeParams(raw json.RawMessage, into any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("malformed parameters: %w", err)
	}
	return nil
}
