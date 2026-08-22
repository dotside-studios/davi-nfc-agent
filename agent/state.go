package agent

import (
	"reflect"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/plugin"
)

// stateInterval is how often the agent looks itself over for the changes
// nothing reports: a card arriving at the reader, or leaving it.
const stateInterval = 500 * time.Millisecond

// Snapshot is what the agent looks like right now, as much of it as a plugin
// has any business seeing.
//
// Every value comes from the agent itself, never from a menu and never from a
// plugin: what a plugin acts on should be what the agent will do, not what a
// checkbox happens to show.
func (a *Agent) Snapshot() plugin.State {
	state := plugin.State{
		Running:  a.Reader != nil,
		Device:   a.CurrentDevicePath(),
		Settings: a.Settings(),
		Explicit: a.Explicit(),
	}

	if a.Devices != nil {
		state.Paired = a.Devices.Count()
	}
	if card := a.LastCard(); card != nil && card.UID != "" {
		state.Card = plugin.Card{Present: true, UID: card.UID, Type: card.Type}
	}
	return state
}

// PublishState hands a fresh snapshot to the plugins watching. It is called
// wherever the agent already knows something changed; WatchState covers what
// nothing announces.
func (a *Agent) PublishState() { a.Plugins().Publish(a.Snapshot()) }

// WatchState publishes the agent's state whenever it differs from the last one
// published, until the agent shuts down.
//
// It is what makes a plugin reactive without polling of its own: a card at the
// reader is announced by nothing, so something has to look, and one watcher for
// every plugin is better than one per plugin. A build that never calls this
// still sees the changes the agent announces itself — starting, stopping,
// restarting, a settings change — just not a card arriving.
func (a *Agent) WatchState(interval time.Duration) {
	if interval <= 0 {
		interval = stateInterval
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		last := a.Plugins().State()
		for {
			select {
			case <-a.stateDone:
				return
			case <-ticker.C:
				next := a.Snapshot()
				if reflect.DeepEqual(next, last) {
					continue
				}
				last = next
				a.Plugins().Publish(next)
			}
		}
	}()
}
