package clientserver

import (
	"context"
	"encoding/hex"
	"strings"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/ntag424"
	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

// planSDM lays out what configureSDM would write, in read-only mode and with
// nothing on any reader, since it touches no tag.
func TestNTAG424PlanSDMNeedsNoTag(t *testing.T) {
	s := newTagOps(configInMode(t, nfc.ModeReadOnly))

	res, err := s.NTAG424(context.Background(), server.NTAG424Op{Request: protocol.NTAG424RequestPayload{
		Op:          "planSDM",
		URLTemplate: "https://davi.social/t?p={picc}&m={mac}",
	}})
	if err != nil {
		t.Fatalf("planSDM: %v", err)
	}
	if res.Op != "planSDM" || res.Plan == nil || res.Plan.Settings == nil {
		t.Fatalf("planSDM = %+v", res)
	}

	want, err := ntag424.PlanSDM("https://davi.social/t?p={picc}&m={mac}", ntag424.SDMOptions{
		MetaRead: 0, FileRead: 0, CounterRet: ntag424.AccessNever, Change: 0, Read: ntag424.AccessFree, Write: 0, ReadWrite: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := res.Plan.NDEFHex; got != strings.ToUpper(hex.EncodeToString(want.NDEF)) {
		t.Errorf("ndefHex = %s", got)
	}
	if res.Plan.Length != len(want.NDEF) {
		t.Errorf("length = %d, want %d", res.Plan.Length, len(want.NDEF))
	}
	if got := res.Plan.Settings; !got.SDMEnabled || got.PICCDataOffset != want.Settings.PICCDataOffset || got.MACOffset != want.Settings.MACOffset {
		t.Errorf("settings = %+v, want offsets %d and %d", got, want.Settings.PICCDataOffset, want.Settings.MACOffset)
	}
}

func TestNTAG424PlanSDMRefusesBadTemplates(t *testing.T) {
	s := newTagOps(Config{})

	badRight := 7
	for name, r := range map[string]protocol.NTAG424RequestPayload{
		"no template":   {Op: "planSDM"},
		"no mac":        {Op: "planSDM", URLTemplate: "https://x.test/t?p={picc}"},
		"bad key right": {Op: "planSDM", URLTemplate: "https://x.test/t?p={picc}&m={mac}", SDM: &protocol.NTAG424SDMOptions{MetaRead: &badRight}},
	} {
		_, err := s.NTAG424(context.Background(), server.NTAG424Op{Request: r})
		if got := codeOf(err); got != protocol.ErrCodeInvalidRequest {
			t.Errorf("%s: code = %q, want %q", name, got, protocol.ErrCodeInvalidRequest)
		}
	}
}
