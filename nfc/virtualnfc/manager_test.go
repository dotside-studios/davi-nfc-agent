package virtualnfc

import (
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// readOnlyCard is a minimal presentable card whose tag reports read-only, enough
// to exercise the field/registry without silicon or a route.
func readOnlyCard(uid string) *Card {
	return NewCard(uid, NewRoutedTag(RoutedTagConfig{UID: uid, Type: "test", Technology: "ISO14443A"}))
}

func TestPollDeviceGetTagsReflectsField(t *testing.T) {
	d := NewDevice("reader-0", PollMode, "")

	tags, err := d.GetTags()
	if err != nil || len(tags) != 0 {
		t.Fatalf("empty field GetTags = %v, %v; want none", tags, err)
	}

	d.Present(readOnlyCard("04:AA"), readOnlyCard("04:BB"))
	tags, _ = d.GetTags()
	if len(tags) != 2 || tags[0].UID() != "04:AA" || tags[1].UID() != "04:BB" {
		t.Fatalf("after Present, GetTags order = %v, want [04:AA 04:BB]", uids(tags))
	}

	d.Remove("04:AA")
	tags, _ = d.GetTags()
	if len(tags) != 1 || tags[0].UID() != "04:BB" {
		t.Fatalf("after Remove, GetTags = %v, want [04:BB]", uids(tags))
	}
}

func TestPollDeviceDoesNotEmit(t *testing.T) {
	m := NewManager()
	d := NewDevice("reader-0", PollMode, "")
	m.Plug("reader-0", d)

	var got int
	conn := nfc.OnScan(m, func(nfc.ScannedTag) { got++ })
	defer conn.Disconnect()

	d.Present(readOnlyCard("04:AA"))
	if got != 0 {
		t.Fatalf("poll device emitted %d scans, want 0", got)
	}
}

func TestEventDeviceEmitsThroughManager(t *testing.T) {
	m := NewManager()
	d := NewDevice("phone-1", EventMode, "smartphone")
	m.Plug("phone-1", d)

	var scans []nfc.ScannedTag
	conn := nfc.OnScan(m, func(s nfc.ScannedTag) { scans = append(scans, s) })
	defer conn.Disconnect()

	d.Present(readOnlyCard("04:AA"))
	d.Remove("04:AA")

	if len(scans) != 2 {
		t.Fatalf("got %d scans, want 2 (arrival + removal)", len(scans))
	}
	if scans[0].Device != "phone-1" || scans[0].Tag == nil || scans[0].Tag.UID() != "04:AA" {
		t.Errorf("arrival = %+v, want device phone-1 tag 04:AA", scans[0])
	}
	if scans[1].Device != "phone-1" || scans[1].Tag != nil || scans[1].RemovedUID != "04:AA" {
		t.Errorf("removal = %+v, want device phone-1 RemovedUID 04:AA", scans[1])
	}
}

// A manager can host a polled reader and an event phone at once; the polled one
// stays out of the Scans() stream and is listed as pollable.
func TestManagerHostsBothModes(t *testing.T) {
	m := NewManager()
	m.Plug("reader-0", NewDevice("reader-0", PollMode, ""))
	m.Plug("phone-1", NewDevice("phone-1", EventMode, "smartphone"))

	listings, err := m.Devices()
	if err != nil {
		t.Fatal(err)
	}
	if len(listings) != 2 {
		t.Fatalf("Devices() = %d, want 2", len(listings))
	}
	byPath := map[string]nfc.DeviceListing{}
	for _, l := range listings {
		byPath[l.Path] = l
	}
	if got := byPath["reader-0"].Capabilities; !got.CanPoll || got.SupportsEvents {
		t.Errorf("reader-0 caps = %+v, want CanPoll and not SupportsEvents", got)
	}
	if got := byPath["phone-1"].Capabilities; got.CanPoll || !got.SupportsEvents {
		t.Errorf("phone-1 caps = %+v, want SupportsEvents and not CanPoll", got)
	}

	// nfc.ListReaders keeps only the pollable device, exactly as the Supervisor
	// would when deciding what to open.
	readers, err := nfc.ListReaders(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(readers) != 1 || readers[0] != "reader-0" {
		t.Fatalf("ListReaders = %v, want [reader-0]", readers)
	}
}

func TestPlugUnplugNotifiesAndStopsReporting(t *testing.T) {
	m := NewManager()
	d := NewDevice("phone-1", EventMode, "smartphone")

	m.Plug("phone-1", d)
	if !drained(m.DeviceChanges()) {
		t.Error("Plug did not signal DeviceChanges")
	}

	m.Unplug("phone-1")
	if !drained(m.DeviceChanges()) {
		t.Error("Unplug did not signal DeviceChanges")
	}
	if _, err := m.OpenDevice("phone-1"); err == nil {
		t.Error("OpenDevice after Unplug succeeded, want error")
	}

	// After unplug the device no longer reaches the manager's signal.
	var got int
	conn := nfc.OnScan(m, func(nfc.ScannedTag) { got++ })
	defer conn.Disconnect()
	d.Present(readOnlyCard("04:AA"))
	if got != 0 {
		t.Fatalf("unplugged device emitted %d scans, want 0", got)
	}
}

func TestManagerPresentRemoveByPath(t *testing.T) {
	m := NewManager()
	m.Plug("reader-0", NewDevice("reader-0", PollMode, ""))

	if err := m.Present("reader-0", readOnlyCard("04:AA")); err != nil {
		t.Fatal(err)
	}
	if err := m.Present("nope", readOnlyCard("04:BB")); err == nil {
		t.Error("Present to unknown path succeeded, want error")
	}

	d, _ := m.OpenDevice("reader-0")
	tags, _ := d.GetTags()
	if len(tags) != 1 || tags[0].UID() != "04:AA" {
		t.Fatalf("GetTags = %v, want [04:AA]", uids(tags))
	}

	if err := m.Remove("reader-0", "04:AA"); err != nil {
		t.Fatal(err)
	}
	tags, _ = d.GetTags()
	if len(tags) != 0 {
		t.Fatalf("after Remove GetTags = %v, want none", uids(tags))
	}
}

func uids(tags []nfc.Tag) []string {
	out := make([]string, len(tags))
	for i, t := range tags {
		out[i] = t.UID()
	}
	return out
}

// drained reports whether a signal was pending on the change channel.
func drained(ch <-chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}
