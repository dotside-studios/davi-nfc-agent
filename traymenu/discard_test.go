package traymenu

import "testing"

// A menu on the discard driver is a menu in every respect but being drawn. It
// is what a plugin adds its entries to where there is no tray, so nothing it
// does may panic and nothing may block.
func TestDiscardDrawsNothingAndTolerantOfEverything(t *testing.T) {
	menu := New(Discard())
	defer menu.Close()

	menu.SetIcon([]byte{1, 2, 3})
	menu.SetTooltip("nothing to see")

	item := menu.Add("Back Up Now", Tooltip("runs a backup"))
	item.SetTitle("Backing Up...")
	item.SetChecked(true)
	item.Disable()
	item.Hide()

	if got := item.Title(); got != "Backing Up..." {
		t.Errorf("Title() = %q, want the state the item was given", got)
	}

	section := menu.Section("Extensions")
	section.Set("backup", "Back Up Now")
	if section.Len() != 1 {
		t.Errorf("Len() = %d, want the entry registered", section.Len())
	}

	// A discarded item is never clicked, so a click delivered to it does
	// nothing rather than hanging on a dispatch that never comes.
	item.Show()
	item.Enable()
	item.Click()

	menu.AddSeparator()
	item.Remove()
}

// New(nil) used to mean the real tray. It now means nothing is drawn, which is
// what makes the agent buildable without a toolkit.
func TestNilDriverDrawsNothing(t *testing.T) {
	menu := New(nil)
	defer menu.Close()

	if _, ok := menu.driver.(discard); !ok {
		t.Errorf("driver = %T, want the discard driver", menu.driver)
	}
}
