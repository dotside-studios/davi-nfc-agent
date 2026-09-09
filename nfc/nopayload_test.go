package nfc

import (
	"errors"
	"testing"
	"time"
)

// pollingReader stands up a reader over a mock device holding tag, connected and
// ready for doPoll.
func pollingReader(t *testing.T, tag Tag) *deviceReader {
	t.Helper()

	manager := NewMockManager()
	manager.DevicesList = []string{"mock:usb:001"}
	device := NewMockDevice()
	device.SetTags([]Tag{tag})
	manager.MockDevice = device

	reader, err := newDeviceReaderWithClock("mock:usb:001", manager, 5*time.Second, NewFakeClock(time.Now()))
	if err != nil {
		t.Fatalf("newDeviceReaderWithClock: %v", err)
	}
	t.Cleanup(reader.Close)
	return reader
}

// pollAndCollect polls n times, draining after each poll so a send never blocks
// on the one-slot data channel.
func pollAndCollect(reader *deviceReader, n int) []NFCData {
	var got []NFCData
	for range n {
		reader.doPoll()
		for drained := false; !drained; {
			select {
			case data := <-reader.Data():
				got = append(got, data)
			default:
				drained = true
			}
		}
	}
	return got
}

// A tag holding no NDEF message is still a tag that was presented. It scans
// once, carrying its identity and no error, rather than once per poll as a
// failure.
func TestPoll_PayloadlessCardScansOnce(t *testing.T) {
	tag := NewMockTag("04A1B2C3")
	tag.TagType = CardTypeNtag215
	tag.IsConnected = true
	tag.ReadDataError = NewNoPayloadError("ReadData", "04A1B2C3", nil)

	got := pollAndCollect(pollingReader(t, tag), 20)

	if len(got) != 1 {
		t.Fatalf("got %d scans across 20 polls, want 1", len(got))
	}
	if got[0].Err != nil {
		t.Errorf("scan carries err = %v, want nil", got[0].Err)
	}
	if got[0].Card == nil {
		t.Fatal("scan carries no card")
	}
	if got[0].Card.UID != "04A1B2C3" {
		t.Errorf("card UID = %q, want %q", got[0].Card.UID, "04A1B2C3")
	}
}

// A tag whose read genuinely fails is reported, but once per card rather than at
// the polling rate.
func TestPoll_ReadFaultReportedOncePerCard(t *testing.T) {
	tag := NewMockTag("04A1B2C3")
	tag.TagType = CardTypeNtag215
	tag.IsConnected = true
	tag.ReadDataError = NewReadError("ReadData", errors.New("bad response"))

	got := pollAndCollect(pollingReader(t, tag), 20)

	if len(got) != 1 {
		t.Fatalf("got %d reports across 20 polls, want 1", len(got))
	}
	if got[0].Err == nil {
		t.Error("scan carries no error, want the read failure")
	}
}

// A card that faulted and then reads is reported again if it faults later, so
// the guard suppresses repetition rather than the second fault.
func TestPoll_ReadFaultReportedAgainAfterASuccessfulRead(t *testing.T) {
	tag := NewMockTag("04A1B2C3")
	tag.TagType = CardTypeNtag215
	tag.IsConnected = true
	tag.ReadDataError = NewReadError("ReadData", errors.New("bad response"))

	reader := pollingReader(t, tag)
	got := pollAndCollect(reader, 3)

	tag.ReadDataError = nil
	tag.Data = EncodeNdefMessageWithTextRecord("hello", "en")
	got = append(got, pollAndCollect(reader, 1)...)

	tag.ReadDataError = NewReadError("ReadData", errors.New("bad response"))
	got = append(got, pollAndCollect(reader, 3)...)

	if len(got) != 3 {
		t.Fatalf("got %d scans, want 3 (fault, read, fault)", len(got))
	}
	if got[0].Err == nil || got[1].Err != nil || got[2].Err == nil {
		t.Errorf("errors across the three scans = %v, %v, %v; want set, nil, set",
			got[0].Err, got[1].Err, got[2].Err)
	}
}

// The no-payload answer is cached, so the consumers that each ask a card for its
// message do not each go back to the tag to be told so again.
func TestCard_ReadMessageCachesNoPayload(t *testing.T) {
	tag := NewMockTag("04A1B2C3")
	tag.TagType = CardTypeNtag215
	tag.IsConnected = true
	tag.ReadDataError = NewNoPayloadError("ReadData", "04A1B2C3", nil)

	card := NewCard(tag)
	for range 3 {
		if _, err := card.ReadMessage(); !IsNoPayloadError(err) {
			t.Fatalf("ReadMessage() err = %v, want a no-payload error", err)
		}
	}

	if got := countCalls(tag, "ReadData"); got != 1 {
		t.Errorf("ReadData called %d times, want 1", got)
	}
}

// A tag that declares it carries no NDEF is not read at all.
func TestCard_SkipsTheReadWhenTheTagCarriesNoNDEF(t *testing.T) {
	tag := NewMockTag("04A1B2C3")
	tag.IsConnected = true
	tag.MockCapabilities = &TagCapabilities{CanRead: true, CanLock: true, SupportsNDEF: false}

	card := NewCard(tag)
	if _, err := card.ReadMessage(); !IsNoPayloadError(err) {
		t.Fatalf("ReadMessage() err = %v, want a no-payload error", err)
	}
	if got := countCalls(tag, "ReadData"); got != 0 {
		t.Errorf("ReadData called %d times, want 0", got)
	}
}

// Returning nothing with no error is indistinguishable from a successful read of
// an empty tag, so a tag carrying no NDEF has to say so.
func TestAssertCapabilitiesConsistent_RejectsASilentEmptyRead(t *testing.T) {
	tag := NewMockTag("04A1B2C3")
	tag.IsConnected = true
	tag.MockCapabilities = &TagCapabilities{CanRead: true, CanLock: true, SupportsNDEF: false}

	if err := AssertCapabilitiesConsistent(tag); err == nil {
		t.Error("AssertCapabilitiesConsistent() = nil, want a drift error")
	}

	tag.ReadDataError = NewNoPayloadError("ReadData", "04A1B2C3", nil)
	if err := AssertCapabilitiesConsistent(tag); err != nil {
		t.Errorf("AssertCapabilitiesConsistent() = %v, want nil", err)
	}
}

func countCalls(tag *MockTag, method string) int {
	n := 0
	for _, call := range tag.GetCallLog() {
		if call == method {
			n++
		}
	}
	return n
}
