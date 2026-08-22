package traymenu_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/traymenu"
)

// newMenu returns a menu on a fake driver, closed when the test ends.
func newMenu(t *testing.T) (*traymenu.Menu, *traymenu.Fake) {
	t.Helper()
	fake := traymenu.NewFake()
	menu := traymenu.New(fake)
	t.Cleanup(menu.Close)
	return menu, fake
}

func TestBuildsTree(t *testing.T) {
	menu, fake := newMenu(t)

	menu.SetIcon([]byte{1, 2, 3})
	menu.SetTooltip("NFC Agent")

	menu.Add("Starting...", traymenu.Tooltip("Agent status"), traymenu.Disabled())
	menu.AddSeparator()

	urls := menu.AddSubmenu("Server URLs")
	urls.Add("Device: Not running", traymenu.Disabled())
	urls.Add("Copy Device URL")
	urls.Add("Pairing PIN", traymenu.HiddenIf(true))

	mode := menu.AddSubmenu("Mode: Read/Write")
	mode.AddCheckbox("Read/Write Mode", true)
	mode.AddCheckbox("Read Only Mode", false)

	menu.AddSeparator()
	menu.Add("Quit")

	want := strings.Join([]string{
		"Starting... (disabled)",
		"----",
		"Server URLs",
		"  Device: Not running (disabled)",
		"  Copy Device URL",
		"  Pairing PIN (hidden)",
		"Mode: Read/Write",
		"  [x] Read/Write Mode",
		"  [ ] Read Only Mode",
		"----",
		"Quit",
		"",
	}, "\n")

	if got := fake.Render(); got != want {
		t.Errorf("menu rendered as:\n%s\nwant:\n%s", got, want)
	}
	if string(fake.Icon()) != "\x01\x02\x03" {
		t.Errorf("icon = %v, want [1 2 3]", fake.Icon())
	}
	if fake.Tooltip() != "NFC Agent" {
		t.Errorf("tooltip = %q, want %q", fake.Tooltip(), "NFC Agent")
	}
	if got := fake.Find("Server URLs", "Device: Not running").Tooltip(); got != "" {
		t.Errorf("tooltip = %q, want empty", got)
	}
	if got := fake.Find("Starting...").Tooltip(); got != "Agent status" {
		t.Errorf("tooltip = %q, want %q", got, "Agent status")
	}
}

func TestFindMissingReturnsNil(t *testing.T) {
	menu, fake := newMenu(t)
	menu.AddSubmenu("Mode").Add("Read Only")

	if fake.Find("Mode", "Write Only") != nil {
		t.Error("Find returned an item for a title that is not there")
	}
	if fake.Find("Nope") != nil {
		t.Error("Find returned an item for a top level title that is not there")
	}
}

func TestClickRunsHandlersInOrder(t *testing.T) {
	menu, _ := newMenu(t)

	var got []string
	item := menu.Add("Quit", traymenu.OnClick(func() { got = append(got, "option") }))
	item.OnClick(func() { got = append(got, "method") })
	item.Clicked().Connect(func(clicked *traymenu.Item) { got = append(got, clicked.Title()) })

	item.Click()

	want := []string{"option", "method", "Quit"}
	if len(got) != len(want) {
		t.Fatalf("handlers ran %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("handlers ran %v, want %v", got, want)
		}
	}
}

func TestDisconnectedHandlerStopsRunning(t *testing.T) {
	menu, _ := newMenu(t)

	calls := 0
	item := menu.Add("Refresh")
	conn := item.OnClick(func() { calls++ })

	item.Click()
	conn.Disconnect()
	item.Click()

	if calls != 1 {
		t.Fatalf("handler ran %d times, want 1", calls)
	}
}

func TestDisabledAndHiddenItemsIgnoreClicks(t *testing.T) {
	menu, _ := newMenu(t)

	clicks := 0
	disabled := menu.Add("Status", traymenu.Disabled(), traymenu.OnClick(func() { clicks++ }))
	hidden := menu.Add("Secret", traymenu.Hidden(), traymenu.OnClick(func() { clicks++ }))

	disabled.Click()
	hidden.Click()
	if clicks != 0 {
		t.Fatalf("a disabled or hidden item ran %d handlers, want 0", clicks)
	}

	disabled.Enable()
	hidden.Show()
	disabled.Click()
	hidden.Click()
	if clicks != 2 {
		t.Fatalf("handlers ran %d times after enabling, want 2", clicks)
	}
}

func TestStateChangesReachTheDriver(t *testing.T) {
	menu, fake := newMenu(t)

	item := menu.AddCheckbox("Flash and Beep on Scan", false)
	native := fake.Find("Flash and Beep on Scan")

	if item.Checked() || native.Checked() {
		t.Fatal("item starts checked")
	}
	if !item.Checkbox() {
		t.Fatal("Checkbox() = false for an item added with AddCheckbox")
	}

	if got := item.Toggle(); !got || !item.Checked() || !native.Checked() {
		t.Fatalf("Toggle returned %v; item checked = %v, platform checked = %v", got, item.Checked(), native.Checked())
	}

	item.SetTitle("Quiet on Scan")
	item.SetTooltip("no feedback")
	item.Disable()
	item.Hide()

	if native.Title() != "Quiet on Scan" || item.Title() != "Quiet on Scan" {
		t.Errorf("title = %q / %q, want %q", item.Title(), native.Title(), "Quiet on Scan")
	}
	if native.Tooltip() != "no feedback" || item.Tooltip() != "no feedback" {
		t.Errorf("tooltip = %q / %q, want %q", item.Tooltip(), native.Tooltip(), "no feedback")
	}
	if item.Enabled() || native.Enabled() {
		t.Error("item still enabled after Disable")
	}
	if item.Visible() || native.Visible() {
		t.Error("item still visible after Hide")
	}
}

// TestPlatformClicksAreNotDropped covers the reason this package exists: the
// tray library drops a click when nobody is receiving on the item's channel.
func TestPlatformClicksAreNotDropped(t *testing.T) {
	menu, fake := newMenu(t)

	const clicks = 50
	seen := make(chan struct{}, clicks)
	menu.Add("Refresh Devices", traymenu.OnClick(func() { seen <- struct{}{} }))
	native := fake.Find("Refresh Devices")

	for i := 0; i < clicks; i++ {
		native.Deliver()
	}

	for i := 0; i < clicks; i++ {
		select {
		case <-seen:
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d clicks reached the handler", i, clicks)
		}
	}
}

func TestHandlersRunOneAtATime(t *testing.T) {
	menu, fake := newMenu(t)

	var mu sync.Mutex
	inFlight, maxInFlight := 0, 0
	done := make(chan struct{}, 20)

	menu.Add("Work", traymenu.OnClick(func() {
		mu.Lock()
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		mu.Unlock()

		time.Sleep(time.Millisecond)

		mu.Lock()
		inFlight--
		mu.Unlock()
		done <- struct{}{}
	}))
	native := fake.Find("Work")

	for i := 0; i < 20; i++ {
		native.Deliver()
	}
	for i := 0; i < 20; i++ {
		<-done
	}

	mu.Lock()
	defer mu.Unlock()
	if maxInFlight != 1 {
		t.Fatalf("%d handlers ran at once, want 1", maxInFlight)
	}
}

func TestPanickingHandlerIsReportedNotFatal(t *testing.T) {
	menu, _ := newMenu(t)

	var logged string
	menu.SetLogger(func(format string, args ...any) { logged = format })

	boom := menu.Add("Boom", traymenu.OnClick(func() { panic("handler bug") }))
	boom.Click()

	if logged == "" {
		t.Fatal("a panicking handler was not reported")
	}

	// The dispatcher survived it.
	survived := false
	next := menu.Add("Next", traymenu.OnClick(func() { survived = true }))
	next.Click()
	if !survived {
		t.Fatal("the menu stopped handling clicks after a panic")
	}
}

func TestRunCallsBackAndQuitReturns(t *testing.T) {
	fake := traymenu.NewFake()
	menu := traymenu.New(fake)

	ready := make(chan struct{})
	exited := make(chan struct{})
	returned := make(chan struct{})

	go func() {
		menu.Run(
			func() { close(ready) },
			func() { close(exited) },
		)
		close(returned)
	}()

	<-ready
	if !fake.Running() {
		t.Fatal("driver is not running after onReady")
	}

	menu.Quit()

	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("onExit was not called")
	}
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after Quit")
	}
}

func TestCloseIsIdempotentAndStopsClicks(t *testing.T) {
	menu, _ := newMenu(t)

	clicks := 0
	item := menu.Add("Quit", traymenu.OnClick(func() { clicks++ }))

	menu.Close()
	menu.Close()

	item.Click() // must return rather than block
	if clicks != 0 {
		t.Fatalf("handler ran %d times after Close, want 0", clicks)
	}
}
