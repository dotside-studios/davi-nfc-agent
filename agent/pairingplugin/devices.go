package pairingplugin

import (
	"fmt"

	"github.com/dotside-studios/davi-nfc-agent/secure/pairing"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// pairedSlotCount bounds the paired devices shown. The pool is fixed and
// reused, as with origins: a row added on a refresh would land after the
// actions below it.
const pairedSlotCount = 12

// setupDevicesMenu builds the Paired Devices submenu under section.
func (p *Plugin) setupDevicesMenu(section traymenu.Container) {
	p.devicesMenu = section.AddSubmenu("Paired Devices",
		traymenu.Tooltip("Phones and readers that have paired with this agent"))

	// Declared first so the devices sit above the actions that apply to all of
	// them: menu order is the order items are added.
	p.pairedDevices = traymenu.NewList[string](p.devicesMenu, pairedSlotCount)
	p.pairedDevices.OnActivate(func(row traymenu.Row[string]) { p.revokeDevice(row.Value) })

	p.requirePaired = p.devicesMenu.AddCheckbox(
		"Require pairing",
		false,
		traymenu.Tooltip("Admit only devices that have paired. The shared secret stops admitting devices; browser consoles are unaffected."),
		traymenu.OnClick(p.handleRequirePaired),
	)

	p.revokeAll = p.devicesMenu.Add(
		"Revoke all devices",
		traymenu.Tooltip("Every paired device must pair again. Use when the machine changes hands."),
		traymenu.OnClick(p.handleRevokeAllDevices),
	)
}

// PairedDevices is the credential store this plugin lists and revokes from,
// nil on a plugin built without one. For a console showing the same devices.
func (p *Plugin) PairedDevices() pairing.Store {
	if p == nil {
		return nil
	}
	return p.Devices
}

func (p *Plugin) devices() pairing.Store { return p.PairedDevices() }

// refreshDevicesMenu redraws the submenu from the store.
func (p *Plugin) refreshDevicesMenu() {
	if p.devices() == nil || p.pairedDevices == nil {
		return
	}

	devices := p.devices().List()

	rows := make([]traymenu.Row[string], 0, len(devices))
	for _, device := range devices {
		label := device.Name
		if device.Platform != "" {
			label = fmt.Sprintf("%s (%s)", device.Name, device.Platform)
		}
		rows = append(rows, traymenu.Row[string]{
			Value:   device.ID,
			Title:   label,
			Tooltip: fmt.Sprintf("Paired %s, click to revoke", device.PairedAt.Local().Format("2006-01-02 15:04")),
		})
	}

	if dropped := p.pairedDevices.Set(rows); dropped > 0 {
		p.logf("%d more devices are paired than the menu can show; revoke from the control center", dropped)
	}

	if len(devices) == 0 {
		p.devicesMenu.SetTitle("Paired Devices (none)")
		p.revokeAll.Hide()
	} else {
		p.devicesMenu.SetTitle(fmt.Sprintf("Paired Devices (%d)", len(devices)))
		p.revokeAll.Show()
	}

	if p.agent != nil {
		p.requirePaired.SetChecked(p.agent.RequirePairedDevice())
	}
}

// handleRequirePaired toggles the paired-device requirement live, so it can be
// tried against a real device without restarting the agent.
func (p *Plugin) handleRequirePaired() {
	if p.agent == nil {
		return
	}
	on := !p.requirePaired.Checked()

	if on && p.devices() != nil && p.devices().Count() == 0 {
		// Turning this on with nothing paired locks out every device, which is
		// unlikely to be what the operator meant from this menu.
		p.logf("Not requiring pairing: no devices are paired, so every device would be refused")
		p.requirePaired.SetChecked(false)
		return
	}

	p.agent.SetRequirePairedDevice(on)

	if p.agent.RequirePairedDevice() {
		p.logf("Requiring paired devices; the shared secret no longer admits one")
	} else {
		p.logf("No longer requiring paired devices")
	}

	p.refreshDevicesMenu()
}

// revokeDevice drops one paired device, which then has to pair again.
func (p *Plugin) revokeDevice(id string) {
	if p.devices() == nil || id == "" {
		return
	}

	if err := p.devices().Revoke(id); err != nil {
		p.logf("Failed to revoke device %s: %v", id, err)
		return
	}

	p.logf("Revoked device %s", id)
	p.refreshDevicesMenu()
}

// handleRevokeAllDevices clears the store.
func (p *Plugin) handleRevokeAllDevices() {
	if p.devices() == nil {
		return
	}

	if err := p.devices().RevokeAll(); err != nil {
		p.logf("Failed to revoke devices: %v", err)
		return
	}

	p.logf("Revoked all paired devices")
	p.refreshDevicesMenu()
}
