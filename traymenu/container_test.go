package traymenu

import (
	"strings"
	"testing"
)

// A Section is a Container, so a plugin or a List handed one adds to it as it
// would to any other menu.
func TestSectionIsAContainer(t *testing.T) {
	fake := NewFake()
	menu := New(fake)
	defer menu.Close()

	var section Container = NewSection(menu, "Extensions")
	section.Add("Back Up Now")
	nested := section.Section("Servers")
	nested.Set("device", "Device: 9470")
	section.AddSeparator()

	if item := fake.Find("Extensions", "Back Up Now"); item == nil {
		t.Errorf("the entry is not in the section:\n%s", fake.Render())
	}
	if item := fake.Find("Extensions", "Servers", "Device: 9470"); item == nil {
		t.Errorf("the nested section is not there:\n%s", fake.Render())
	}
}

// Children is how a host finds out whether anything landed in the submenu it
// handed over, so it can leave an empty one hidden.
func TestChildrenReportsWhatLanded(t *testing.T) {
	menu := New(NewFake())
	defer menu.Close()

	parent := menu.AddSubmenu("Extensions")
	if got := parent.Children(); len(got) != 0 {
		t.Fatalf("Children() = %d on an empty submenu, want none", len(got))
	}

	first := parent.Add("Back Up Now")
	parent.Add("Restore")

	titles := func() string {
		var out []string
		for _, child := range parent.Children() {
			out = append(out, child.Title())
		}
		return strings.Join(out, ",")
	}
	if got := titles(); got != "Back Up Now,Restore" {
		t.Errorf("Children() = %q, want both in order", got)
	}

	// A removed entry is not something that landed: it is gone.
	first.Remove()
	if got := titles(); got != "Restore" {
		t.Errorf("Children() = %q after a removal, want only what is left", got)
	}
}
