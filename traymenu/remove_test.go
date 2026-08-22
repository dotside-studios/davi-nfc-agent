package traymenu_test

import (
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

func TestRemoveTakesTheItemOffTheMenu(t *testing.T) {
	menu, fake := newMenu(t)

	menu.Add("Keep")
	drop := menu.Add("Drop")
	menu.Add("Also Keep")

	drop.Remove()

	if got := fake.Render(); got != "Keep\nAlso Keep\n" {
		t.Fatalf("menu rendered as:\n%s", got)
	}
	if !drop.Removed() {
		t.Error("Removed() = false after Remove")
	}
	if fake.Find("Drop") != nil {
		t.Error("the removed item is still findable")
	}
}

func TestRemoveTakesTheSubmenuWithIt(t *testing.T) {
	menu, fake := newMenu(t)

	devices := menu.AddSubmenu("Devices")
	child := devices.Add("pcsc:0")
	menu.Add("Quit")

	devices.Remove()

	if got := fake.Render(); got != "Quit\n" {
		t.Fatalf("menu rendered as:\n%s", got)
	}
	if !child.Removed() {
		t.Error("a child of the removed submenu does not know it went")
	}
	if fake.Find("Devices") != nil {
		t.Error("the removed submenu is still findable")
	}

	// The tray library re-adds an item on any state change, so an unmarked
	// child would put itself back under a parent that no longer exists.
	child.SetTitle("back?")
	if got := fake.Render(); got != "Quit\n" {
		t.Fatalf("a child of a removed submenu came back:\n%s", got)
	}
}

func TestAddingToARemovedSubmenuIsInert(t *testing.T) {
	menu, fake := newMenu(t)

	devices := menu.AddSubmenu("Devices")
	devices.Remove()

	orphan := devices.Add("pcsc:0")
	devices.AddSeparator()

	if !orphan.Removed() {
		t.Error("an item added to a removed submenu is live")
	}
	orphan.SetTitle("still here?")
	orphan.Click()

	if got := fake.Render(); got != "" {
		t.Fatalf("the menu grew under a removed submenu:\n%s", got)
	}
}

// A removed item must stay removed: the tray library re-adds an item to the
// platform menu on any state change, so an unguarded SetTitle would bring it
// back.
func TestRemovedItemIgnoresEverything(t *testing.T) {
	menu, fake := newMenu(t)

	item := menu.Add("Drop", traymenu.Tooltip("before"))
	native := fake.Find("Drop")

	clicked := false
	item.OnClick(func() { clicked = true })

	item.Remove()

	item.SetTitle("back?")
	item.SetTooltip("after")
	item.SetChecked(true)
	item.Enable()
	item.Show()
	item.Click()

	if item.Title() != "Drop" || item.Tooltip() != "before" {
		t.Errorf("removed item took a state change: title %q, tooltip %q", item.Title(), item.Tooltip())
	}
	if item.Checked() || !item.Visible() {
		t.Error("removed item took a checkmark or visibility change")
	}
	if clicked {
		t.Error("a removed item ran its handler")
	}
	if native.Title() != "Drop" {
		t.Errorf("the platform saw a change on a removed item: %q", native.Title())
	}
	if got := fake.Render(); got != "" {
		t.Fatalf("the removed item came back:\n%s", got)
	}
}

func TestToggleOnRemovedItemKeepsItsState(t *testing.T) {
	menu, _ := newMenu(t)

	item := menu.AddCheckbox("Flash and Beep on Scan", true)
	item.Remove()

	if got := item.Toggle(); !got || !item.Checked() {
		t.Fatalf("Toggle on a removed item returned %v, want its unchanged state", got)
	}
}

func TestRemoveIsIdempotent(t *testing.T) {
	menu, _ := newMenu(t)

	item := menu.Add("Drop")
	item.Remove()
	item.Remove()
}

func TestRemoveStopsTheWatcher(t *testing.T) {
	menu, fake := newMenu(t)

	item := menu.Add("Drop")
	native := fake.Find("Drop")

	clicks := make(chan struct{}, 1)
	item.OnClick(func() { clicks <- struct{}{} })

	native.Deliver()
	select {
	case <-clicks:
	case <-time.After(5 * time.Second):
		t.Fatal("the click never arrived")
	}

	item.Remove()
	native.Deliver() // a no-op on a removed item, and must not panic

	select {
	case <-clicks:
		t.Fatal("a removed item still delivers clicks")
	case <-time.After(50 * time.Millisecond):
	}
}
