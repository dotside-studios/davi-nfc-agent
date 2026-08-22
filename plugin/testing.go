package plugin

import (
	"fmt"
	"sync"

	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// Harness runs plugins with no agent behind them, for testing one.
//
// It is a real [Host] — the same lifecycle, the same contexts — over a tray
// that records a menu instead of drawing one, so a test drives a plugin the way
// the agent does:
//
//	h := plugin.NewHarness(&turnstile{})
//	defer h.Close()
//
//	h.Init()
//	h.Start()
//	h.Publish(plugin.State{Running: true, Card: plugin.Card{Present: true, UID: "04A2"}})
//
//	h.Tray.Find("Turnstile", "Hold Gate Open").Click()
//	h.Render()
//	h.Copied()
type Harness struct {
	*Host

	// Tray is the fake the menus are drawn on.
	Tray *traymenu.Fake

	// Menu is the menu itself, for adding entries of the agent's own beside the
	// plugins'.
	Menu *traymenu.Menu

	// OpenErr is what Open returns, for the machine with no browser on it.
	OpenErr error

	mu     sync.Mutex
	copied []Copied
	opened []string
	logs   []string
}

// Copied is one value a plugin put on the clipboard.
type Copied struct {
	What  string
	Value string
}

// NewHarness returns a harness with the plugins registered but no phase run
// yet, so a test can watch them run.
func NewHarness(plugins ...Plugin) *Harness {
	fake := traymenu.NewFake()
	h := &Harness{Tray: fake, Menu: traymenu.New(fake)}

	h.Host = New(Config{
		Logf:      h.logf,
		Menus:     h,
		Clipboard: h.copy,
		Browser:   h.open,
	})
	_ = h.Use(plugins...)
	return h
}

// MenuFor gives each plugin a submenu named after it, as the tray does.
func (h *Harness) MenuFor(info Info) traymenu.Container {
	return h.Menu.AddSubmenu(info.Name(), traymenu.Tooltip(info.Tooltip))
}

// Close closes the host and the menu behind it.
func (h *Harness) Close() error {
	err := h.Host.Close()
	h.Menu.Close()
	return err
}

// Render returns the whole menu as text.
func (h *Harness) Render() string { return h.Tray.Render() }

// Copied returns what the plugins put on the clipboard, oldest first.
func (h *Harness) Copied() []Copied {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]Copied(nil), h.copied...)
}

// Opened returns what they asked a browser to show, oldest first.
func (h *Harness) Opened() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.opened...)
}

// Logs returns what they logged, oldest first.
func (h *Harness) Logs() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.logs...)
}

func (h *Harness) copy(what, value string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.copied = append(h.copied, Copied{What: what, Value: value})
}

func (h *Harness) open(target string) error {
	h.mu.Lock()
	h.opened = append(h.opened, target)
	err := h.OpenErr
	h.mu.Unlock()
	return err
}

func (h *Harness) logf(format string, args ...any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.logs = append(h.logs, fmt.Sprintf(format, args...))
}
