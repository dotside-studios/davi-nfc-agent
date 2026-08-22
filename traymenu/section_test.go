package traymenu_test

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

func TestSectionKeepsItsPlace(t *testing.T) {
	menu, fake := newMenu(t)

	plugins := traymenu.NewSection(menu, "Plugins", traymenu.Tooltip("Registered elsewhere"))
	menu.AddSeparator()
	menu.Add("Quit")

	// Registered long after Quit, and still under Plugins.
	plugins.Set("backup", "Back Up Now")
	plugins.Set("sync", "Sync")

	want := "Plugins\n  Back Up Now\n  Sync\n----\nQuit\n"
	if got := fake.Render(); got != want {
		t.Fatalf("menu rendered as:\n%s\nwant:\n%s", got, want)
	}
	if plugins.Item().Tooltip() != "Registered elsewhere" {
		t.Errorf("tooltip = %q", plugins.Item().Tooltip())
	}
}

func TestSectionSetReplacesByKey(t *testing.T) {
	menu, fake := newMenu(t)

	plugins := traymenu.NewSection(menu, "Plugins")
	first := plugins.Set("backup", "Back Up Now")
	plugins.Set("sync", "Sync")

	second := plugins.Set("backup", "Back Up (2 pending)")

	if plugins.Len() != 2 {
		t.Fatalf("Len = %d, want 2: re-registering added a second entry", plugins.Len())
	}
	if !first.Removed() {
		t.Error("the replaced item was not removed")
	}
	if plugins.Get("backup") != second {
		t.Error("Get returns the item that was replaced")
	}

	// The replacement goes to the end, since that is where new items land.
	want := "Plugins\n  Sync\n  Back Up (2 pending)\n"
	if got := fake.Render(); got != want {
		t.Fatalf("menu rendered as:\n%s\nwant:\n%s", got, want)
	}
	if keys := plugins.Keys(); len(keys) != 2 || keys[0] != "sync" || keys[1] != "backup" {
		t.Fatalf("Keys = %v, want [sync backup]", keys)
	}
}

func TestSectionEntriesClick(t *testing.T) {
	menu, _ := newMenu(t)

	plugins := traymenu.NewSection(menu, "Plugins")

	ran := false
	plugins.Set("backup", "Back Up Now", traymenu.OnClick(func() { ran = true })).Click()

	if !ran {
		t.Fatal("a registered entry does not run its handler")
	}
}

func TestSectionRemove(t *testing.T) {
	menu, fake := newMenu(t)

	plugins := traymenu.NewSection(menu, "Plugins")
	plugins.Set("backup", "Back Up Now")
	plugins.Set("sync", "Sync")

	if !plugins.Remove("backup") {
		t.Fatal("Remove reported nothing to remove")
	}
	if plugins.Remove("backup") {
		t.Fatal("Remove reported removing the same key twice")
	}

	if got := fake.Render(); got != "Plugins\n  Sync\n" {
		t.Fatalf("menu rendered as:\n%s", got)
	}
	if plugins.Get("backup") != nil {
		t.Error("Get still returns a removed entry")
	}
	if plugins.Len() != 1 {
		t.Errorf("Len = %d, want 1", plugins.Len())
	}
}

func TestSectionClear(t *testing.T) {
	menu, fake := newMenu(t)

	plugins := traymenu.NewSection(menu, "Plugins")
	plugins.Set("backup", "Back Up Now")
	plugins.Set("sync", "Sync")

	plugins.Clear()

	if got := fake.Render(); got != "Plugins\n" {
		t.Fatalf("menu rendered as:\n%s", got)
	}
	if plugins.Len() != 0 || len(plugins.Keys()) != 0 {
		t.Errorf("Len = %d, Keys = %v, want empty", plugins.Len(), plugins.Keys())
	}

	// And it takes registrations again afterwards.
	plugins.Set("backup", "Back Up Now")
	if got := fake.Render(); got != "Plugins\n  Back Up Now\n" {
		t.Fatalf("menu rendered as:\n%s", got)
	}
}

func TestSectionInSubmenu(t *testing.T) {
	menu, fake := newMenu(t)

	tools := menu.AddSubmenu("Tools")
	plugins := traymenu.NewSection(tools, "Plugins")
	plugins.Set("backup", "Back Up Now")

	want := "Tools\n  Plugins\n    Back Up Now\n"
	if got := fake.Render(); got != want {
		t.Fatalf("menu rendered as:\n%s\nwant:\n%s", got, want)
	}
}
