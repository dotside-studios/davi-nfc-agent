package traymenu_test

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

func rows(values ...string) []traymenu.Row[string] {
	out := make([]traymenu.Row[string], 0, len(values))
	for _, v := range values {
		out = append(out, traymenu.Row[string]{Value: v, Title: v, Tooltip: "click to revoke", Checked: true})
	}
	return out
}

func TestListShowsRowsAndHidesTheRest(t *testing.T) {
	menu, fake := newMenu(t)

	list := traymenu.NewList[string](menu.AddSubmenu("Allowed Origins"), 3, traymenu.Checkbox(false))
	if list.Cap() != 3 || list.Len() != 0 {
		t.Fatalf("Cap/Len = %d/%d, want 3/0", list.Cap(), list.Len())
	}

	if dropped := list.Set(rows("https://a.test", "https://b.test")); dropped != 0 {
		t.Fatalf("dropped %d rows, want 0", dropped)
	}

	want := "Allowed Origins\n  [x] https://a.test\n  [x] https://b.test\n  [ ]  (hidden)\n"
	if got := fake.Render(); got != want {
		t.Fatalf("menu rendered as:\n%s\nwant:\n%s", got, want)
	}
	if list.Len() != 2 {
		t.Fatalf("Len = %d, want 2", list.Len())
	}
	if got := fake.Find("Allowed Origins", "https://a.test").Tooltip(); got != "click to revoke" {
		t.Fatalf("tooltip = %q, want %q", got, "click to revoke")
	}
}

func TestListReusesSlotsAndReportsOverflow(t *testing.T) {
	menu, _ := newMenu(t)

	list := traymenu.NewList[string](menu.AddSubmenu("Paired Devices"), 2)
	list.Set(rows("phone", "reader"))

	dropped := list.Set(rows("laptop", "tablet", "watch"))
	if dropped != 1 {
		t.Fatalf("dropped = %d, want 1", dropped)
	}

	got := list.Rows()
	if len(got) != 2 || got[0].Value != "laptop" || got[1].Value != "tablet" {
		t.Fatalf("rows = %v, want [laptop tablet]", got)
	}

	// Same pool, relabelled.
	if len(list.Items()) != 2 {
		t.Fatalf("pool grew to %d items, want 2", len(list.Items()))
	}
}

func TestListActivateCarriesTheRow(t *testing.T) {
	menu, _ := newMenu(t)

	list := traymenu.NewList[string](menu.AddSubmenu("Devices"), 3)
	list.Set(rows("pcsc:0", "pcsc:1"))

	var seen []traymenu.Row[string]
	list.OnActivate(func(row traymenu.Row[string]) { seen = append(seen, row) })

	list.Items()[1].Click()

	if len(seen) != 1 || seen[0].Value != "pcsc:1" {
		t.Fatalf("Activated raised with %v, want the pcsc:1 row", seen)
	}
}

func TestListIgnoresClicksOnEmptySlots(t *testing.T) {
	menu, _ := newMenu(t)

	list := traymenu.NewList[string](menu.AddSubmenu("Devices"), 3)
	list.Set(rows("pcsc:0"))

	activations := 0
	list.OnActivate(func(traymenu.Row[string]) { activations++ })

	// Hidden, so a click is refused twice over: by the item and by the list.
	list.Items()[2].Click()
	if activations != 0 {
		t.Fatalf("an empty slot raised Activated %d times, want 0", activations)
	}

	list.Clear()
	if list.Len() != 0 {
		t.Fatalf("Len = %d after Clear, want 0", list.Len())
	}
	if list.Items()[0].Visible() {
		t.Fatal("a cleared slot is still visible")
	}
}

func TestListHandlerMayRedrawTheList(t *testing.T) {
	menu, _ := newMenu(t)

	list := traymenu.NewList[string](menu.AddSubmenu("Origins"), 4, traymenu.Checkbox(false))
	list.Set(rows("https://a.test", "https://b.test"))

	// The real menus all redraw themselves from the store the click just
	// changed, so Set has to be callable from inside a handler.
	list.OnActivate(func(row traymenu.Row[string]) {
		if row.Value == "https://a.test" {
			list.Set(rows("https://b.test"))
		}
	})

	list.Items()[0].Click()

	got := list.Rows()
	if len(got) != 1 || got[0].Value != "https://b.test" {
		t.Fatalf("rows = %v, want [https://b.test]", got)
	}
}
