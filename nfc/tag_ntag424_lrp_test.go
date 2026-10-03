package nfc

import (
	"bytes"
	"errors"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc/ntag424"
)

type lrpTransport struct{ sent [][]byte }

func (l *lrpTransport) IsCardPresent() bool { return true }

func (l *lrpTransport) Transceive(cmd []byte) ([]byte, error) {
	l.sent = append(l.sent, bytes.Clone(cmd))
	if cmd[1] == 0xA4 {
		return []byte{0x90, 0x00}, nil
	}
	reply := append([]byte{0x01}, make([]byte, 16)...)
	return append(reply, 0x91, 0xAF), nil
}

func TestNTAG424RefusesLRP(t *testing.T) {
	tr := &lrpTransport{}
	tag := newPCSCNTAG424Tag(tr, "04A1B2C3D4E5F6")
	tag.SetNTAG424Keys(NTAG424Keys{Master: make([]byte, 16)})

	_, err := tag.ReadSig()
	if !errors.Is(err, ntag424.ErrLRP) || !IsAuthError(err) {
		t.Fatalf("ReadSig = %v, want an auth error wrapping ErrLRP", err)
	}
	sent := len(tr.sent)
	if _, err := tag.ReadSig(); !errors.Is(err, ntag424.ErrLRP) {
		t.Fatalf("second ReadSig = %v", err)
	}
	if len(tr.sent) != sent {
		t.Error("an LRP tag was probed again")
	}
}
