//go:build !nocontrol

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
)

// dispatch routes a console action to the code that performs it. Named actions
// rather than REST verbs: most of these are operations, not resource edits.
func (c *ControlServer) dispatch(req controlAction) (any, error) {
	switch req.Action {

	// ---- agent lifecycle ----

	case "agent.start":
		if c.OnStart == nil {
			return nil, errors.New("agent cannot be started from here")
		}
		return nil, c.OnStart()

	case "agent.stop":
		if c.OnStop == nil {
			return nil, errors.New("agent cannot be stopped from here")
		}
		c.OnStop()
		return nil, nil

	case "agent.restartServers":
		return nil, c.agent.RestartServers()

	case "agent.quit":
		if c.OnQuit == nil {
			return nil, errors.New("agent cannot be quit from here")
		}
		// Deferred so the response reaches the console before the process exits.
		go c.OnQuit()
		return nil, nil

	// ---- reader ----

	case "reader.selectDevice":
		var params struct {
			DevicePath string `json:"devicePath"`
		}
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		if c.OnSelectDevice == nil {
			return nil, errors.New("device cannot be changed from here")
		}
		if err := c.OnSelectDevice(params.DevicePath); err != nil {
			return nil, err
		}
		_, err := c.settings.Update(func(s *Settings) { s.DevicePath = params.DevicePath })
		return nil, err

	case "reader.setMode":
		var params struct {
			Mode string `json:"mode"`
		}
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		switch params.Mode {
		case ModeReadWrite, ModeReadOnly, ModeWriteOnly:
		default:
			return nil, fmt.Errorf("unknown mode %q", params.Mode)
		}
		return nil, c.saveSettings(func(s *Settings) { s.Mode = params.Mode })

	case "reader.setCardTypes":
		var params struct {
			CardTypes []string `json:"cardTypes"`
		}
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return nil, c.saveSettings(func(s *Settings) { s.CardTypes = params.CardTypes })

	// ---- settings ----

	case "settings.save":
		var params Settings
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		return nil, c.saveSettings(func(s *Settings) {
			port := params.Port
			*s = params
			s.Port = port
		})

	case "clients.disconnect":
		var params struct {
			ID string `json:"id"`
		}
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		if c.agent.ClientServer == nil {
			return nil, errors.New("agent is not running")
		}
		// A client is free to reconnect immediately; this ends the session it
		// has, it does not bar it. Removing its origin is what bars it.
		if !c.agent.ClientServer.Disconnect(params.ID) {
			return nil, errors.New("no such client — it may have already disconnected")
		}
		return nil, nil

	// ---- paired devices ----

	case "devices.revoke":
		var params struct {
			ID string `json:"id"`
		}
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		if c.agent.Devices == nil {
			return nil, errors.New("no device registry")
		}
		return nil, c.agent.Devices.Revoke(params.ID)

	case "devices.revokeAll":
		if c.agent.Devices == nil {
			return nil, errors.New("no device registry")
		}
		return nil, c.agent.Devices.RevokeAll()

	case "devices.setRequirePaired":
		var params struct {
			Enabled bool `json:"enabled"`
		}
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		// Persisted, unlike the tray's session-only version of this toggle.
		return nil, c.saveSettings(func(s *Settings) { s.RequirePairedDevice = params.Enabled })

	// ---- origins ----

	case "origins.allow":
		var params struct {
			Origin string `json:"origin"`
		}
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		if c.agent.Origins == nil {
			return nil, errors.New("no origin store")
		}
		return nil, c.agent.Origins.Allow(params.Origin)

	case "origins.revoke":
		var params struct {
			Origin string `json:"origin"`
		}
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		if c.agent.Origins == nil {
			return nil, errors.New("no origin store")
		}
		return nil, c.agent.Origins.Revoke(params.Origin)

	case "origins.setAllowAny":
		var params struct {
			Enabled bool `json:"enabled"`
		}
		if err := decodeParams(req.Params, &params); err != nil {
			return nil, err
		}
		if c.agent.Origins == nil {
			return nil, errors.New("no origin store")
		}
		if params.Enabled {
			// Never persisted, matching the tray.
			log.Printf("Warning: origin checking disabled for this session from the control center; any site the operator visits can drive the reader")
		}
		c.agent.Origins.SessionAllowAny(params.Enabled)
		return nil, nil

	// ---- security ----

	case "security.rotateAPISecret":
		secret, err := c.agent.RotateAPISecret()
		if err != nil {
			return nil, err
		}
		return map[string]any{"apiSecret": secret}, nil

	case "security.rotatePairingPIN":
		if c.bootstrap == nil {
			return nil, errors.New("pairing server is disabled")
		}
		return map[string]any{"pin": c.bootstrap.RotatePIN()}, nil

	case "security.regenerateCertificate":
		if c.agent.TLSManager == nil {
			return nil, errors.New("agent is not managing its own certificates")
		}
		if err := c.agent.TLSManager.RegenerateCertificates(); err != nil {
			return nil, err
		}
		// Takes effect only on a fresh listener.
		return nil, c.agent.RestartServers()

	case "security.revokeControlSessions":
		// Includes the caller's own session, which is the point.
		c.auth.RevokeAll()
		return nil, nil

	default:
		return nil, fmt.Errorf("unknown action %q", req.Action)
	}
}

// saveSettings persists a change, applies it to the running agent and lets the
// tray follow it.
func (c *ControlServer) saveSettings(mutate func(*Settings)) error {
	saved, err := c.settings.Update(mutate)
	if err != nil {
		return err
	}

	saved.Apply(c.agent)
	if c.OnSettings != nil {
		c.OnSettings(saved)
	}
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
