package pcsc

import (
	"errors"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// scriptedCard answers each command from a table keyed by the command bytes,
// and records what it was asked. Anything not in the table is answered 6E00,
// as a card does for a command it does not implement.
type scriptedCard struct {
	answers map[string][]byte
	sent    [][]byte
}

func (c *scriptedCard) ActiveProtocol() protocol { return protocolT1 }

func (c *scriptedCard) Status() (*cardStatus, error) { return &cardStatus{}, nil }

func (c *scriptedCard) Transmit(cmd []byte) ([]byte, error) {
	c.sent = append(c.sent, append([]byte(nil), cmd...))
	if resp, ok := c.answers[string(cmd)]; ok {
		return resp, nil
	}
	return []byte{0x6E, 0x00}, nil
}

func (c *scriptedCard) Control(uint32, []byte) ([]byte, error) {
	return nil, errors.New("not supported")
}

func (c *scriptedCard) Disconnect(disposition) error { return nil }

// shortISO4ATR is the ATR a Type 4 card presents on a typical reader. It
// carries no card-type byte: DetectTagTypeFromATR reads it as unknown and
// isISO14443_4Compatible rejects it, which is why the version probe exists.
var shortISO4ATR = []byte{0x3B, 0x81, 0x80, 0x01, 0x80, 0x80}

func type4Device(card scardCard) *device {
	return &device{card: card, readerName: "ACS ACR122U PICC Interface 00 00", atr: shortISO4ATR, uid: "04A1B2C3D4E5F6"}
}

// ntag424Version is the first frame of an NTAG 424 DNA's wrapped GET_VERSION,
// answered 91 AF because two frames follow.
var ntag424Version = []byte{0x04, 0x04, 0x02, 0x30, 0x00, 0x11, 0x05, 0x91, 0xAF}

// A card that answers the wrapped GET_VERSION as an NTAG 424 DNA is driven as
// one. Without the probe this card is refused outright, since neither ATR check
// accepts it and detection ends in an unsupported-tag error.
func TestGetTagsNamesAnNTAG424(t *testing.T) {
	card := &scriptedCard{answers: map[string][]byte{
		string(nfc.NTAG424GetVersionAPDU()): ntag424Version,
	}}

	tags, err := type4Device(card).GetTags()
	if err != nil {
		t.Fatalf("GetTags: %v", err)
	}
	if len(tags) != 1 {
		t.Fatalf("got %d tags, want 1", len(tags))
	}
	if got := tags[0].Type(); got != nfc.CardTypeNtag424 {
		t.Errorf("Type() = %q, want %q", got, nfc.CardTypeNtag424)
	}
	if got := nfc.GetTagCapabilities(tags[0]).MaxNDEFSize; got != 254 {
		t.Errorf("MaxNDEFSize = %d, want 254", got)
	}
}

// A card that does not implement the command, or answers as something else, is
// left to the detection that follows rather than being called an NTAG 424.
func TestProbeWrappedVersionNamesOnlyWhatItKnows(t *testing.T) {
	cases := map[string][]byte{
		"class not supported": nil, // answered 6E00 by the scripted default
		"a DESFire version":   {0x04, 0x01, 0x01, 0x12, 0x00, 0x18, 0x05, 0x91, 0xAF},
		"an NTAG215 version":  {0x04, 0x04, 0x02, 0x01, 0x00, 0x11, 0x03, 0x91, 0xAF},
		"a truncated frame":   {0x04, 0x04, 0x91, 0xAF},
	}
	for name, answer := range cases {
		t.Run(name, func(t *testing.T) {
			card := &scriptedCard{answers: map[string][]byte{}}
			if answer != nil {
				card.answers[string(nfc.NTAG424GetVersionAPDU())] = answer
			}

			if got := type4Device(card).probeWrappedVersion(); got != nfc.DetectedUnknown {
				t.Errorf("probeWrappedVersion() = %v, want DetectedUnknown", got)
			}
		})
	}
}

// A kind the ATR already named is not probed, so the common cards pay nothing
// for a check only Type 4 cards need.
func TestRefineType4OnlyProbesType4Cards(t *testing.T) {
	card := &scriptedCard{answers: map[string][]byte{}}
	dev := type4Device(card)

	if got := dev.refineType4(nfc.DetectedNTAG215); got != nfc.DetectedNTAG215 {
		t.Errorf("refineType4(NTAG215) = %v, want NTAG215 unchanged", got)
	}
	if len(card.sent) != 0 {
		t.Errorf("sent %d commands to a card that was already named, want 0", len(card.sent))
	}

	if got := dev.refineType4(nfc.DetectedISO14443_4); got != nfc.DetectedISO14443_4 {
		t.Errorf("refineType4(ISO14443_4) = %v, want it unchanged when unrecognised", got)
	}
	if len(card.sent) != 1 {
		t.Errorf("sent %d commands to a Type 4 card, want exactly 1", len(card.sent))
	}

	card.answers[string(nfc.NTAG424GetVersionAPDU())] = ntag424Version
	if got := dev.refineType4(nfc.DetectedISO14443_4); got != nfc.DetectedNTAG424 {
		t.Errorf("refineType4(ISO14443_4) = %v, want DetectedNTAG424 once the card names itself", got)
	}
}
