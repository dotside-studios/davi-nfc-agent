package tray

import (
	"github.com/dotside-studios/davi-nfc-agent/pairing"
	"strings"
	"testing"

	nfcagent "github.com/dotside-studios/davi-nfc-agent/agent"
	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// pluginFunc is a plugin written as one function.
type pluginFunc func(nfcagent.AgentContext) error

func (f pluginFunc) Activate(ctx nfcagent.AgentContext) error { return f(ctx) }

// A plugin's entry goes on the top level, indistinguishable from one the tray
// declared itself, and lands where the tray activated the plugins rather than
// after Quit, which is where anything added later would go.
func TestAPluginsEntryIsATopLevelEntry(t *testing.T) {
	a := newTestAgent()
	if err := a.Plugins.Add(pluginFunc(func(ctx nfcagent.AgentContext) error {
		ctx.Systray.Add("Back Up Now", traymenu.Tooltip("runs a backup"))
		return nil
	})); err != nil {
		t.Fatalf("Add: %v", err)
	}

	_, fake := newTestTray(t, a)

	entry := fake.Find("Back Up Now")
	if entry == nil {
		t.Fatalf("the plugin's entry is not on the menu:\n%s", fake.Render())
	}
	if !entry.Visible() {
		t.Error("the plugin's entry is hidden")
	}

	// Under the status line, above what the tray declares itself: the
	// addresses a listener serves on are what this menu is opened for.
	got := titles(fake)
	status, backup, device := indexOf(got, "Starting..."), indexOf(got, "Back Up Now"), indexOf(got, "Device")
	if status < 0 || backup < 0 || device < 0 || backup < status || backup > device {
		t.Errorf("menu reads:\n%s\n\nthe plugin's entry should sit between the status and the tray's own entries", strings.Join(got, "\n"))
	}
}

// indexOf reports where a title appears in the menu, or -1.
func indexOf(titles []string, want string) int {
	for i, title := range titles {
		if title == want {
			return i
		}
	}
	return -1
}

// The pairing entries belong to the pairing plugin now, so they appear in the
// section the tray hands the plugins rather than in its own URLs submenu.
func TestPairingEntriesComeFromThePlugin(t *testing.T) {
	a := newTestAgent()
	pairing := nfcagent.NewPairingPlugin(pairing.NewServer(pairing.ServerOptions{}), 9498)
	if err := a.Plugins.Add(pairing); err != nil {
		t.Fatalf("Plugins.Add: %v", err)
	}

	_, fake := newTestTray(t, a)

	pin := fake.Find("Pairing", "Pairing PIN: "+pairing.PIN())
	if pin == nil {
		t.Fatalf("the plugin's PIN entry is not on the menu:\n%s", fake.Render())
	}
	if fake.Find("Server URLs", "Pairing PIN: --") != nil {
		t.Error("the tray still declares a pairing entry of its own")
	}

	// Rotating relabels the entry, wherever the rotation came from: the menu
	// item, or the control center through the same method.
	before := pairing.PIN()
	fresh := pairing.RotatePIN()
	if fresh == before {
		t.Fatal("RotatePIN returned the PIN it replaced")
	}
	if got := pin.Title(); got != "Pairing PIN: "+fresh {
		t.Errorf("PIN entry reads %q, want the fresh PIN", got)
	}
}

// A build that registers no pairing plugin has no pairing entries at all.
func TestNoPairingPluginNoPairingEntries(t *testing.T) {
	_, fake := newTestTray(t, newTestAgent())

	if item := fake.Find("Pairing"); item != nil {
		t.Errorf("a pairing submenu appeared with no plugin behind it:\n%s", fake.Render())
	}
}
