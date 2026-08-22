package surface

import (
	"fmt"
	"sync"

	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// FakeHost is a Host with no agent behind it, for testing a plugin. It records
// what the plugin asked of the agent and lets the test move the agent under it:
//
//	fake := traymenu.NewFake()
//	menu := traymenu.New(fake)
//	defer menu.Close()
//
//	host := surface.NewFakeHost(menu.AddSubmenu("Turnstile"))
//	plugin.Attach(host)
//
//	host.Publish(surface.State{Running: true, Card: surface.Card{Present: true, UID: "04A2"}})
//	fake.Render() // what the plugin drew
type FakeHost struct {
	menu      traymenu.Container
	endpoints *Endpoints

	// OpenErr is what Open returns, for the machine with no browser on it.
	OpenErr error

	mu      sync.Mutex
	state   State
	copied  []Copied
	opened  []string
	logs    []string
	changed traymenu.Signal[State]
}

// Copied is one value a plugin put on the clipboard.
type Copied struct {
	What  string
	Value string
}

// NewFakeHost returns a host whose menu is the given container.
func NewFakeHost(menu traymenu.Container) *FakeHost {
	return &FakeHost{menu: menu, endpoints: NewEndpoints()}
}

func (f *FakeHost) Menu() traymenu.Container { return f.menu }
func (f *FakeHost) Endpoints() *Endpoints    { return f.endpoints }

func (f *FakeHost) State() State {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

func (f *FakeHost) Watch(fn func(State)) *traymenu.Connection { return f.changed.Connect(fn) }

func (f *FakeHost) Copy(what, value string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.copied = append(f.copied, Copied{What: what, Value: value})
}

func (f *FakeHost) Open(target string) error {
	f.mu.Lock()
	f.opened = append(f.opened, target)
	err := f.OpenErr
	f.mu.Unlock()
	return err
}

func (f *FakeHost) Logf(format string, args ...any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logs = append(f.logs, fmt.Sprintf(format, args...))
}

// Publish moves the agent under the plugin: it replaces the state and raises
// the watchers with it, as the tray does.
func (f *FakeHost) Publish(state State) {
	f.mu.Lock()
	f.state = state
	f.mu.Unlock()

	f.changed.Emit(state)
}

// Copied returns what the plugin put on the clipboard, oldest first.
func (f *FakeHost) Copied() []Copied {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Copied(nil), f.copied...)
}

// Opened returns what the plugin asked a browser to show, oldest first.
func (f *FakeHost) Opened() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.opened...)
}

// Logs returns what the plugin logged, oldest first.
func (f *FakeHost) Logs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.logs...)
}
