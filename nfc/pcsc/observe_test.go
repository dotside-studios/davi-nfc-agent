package pcsc

import (
	"bytes"
	"errors"
	"sync"
	"testing"
)

func TestObserverSeesEveryTransmitAndTheOpenedReader(t *testing.T) {
	var mu sync.Mutex
	var cmds [][]byte
	var connected []string
	var method string

	SetObserver(Observer{
		Connected: func(reader string, atr []byte, m string, _ bool) {
			mu.Lock()
			defer mu.Unlock()
			connected = append(connected, reader)
			method = m
		},
		Transmit: func(_ string, command, _ []byte, _ error) {
			mu.Lock()
			defer mu.Unlock()
			cmds = append(cmds, command)
		},
	})
	t.Cleanup(func() { SetObserver(Observer{}) })

	card := &scriptCard{fn: func([]byte) ([]byte, error) { return []byte{0x04, 0x01, 0x90, 0x00}, nil }}
	m := &Manager{ctx: &cardContext{reader: "ACS ACR122U PICC Interface 00 00", card: card}}
	dev, err := m.OpenDevice("ACS ACR122U PICC Interface 00 00")
	if err != nil {
		t.Fatalf("OpenDevice: %v", err)
	}
	defer func() { _ = dev.Close() }()

	if _, err := dev.Transceive([]byte{0xFF, 0xB0, 0x00, 0x04, 0x10}); err != nil {
		t.Fatalf("Transceive: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(connected) != 1 || method != "acr122" {
		t.Errorf("connected = %v, method %q, want one ACR122", connected, method)
	}
	if len(cmds) == 0 || !bytes.Equal(cmds[len(cmds)-1], []byte{0xFF, 0xB0, 0x00, 0x04, 0x10}) {
		t.Errorf("observed commands = % X, want the read last", cmds)
	}
	if len(cmds) != len(card.sent) {
		t.Errorf("observed %d commands, the card was sent %d", len(cmds), len(card.sent))
	}
}

func TestObserverIsOffUnlessSet(t *testing.T) {
	SetObserver(Observer{})
	card := &scriptCard{fn: func([]byte) ([]byte, error) { return nil, errors.New("unused") }}
	got, obs := observe(card, "reader")
	if obs != nil || got != scardCard(card) {
		t.Errorf("observe wrapped a card with no observer set: %T, %v", got, obs)
	}
}

func TestObserverSeesFailedTransmits(t *testing.T) {
	var seen error
	SetObserver(Observer{Transmit: func(_ string, _, _ []byte, err error) { seen = err }})
	t.Cleanup(func() { SetObserver(Observer{}) })

	card, _ := observe(&scriptCard{fn: func([]byte) ([]byte, error) { return nil, errRemovedCard }}, "reader")
	if _, err := card.Transmit([]byte{0x01}); !errors.Is(err, errRemovedCard) {
		t.Fatalf("Transmit err = %v", err)
	}
	if !errors.Is(seen, errRemovedCard) {
		t.Errorf("the observer saw %v, want the removal", seen)
	}
}
