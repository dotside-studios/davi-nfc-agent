package agent

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// originsTray activates an agent serving behind store, with a tray to draw the
// allowlist into.
func originsTray(t *testing.T, p *ServerPlugin) (*Agent, *traymenu.Fake) {
	t.Helper()

	a := New(Config{
		Manager: nfc.NewMockManager(),
		Logger:  log.New(io.Discard, "", 0),
		Plugins: []Plugin{p},
	})

	fake := traymenu.NewFake()
	menu := traymenu.New(fake)
	t.Cleanup(menu.Close)
	if err := a.Activate(menu); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	return a, fake
}

// originStore returns a store on disk, since the section is drawn from what it
// holds rather than from the clicks that put things there.
func originStore(t *testing.T) *server.OriginStore {
	t.Helper()

	store, err := server.NewOriginStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewOriginStore: %v", err)
	}
	return store
}

// A refused page shows up as a one-click offer to allow it, and an allowed one
// as a tick that revokes.
func TestOriginsMenuOffersBlockedOriginsAndAllowsThem(t *testing.T) {
	store := originStore(t)
	p := &ServerPlugin{Origins: store}
	originsTray(t, p)

	store.RecordBlocked("https://console.example")

	row := findOriginRow(t, p, "Allow console.example")
	row.Click()

	if !store.Allowed("console.example") {
		t.Fatal("the origin was not allowed")
	}

	allowed := findOriginRow(t, p, "console.example")
	if !allowed.Checked() {
		t.Error("an allowed origin is not ticked")
	}
	allowed.Click()

	if store.Allowed("console.example") {
		t.Fatal("the origin was not revoked")
	}
}

func TestOriginsAllowAnyToggle(t *testing.T) {
	store := originStore(t)
	p := &ServerPlugin{Origins: store}
	originsTray(t, p)

	p.originAllowAny.Click()
	if !store.IsSessionAllowAny() || !p.originAllowAny.Checked() {
		t.Fatal("the session escape hatch did not turn on")
	}

	p.originAllowAny.Click()
	if store.IsSessionAllowAny() || p.originAllowAny.Checked() {
		t.Fatal("the session escape hatch did not turn back off")
	}
}

// An origin allowed from the console shows as allowed in the tray, without the
// operator reopening the menu.
func TestAnOriginAllowedElsewhereRedrawsTheMenu(t *testing.T) {
	store := originStore(t)
	p := &ServerPlugin{Origins: store}
	originsTray(t, p)

	if err := store.Allow("console.example"); err != nil {
		t.Fatalf("Allow: %v", err)
	}

	if row := findOriginRow(t, p, "console.example"); !row.Checked() {
		t.Error("the allowed origin is not ticked in the menu")
	}
}

// The allowlist is the server's, so a build passing one through on the command
// line hands it here and the plugin builds the store behind it.
func TestTheAllowlistIsSeededFromWhatTheBuildWasTold(t *testing.T) {
	p := &ServerPlugin{AllowedOrigins: []string{"console.example"}}
	originsTray(t, p)

	if p.Origins == nil {
		t.Fatal("no store was built")
	}
	if !p.Origins.Allowed("console.example") {
		t.Error("the origin named on the command line is not allowed")
	}
	if p.Origins.IsSessionAllowAny() {
		t.Error("the check is off without anything asking for it")
	}
}

// "*" is the escape hatch, and it turns the check off for the run rather than
// being stored as an origin.
func TestAnAllowlistOfAnyTurnsTheCheckOff(t *testing.T) {
	p := &ServerPlugin{AllowedOrigins: []string{"*"}}
	originsTray(t, p)

	if !p.Origins.IsSessionAllowAny() {
		t.Error("the check is still on")
	}
	for _, origin := range p.Origins.List() {
		if origin == "*" {
			t.Error(`"*" was stored as an origin`)
		}
	}
}

// Whoever serves an upgrade on the agent's behalf asks the plugin, which reads
// the allowlist per request: an origin allowed while running is admitted
// without the handler being rebuilt.
func TestTheServerAnswersWhichOriginsMayConnect(t *testing.T) {
	store := originStore(t)
	p := &ServerPlugin{Origins: store}
	originsTray(t, p)

	check := p.CheckOrigin()
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Host = "127.0.0.1:9470"
	req.Header.Set("Origin", "https://console.example")

	if check(req) {
		t.Fatal("an unlisted origin was admitted")
	}
	if err := store.Allow("console.example"); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if !check(req) {
		t.Error("an allowed origin was refused by a checker handed over before it was allowed")
	}
}

// A console is built beside the plugin rather than after it, so it subscribes
// before there is a store to subscribe to.
func TestAWatcherRegisteredBeforeActivationFollowsTheAllowlist(t *testing.T) {
	p := &ServerPlugin{}

	var changes int
	p.OnOriginsChange(func() { changes++ })

	originsTray(t, p)
	if err := p.Origins.Allow("console.example"); err != nil {
		t.Fatalf("Allow: %v", err)
	}
	if changes == 0 {
		t.Fatal("an allowed origin did not reach the watcher")
	}

	before := changes
	p.Origins.RecordBlocked("https://evil.example")
	if changes == before {
		t.Error("a refused connection did not reach the watcher")
	}
}

// The client server serves behind the same allowlist: an upgrade from an
// unlisted page is refused, and the refusal is recorded so the console and the
// tray can offer it.
func TestAClientFromAnUnlistedOriginIsRefused(t *testing.T) {
	store := originStore(t)
	p := &ServerPlugin{Origins: store}
	a, _ := originsTray(t, p)

	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Host = "127.0.0.1:9470"
	req.Header.Set("Origin", "https://evil.example")
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")

	rec := httptest.NewRecorder()
	p.Listener().Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("an unlisted origin got %d, want 403", rec.Code)
	}
	if blocked := store.Blocked(); len(blocked) != 1 || blocked[0] != "evil.example" {
		t.Errorf("the store recorded %v, want the refused origin", blocked)
	}
}

// findOriginRow returns the visible origin row with the given label.
func findOriginRow(t *testing.T, p *ServerPlugin, title string) *traymenu.Item {
	t.Helper()

	for _, item := range p.origins.Items() {
		if item.Visible() && item.Title() == title {
			return item
		}
	}

	var shown []string
	for _, item := range p.origins.Items() {
		if item.Visible() {
			shown = append(shown, item.Title())
		}
	}
	t.Fatalf("no origin row titled %q; the menu shows %v", title, shown)
	return nil
}
