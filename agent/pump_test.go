package agent

import (
	"context"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// recordingSink stands in for the client server: it receives what the pumps
// forward.
type recordingSink struct{ tags chan nfc.NFCData }

func newSink() *recordingSink { return &recordingSink{tags: make(chan nfc.NFCData, 8)} }

func (s *recordingSink) Broadcast(data nfc.NFCData) {
	select {
	case s.tags <- data:
	default:
	}
}

func (s *recordingSink) BroadcastDeviceStatus(nfc.DeviceStatus) {}

func awaitScan(t *testing.T, sink *recordingSink) nfc.NFCData {
	t.Helper()

	select {
	case data := <-sink.tags:
		return data
	case <-time.After(2 * time.Second):
		t.Fatal("nothing reached the sink")
		return nfc.NFCData{}
	}
}

func newPumpAgent(t *testing.T) *Agent {
	t.Helper()

	rt, err := Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	return rt.Agent
}

// The filter is the agent's, and it decides before the scan reaches the bridge.
func TestForwardScanAppliesTheCardTypeFilter(t *testing.T) {
	a := newPumpAgent(t)
	sink := newSink()

	// The shipped default is an empty filter, which admits everything, so a
	// type has to be named for there to be a filter at all.
	named := nfc.GetAllCardTypes()[0]
	a.AllowCardType(named)

	tag := nfc.NewMockTag("04A1B2C3")
	tag.TagType = named
	card := nfc.NewCard(tag)

	t.Run("a type the filter names is admitted", func(t *testing.T) {
		a.forwardScan(nfc.NFCData{Card: card}, sink)
		if got := awaitScan(t, sink); got.Card == nil || got.Card.UID != "04A1B2C3" {
			t.Errorf("got %+v, want the scan", got)
		}
	})

	t.Run("a type it does not name is refused", func(t *testing.T) {
		other := nfc.NewMockTag("04A1B2C3")
		other.TagType = "MIFARE Plus"

		a.forwardScan(nfc.NFCData{Card: nfc.NewCard(other)}, sink)

		// Refused scans still reach the bridge, as an error: a client that
		// asked for a write needs to hear why nothing happened.
		got := awaitScan(t, sink)
		if got.Card != nil {
			t.Errorf("a filtered-out card reached the clients: %+v", got.Card)
		}
		if got.Err == nil {
			t.Error("a filtered-out scan must say why it was dropped")
		}
	})

	t.Run("an empty filter admits a type the agent does not know", func(t *testing.T) {
		a.ClearCardTypeFilter()

		other := nfc.NewMockTag("04A1B2C3")
		other.TagType = "MIFARE Plus"

		a.forwardScan(nfc.NFCData{Card: nfc.NewCard(other)}, sink)
		if got := awaitScan(t, sink); got.Card == nil {
			t.Errorf("got %+v, want the scan admitted by an empty filter", got)
		}
	})
}

// A read failure is reported rather than swallowed.
func TestForwardScanPassesErrorsThrough(t *testing.T) {
	a := newPumpAgent(t)
	sink := newSink()

	a.forwardScan(nfc.NFCData{Err: context.DeadlineExceeded}, sink)
	if got := awaitScan(t, sink); got.Err == nil {
		t.Error("the error did not reach the bridge")
	}
}

// pumpDevices is what joins a driver to the client server.
func TestPumpDevicesForwardsUntilCancelled(t *testing.T) {
	sink := newSink()

	src := make(chan nfc.NFCData, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go pumpDevices(ctx, src, sink)

	src <- nfc.NFCData{Card: nfc.NewCard(nfc.NewMockTag("04FFFFFF"))}
	if got := awaitScan(t, sink); got.Card == nil || got.Card.UID != "04FFFFFF" {
		t.Fatalf("got %+v, want the scan", got)
	}

	cancel()

	// After cancellation nothing is drained, so the buffered send stays put.
	src <- nfc.NFCData{Card: nfc.NewCard(nfc.NewMockTag("04EEEEEE"))}
	select {
	case data := <-sink.tags:
		t.Errorf("the pump forwarded %+v after being cancelled", data)
	case <-time.After(300 * time.Millisecond):
	}
}

// A scan has to travel reader-or-device, bridge, client server, clients. The
// client server's leg is background work that something must start, and when
// the unified server stopped doing it as a side effect of binding, nothing did:
// clients connected, counts looked right, and every scan was dropped. OnTag is
// called on that leg, so it fires only when the leg is running.
func TestScanReachesTheClientServerAfterStart(t *testing.T) {
	rt, err := Setup(testOptions(t), nfc.NewMockManager())
	if err != nil {
		t.Fatalf("Setup: %v", err)
	}
	a := rt.Agent

	seen := make(chan string, 1)
	a.Events().Tag.Connect(func(data nfc.NFCData) {
		if data.Card != nil {
			select {
			case seen <- data.Card.UID:
			default:
			}
		}
	})

	if err := a.Start(""); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer a.Stop()

	a.ClientServer.Broadcast(nfc.NFCData{Card: nfc.NewCard(nfc.NewMockTag("04ABCDEF"))})

	select {
	case uid := <-seen:
		if uid != "04ABCDEF" {
			t.Errorf("got %q, want the scan", uid)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the scan never reached the client server: its bridge listeners are not running")
	}
}
