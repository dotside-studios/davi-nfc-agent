//go:build !nowebui

package console

import (
	"encoding/json"
	"fmt"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"log"
)

// dispatch routes a console action to the code that performs it. Named actions
// rather than REST verbs: most of these are operations, not resource edits.
func (c *Server) dispatch(req action) (any, error) {
	switch req.Action {

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
		c.host.ApplyPreferences(func(s *Preferences) { s.DevicePath = params.DevicePath })
		return nil, nil

	case "reader.setMode":
		var params struct {
			Mode string `json:"mode"`
		}
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		switch params.Mode {
		case nfc.ModeNameReadWrite, nfc.ModeNameReadOnly, nfc.ModeNameWriteOnly:
		default:
			return nil, fmt.Errorf("unknown mode %q", params.Mode)
		}
		return nil, c.applyPreferences(func(s *Preferences) { s.Mode = params.Mode })

	case "reader.setCardTypes":
		var params struct {
			CardTypes []string `json:"cardTypes"`
		}
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return nil, c.applyPreferences(func(s *Preferences) { s.CardTypes = params.CardTypes })

	case "settings.save":
		// The whole snapshot, as the console received it from the agent and
		// edited one field of. Replacing rather than merging is what makes an
		// unticked card type stick.
		var params Preferences
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return nil, c.applyPreferences(func(s *Preferences) { *s = params })

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
		return nil, c.applyPreferences(func(s *Preferences) { s.RequirePairedDevice = params.Enabled })

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
		// Installing reissues the certificate, and whatever serves it binds
		// again on its own.
		return nil, c.host.InstallCA()

	case "security.regenerateCertificate":
		// As above: the reissue is the event, and the listener follows it.
		return nil, c.host.RegenerateCertificate()

	case "security.revokeControlSessions":
		// Includes the caller's own session.
		c.auth.RevokeAll()
		return nil, nil

	default:
		return nil, fmt.Errorf("unknown action %q", req.Action)
	}
}

// applyPreferences changes the agent. What it holds afterwards arrives in the
// next snapshot, so nothing here reads the answer back.
func (c *Server) applyPreferences(mutate func(*Preferences)) error {
	c.host.ApplyPreferences(mutate)
	return nil
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
