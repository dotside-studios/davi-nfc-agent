package agent

import (
	"context"
	"testing"
	"time"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

func awaitScan(t *testing.T, bridge *server.ServerBridge) nfc.NFCData {
	t.Helper()

	select {
	case data := <-bridge.TagData:
		return data
	case <-time.After(2 * time.Second):
		t.Fatal("nothing reached the bridge")
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
	bridge := server.NewServerBridge()
	t.Cleanup(bridge.Close)

	// Setup names every known type, which is the shipped default, so the card
	// has to be one of them to be admitted.
	tag := nfc.NewMockTag("04A1B2C3")
	tag.TagType = nfc.GetAllCardTypes()[0]
	card := nfc.NewCard(tag)

	t.Run("a type the filter names is admitted", func(t *testing.T) {
		a.forwardScan(nfc.NFCData{Card: card}, bridge)
		if got := awaitScan(t, bridge); got.Card == nil || got.Card.UID != "04A1B2C3" {
			t.Errorf("got %+v, want the scan", got)
		}
	})

	t.Run("a type it does not name is refused", func(t *testing.T) {
		a.DisallowCardType(tag.TagType)
		t.Cleanup(func() { a.AllowCardType(tag.TagType) })

		a.forwardScan(nfc.NFCData{Card: card}, bridge)

		// Refused scans still reach the bridge, as an error: a client that
		// asked for a write needs to hear why nothing happened.
		got := awaitScan(t, bridge)
		if got.Card != nil {
			t.Errorf("a filtered-out card reached the clients: %+v", got.Card)
		}
		if got.Err == nil {
			t.Error("a filtered-out scan must say why it was dropped")
		}
	})
}

// A read failure is reported rather than swallowed.
func TestForwardScanPassesErrorsThrough(t *testing.T) {
	a := newPumpAgent(t)
	bridge := server.NewServerBridge()
	t.Cleanup(bridge.Close)

	a.forwardScan(nfc.NFCData{Err: context.DeadlineExceeded}, bridge)
	if got := awaitScan(t, bridge); got.Err == nil {
		t.Error("the error did not reach the bridge")
	}
}

// PumpTagData is what joins a driver to the bridge now that no server does it.
func TestPumpTagDataForwardsUntilCancelled(t *testing.T) {
	bridge := server.NewServerBridge()
	t.Cleanup(bridge.Close)

	src := make(chan nfc.NFCData, 1)
	ctx, cancel := context.WithCancel(context.Background())
	go server.PumpTagData(ctx, src, bridge)

	src <- nfc.NFCData{Card: nfc.NewCard(nfc.NewMockTag("04FFFFFF"))}
	if got := awaitScan(t, bridge); got.Card == nil || got.Card.UID != "04FFFFFF" {
		t.Fatalf("got %+v, want the scan", got)
	}

	cancel()

	// After cancellation nothing is drained, so the buffered send stays put.
	src <- nfc.NFCData{Card: nfc.NewCard(nfc.NewMockTag("04EEEEEE"))}
	select {
	case data := <-bridge.TagData:
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
	a.OnTag(func(data nfc.NFCData) {
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

	a.Bridge.TagData <- nfc.NFCData{Card: nfc.NewCard(nfc.NewMockTag("04ABCDEF"))}

	select {
	case uid := <-seen:
		if uid != "04ABCDEF" {
			t.Errorf("got %q, want the scan", uid)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("the scan never reached the client server: its bridge listeners are not running")
	}
}
