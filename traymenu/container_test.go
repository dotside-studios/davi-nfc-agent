package traymenu

import "testing"

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
