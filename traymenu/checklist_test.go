package traymenu_test

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

func newChecklist(t *testing.T) (*traymenu.Checklist[string], *traymenu.Fake) {
	t.Helper()

	menu, fake := newMenu(t)
	list := traymenu.NewChecklist[string](menu.AddSubmenu("Card Type Filter"))
	list.AddAll("All Types")
	list.Add("NTAG213", "NTAG213")
	list.Add("DESFire", "DESFire")
	return list, fake
}

func TestChecklistStartsOnAll(t *testing.T) {
	list, fake := newChecklist(t)

	want := "Card Type Filter\n  [x] All Types\n  [ ] NTAG213\n  [ ] DESFire\n"
	if got := fake.Render(); got != want {
		t.Fatalf("menu rendered as:\n%s\nwant:\n%s", got, want)
	}
	if len(list.Values()) != 0 {
		t.Errorf("Values = %v, want none", list.Values())
	}
}

func TestChecklistTicksAndUnticks(t *testing.T) {
	list, _ := newChecklist(t)

	var seen [][]string
	list.OnChange(func(values []string) { seen = append(seen, values) })

	list.Item("DESFire").Click()
	list.Item("NTAG213").Click()

	if got := list.Values(); len(got) != 2 || got[0] != "NTAG213" || got[1] != "DESFire" {
		t.Fatalf("Values = %v, want [NTAG213 DESFire] in the order they were added", got)
	}
	if list.All().Checked() {
		t.Error("the all entry is still ticked with two types picked")
	}
	if len(seen) != 2 || len(seen[0]) != 1 || len(seen[1]) != 2 {
		t.Fatalf("Changed raised with %v", seen)
	}

	// Unticking the last one puts the all entry back.
	list.Item("DESFire").Click()
	list.Item("NTAG213").Click()

	if got := list.Values(); len(got) != 0 {
		t.Fatalf("Values = %v, want none", got)
	}
	if !list.All().Checked() {
		t.Error("the all entry did not come back when nothing was ticked")
	}
	if last := seen[len(seen)-1]; len(last) != 0 {
		t.Errorf("last Changed carried %v, want none", last)
	}
}

func TestChecklistAllClearsTheRest(t *testing.T) {
	list, fake := newChecklist(t)

	list.Item("NTAG213").Click()
	list.All().Click()

	want := "Card Type Filter\n  [x] All Types\n  [ ] NTAG213\n  [ ] DESFire\n"
	if got := fake.Render(); got != want {
		t.Fatalf("menu rendered as:\n%s\nwant:\n%s", got, want)
	}
	if len(list.Values()) != 0 {
		t.Errorf("Values = %v, want none", list.Values())
	}
}

func TestChecklistSetDoesNotEmit(t *testing.T) {
	list, _ := newChecklist(t)

	emitted := 0
	list.OnChange(func([]string) { emitted++ })

	list.Set([]string{"DESFire"})

	if emitted != 0 {
		t.Fatalf("Set raised Changed %d times, want 0", emitted)
	}
	if !list.Item("DESFire").Checked() || list.Item("NTAG213").Checked() {
		t.Error("Set did not tick exactly the values it was given")
	}
	if list.All().Checked() {
		t.Error("the all entry is ticked with a type selected")
	}

	list.Set(nil)
	if !list.All().Checked() || list.Item("DESFire").Checked() {
		t.Error("Set with nothing did not fall back to the all entry")
	}
}

func TestChecklistWithoutAnAllEntry(t *testing.T) {
	menu, _ := newMenu(t)

	list := traymenu.NewChecklist[int](menu.AddSubmenu("Ports"))
	list.Add(1, "One")
	list.Add(2, "Two")

	if list.All() != nil {
		t.Fatal("All returned an item that was never added")
	}

	list.Item(2).Click()
	if got := list.Values(); len(got) != 1 || got[0] != 2 {
		t.Fatalf("Values = %v, want [2]", got)
	}
}

func TestChecklistItemForUnknownValue(t *testing.T) {
	list, _ := newChecklist(t)

	if list.Item("Type4") != nil {
		t.Fatal("Item returned something for a value that was never added")
	}
}
