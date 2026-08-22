package traymenu_test

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

type mode int

const (
	readWrite mode = iota
	readOnly
	writeOnly
)

func TestRadioTicksOneOption(t *testing.T) {
	menu, fake := newMenu(t)

	group := traymenu.NewRadio[mode](menu.AddSubmenu("Mode"))
	group.Add(readWrite, "Read/Write Mode", traymenu.Tooltip("Allow both"))
	group.Add(readOnly, "Read Only Mode")
	group.Add(writeOnly, "Write Only Mode")

	group.Set(readOnly)

	if got := fake.Render(); got != "Mode\n  [ ] Read/Write Mode\n  [x] Read Only Mode\n  [ ] Write Only Mode\n" {
		t.Fatalf("menu rendered as:\n%s", got)
	}

	if value, ok := group.Value(); !ok || value != readOnly {
		t.Fatalf("Value = (%v, %v), want (readOnly, true)", value, ok)
	}
}

func TestRadioSetDoesNotEmit(t *testing.T) {
	menu, _ := newMenu(t)

	group := traymenu.NewRadio[mode](menu.AddSubmenu("Mode"))
	group.Add(readWrite, "Read/Write Mode")
	group.Add(readOnly, "Read Only Mode")

	emitted := 0
	group.OnSelect(func(mode) { emitted++ })

	group.Set(readOnly)
	if emitted != 0 {
		t.Fatalf("Set raised Selected %d times, want 0", emitted)
	}
}

func TestRadioClickSelectsAndEmits(t *testing.T) {
	menu, _ := newMenu(t)

	group := traymenu.NewRadio[mode](menu.AddSubmenu("Mode"))
	group.Add(readWrite, "Read/Write Mode")
	group.Add(readOnly, "Read Only Mode")
	group.Set(readWrite)

	var seen []mode
	group.OnSelect(func(m mode) { seen = append(seen, m) })

	group.Item(readOnly).Click()

	if len(seen) != 1 || seen[0] != readOnly {
		t.Fatalf("Selected raised with %v, want [readOnly]", seen)
	}
	if !group.Item(readOnly).Checked() || group.Item(readWrite).Checked() {
		t.Fatal("the clicked option is not the ticked one")
	}

	// Clicking the ticked option still means "make this so".
	group.Item(readOnly).Click()
	if len(seen) != 2 {
		t.Fatalf("Selected raised %d times, want 2", len(seen))
	}
}

func TestRadioValueSetBeforeOptionsExist(t *testing.T) {
	menu, _ := newMenu(t)

	group := traymenu.NewRadio[mode](menu.AddSubmenu("Mode"))
	group.Set(writeOnly)

	group.Add(readWrite, "Read/Write Mode")
	late := group.Add(writeOnly, "Write Only Mode")

	if !late.Checked() {
		t.Fatal("an option added after Set is not ticked")
	}
}

func TestRadioItemForUnknownValue(t *testing.T) {
	menu, _ := newMenu(t)

	group := traymenu.NewRadio[mode](menu.AddSubmenu("Mode"))
	group.Add(readWrite, "Read/Write Mode")

	if group.Item(writeOnly) != nil {
		t.Fatal("Item returned something for a value that was never added")
	}
}
