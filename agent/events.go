package agent

import (
	"strconv"

	"github.com/dotside-studios/davi-nfc-agent/event"
	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// Change names what changed, for a subscriber to [Events.Any].
type Change int

const (
	ChangeState Change = iota
	ChangePreferences
	ChangeClients
	ChangeServers
	ChangeDevices
	ChangeOrigins
	ChangeBlocked
)

func (c Change) String() string {
	switch c {
	case ChangeState:
		return "state"
	case ChangePreferences:
		return "preferences"
	case ChangeClients:
		return "clients"
	case ChangeServers:
		return "servers"
	case ChangeDevices:
		return "devices"
	case ChangeOrigins:
		return "origins"
	case ChangeBlocked:
		return "blocked"
	}
	return "change(" + strconv.Itoa(int(c)) + ")"
}

// Events is what the agent reports: one signal per thing that can change, and
// [Events.Any] for a subscriber that redraws on all of them.
//
//	conn := ctx.Events.Preferences.Connect(func(p agent.Preferences) { render(p) })
//	defer conn.Disconnect()
//
// Handlers run on the goroutine that made the change, in the order they
// connected, and the typed signal fires before Any. A handler must not block:
// hand slow work to a goroutine of your own. Connecting and disconnecting is
// safe at any time, including from inside a handler and while the agent runs.
type Events struct {
	// State carries every settled lifecycle transition.
	State event.Signal[State]

	// Preferences carries the preferences after each change, whoever made it.
	Preferences event.Signal[Preferences]

	// Clients carries the number of connected clients after each connect and
	// disconnect.
	Clients event.Signal[int]

	// Servers carries the port the listeners are bound on, after a restart has
	// rebuilt them.
	Servers event.Signal[int]

	// Devices carries the paired devices after each pairing and revocation.
	// Silent on an agent built without a registry.
	Devices event.Signal[[]PairedDevice]

	// Origins carries the allowlist after each edit. Silent on an agent built
	// without an origin store.
	Origins event.Signal[[]string]

	// Blocked carries each origin refused a connection, once per origin.
	Blocked event.Signal[string]

	// Tag carries every scan the agent broadcasts, so a program embedding the
	// agent acts on cards without connecting to its own WebSocket endpoint.
	//
	// Any does not repeat it: a scan is traffic rather than a change of state,
	// and a subscriber redrawing on Any should not redraw per card.
	Tag event.Signal[nfc.NFCData]

	// Any carries the kind of every emission above except Tag, for a
	// subscriber that redraws rather than acting on the value.
	Any event.Signal[Change]
}

// Events is what the agent reports. The signals are live from New, so a
// subscriber may connect before the agent starts and stay connected across a
// restart.
func (a *Agent) Events() *Events { return &a.events }

// watchStores republishes the origin and device stores through the agent, so a
// subscriber follows one surface rather than three. Called from New, before the
// agent is handed out.
func (a *Agent) watchStores() {
	if a.origins != nil {
		a.origins.changed.Connect(a.fireOriginsChanged)
		a.origins.rejected.Connect(a.fireOriginBlocked)
	}
	if a.devices != nil {
		a.devices.changed.Connect(a.fireDevicesChanged)
	}
}

func (a *Agent) fireState(s State) {
	a.events.State.Emit(s)
	a.events.Any.Emit(ChangeState)
}

func (a *Agent) firePreferencesChanged() {
	a.events.Preferences.Emit(a.Preferences())
	a.events.Any.Emit(ChangePreferences)
}

func (a *Agent) fireClientsChanged(count int) {
	a.events.Clients.Emit(count)
	a.events.Any.Emit(ChangeClients)
}

func (a *Agent) fireServerRestart() {
	a.events.Servers.Emit(a.DevicePort())
	a.events.Any.Emit(ChangeServers)
}

func (a *Agent) fireDevicesChanged(devices []PairedDevice) {
	a.events.Devices.Emit(devices)
	a.events.Any.Emit(ChangeDevices)
}

func (a *Agent) fireOriginsChanged(origins []string) {
	a.events.Origins.Emit(origins)
	a.events.Any.Emit(ChangeOrigins)
}

func (a *Agent) fireOriginBlocked(origin string) {
	a.events.Blocked.Emit(origin)
	a.events.Any.Emit(ChangeBlocked)
}
