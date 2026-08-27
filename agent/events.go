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
	ChangeServers
	ChangeDevices
)

func (c Change) String() string {
	switch c {
	case ChangeState:
		return "state"
	case ChangePreferences:
		return "preferences"
	case ChangeServers:
		return "servers"
	case ChangeDevices:
		return "devices"
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
//
// The signals carrying state are [event.Property]: connecting calls the handler
// with the current value, so a subscriber draws its first frame without reading
// the agent separately. Tag, Reader and Any carry traffic and do not.
type Events struct {
	// State carries every settled lifecycle transition.
	State event.Property[State]

	// Preferences carries the preferences after each change, whoever made it.
	Preferences event.Property[Preferences]

	// Servers carries the agent's configured port, after a listener has bound
	// again. Emitted when a reissued certificate rebinds one.
	Servers event.Property[int]

	// Reader carries the reader's status: whether it is connected, and whether
	// a card is on it. Emitted while the agent runs, which is when there is a
	// reader to report on.
	//
	// Any does not repeat it, for the same reason it leaves out Tag: a card
	// arriving and leaving would redraw every page twice per tap.
	Reader event.Signal[nfc.DeviceStatus]

	// Readers carries the readers that can be picked, whenever the set
	// changes. Emitted on a manager that reports device changes; the current
	// value is [Agent.Readers] either way.
	Readers event.Property[[]string]

	// Devices carries the paired devices after each pairing and revocation.
	// Empty on an agent built without a registry.
	Devices event.Property[[]PairedDevice]

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

// publishEvents points the state-carrying signals at what they report on, so
// connecting to one answers with the current value. Called from New, before the
// agent is handed out; the functions run on the subscriber's goroutine.
func (a *Agent) publishEvents() {
	a.events.State.Current = a.State
	a.events.Preferences.Current = a.Preferences
	a.events.Servers.Current = a.DevicePort
	a.events.Readers.Current = a.Readers
	a.events.Devices.Current = func() []PairedDevice {
		if a.devices == nil {
			return nil
		}
		return a.devices.List()
	}
}

// watchStores republishes the device store through the agent, so a subscriber
// follows one surface rather than two. Called from New, before the
// agent is handed out.
func (a *Agent) watchStores() {
	if a.devices != nil {
		a.devices.changed.Connect(a.fireDevicesChanged)
	}
}

// watchManager republishes the manager's device changes as Events().Readers. It
// runs for the agent's lifetime rather than for a run: a reader is plugged in
// whether or not the agent is started.
func (a *Agent) watchManager() {
	notifier, ok := a.manager.(nfc.DeviceChangeNotifier)
	if !ok {
		return
	}

	changes := notifier.DeviceChanges()
	go func() {
		for {
			select {
			case <-a.done:
				return
			case _, ok := <-changes:
				if !ok {
					return
				}
				a.events.Readers.Emit(a.Readers())
			}
		}
	}()
}

func (a *Agent) fireState(s State) {
	a.events.State.Emit(s)
	a.events.Any.Emit(ChangeState)
}

func (a *Agent) firePreferencesChanged() {
	a.events.Preferences.Emit(a.Preferences())
	a.events.Any.Emit(ChangePreferences)
}

// ServerRebound reports that a listener bound again, raising Events().Servers
// with the agent's configured port. Called by whatever serves the agent, which
// is a plugin rather than the agent itself, so the agent cannot raise it.
func (a *Agent) ServerRebound() { a.fireServerRestart() }

func (a *Agent) fireServerRestart() {
	a.events.Servers.Emit(a.DevicePort())
	a.events.Any.Emit(ChangeServers)
}

func (a *Agent) fireDevicesChanged(devices []PairedDevice) {
	a.events.Devices.Emit(devices)
	a.events.Any.Emit(ChangeDevices)
}

func (a *Agent) fireReaderStatus(status nfc.DeviceStatus) {
	a.events.Reader.Emit(status)
}
