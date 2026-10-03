package ntag424

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"
)

var (
	tapUID     = mustHexNoT("04958CAA5C5E80")
	tapMeta    = mustHexNoT("00112233445566778899AABBCCDDEEFF")
	tapFile    = mustHexNoT("FFEEDDCCBBAA99887766554433221100")
	tapPadding = []byte{1, 2, 3, 4, 5}
)

func mustHexNoT(s string) []byte {
	b, err := hex.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func upperHex(b []byte) []byte {
	return []byte(strings.ToUpper(hex.EncodeToString(b)))
}

// simulateTap fills a plan's mirrors the way the card does on a read, at the
// offsets the plan's settings name, and returns the NDEF file as read.
func simulateTap(t *testing.T, plan *SDMPlan, counter uint32, fileData []byte) []byte {
	t.Helper()
	s := plan.Settings
	ndef := bytes.Clone(plan.NDEF)
	put := func(off uint32, text []byte) {
		if int(off)+len(text) > len(ndef) {
			t.Fatalf("mirror at %d, %d chars, runs past the %d-byte message", off, len(text), len(ndef))
		}
		copy(ndef[off:], text)
	}

	picc := &PICCData{UID: tapUID, ReadCounter: counter, UIDMirrored: true, CounterMirrored: true}
	if s.SDMMetaRead == AccessFree {
		put(s.UIDOffset, upperHex(tapUID))
		put(s.ReadCounterOffset, []byte(fmt.Sprintf("%06X", counter)))
	} else {
		plain := make([]byte, PICCDataSize)
		plain[0] = piccTagUIDMirrored | piccTagCounterMirrored | uidLength
		copy(plain[1:], tapUID)
		copy(plain[8:], encodeCounter(counter))
		copy(plain[11:], tapPadding)
		block, _ := aes.NewCipher(tapMeta)
		enc := make([]byte, PICCDataSize)
		cipher.NewCBCEncrypter(block, make([]byte, 16)).CryptBlocks(enc, plain)
		put(s.PICCDataOffset, upperHex(enc))
	}

	if s.EncryptFileData {
		encKey, _, err := SessionKeys(tapFile, picc)
		if err != nil {
			t.Fatal(err)
		}
		block, _ := aes.NewCipher(encKey)
		iv := make([]byte, 16)
		copy(iv, encodeCounter(counter))
		block.Encrypt(iv, iv)
		enc := make([]byte, len(fileData))
		cipher.NewCBCEncrypter(block, iv).CryptBlocks(enc, fileData)
		put(s.ENCOffset, upperHex(enc))
	}

	mac, err := MAC(tapFile, picc, ndef[s.MACInputOffset:s.MACOffset])
	if err != nil {
		t.Fatal(err)
	}
	put(s.MACOffset, upperHex(mac))
	return ndef
}

// ndefURL extracts the URI from an NDEF file holding one URI record.
func ndefURL(t *testing.T, ndef []byte) string {
	t.Helper()
	if int(ndef[0])<<8|int(ndef[1]) != len(ndef)-2 {
		t.Fatalf("NLEN %d, message is %d bytes", int(ndef[0])<<8|int(ndef[1]), len(ndef)-2)
	}
	rec := ndef[2:]
	flags, typeLen := rec[0], int(rec[1])
	rec = rec[2:]
	if flags != 0xD1 {
		t.Fatalf("record flags = %#x, want a short single record", flags)
	}
	payloadLen, rec := int(rec[0]), rec[1:]
	if string(rec[:typeLen]) != "U" || len(rec)-typeLen != payloadLen {
		t.Fatalf("not a single URI record: type %q, payload %d of %d", rec[:typeLen], payloadLen, len(rec)-typeLen)
	}
	payload := rec[typeLen:]
	prefixes := map[byte]string{0: "", 1: "http://www.", 2: "https://www.", 3: "http://", 4: "https://"}
	return prefixes[payload[0]] + string(payload[1:])
}

func TestPlanSDMRoundTripsWithVerifyURL(t *testing.T) {
	fileData := []byte("0123456789ABCDEF")
	tests := []struct {
		name     string
		template string
		opts     SDMOptions
		wantData []byte
	}{
		{
			name:     "encrypted PICCData and MAC",
			template: "https://example.com/t?picc_data={picc}&cmac={mac}",
			opts:     SDMOptions{MetaRead: 2, FileRead: 1, CounterRet: 1, Read: AccessFree, Write: 0},
		},
		{
			name:     "encrypted PICCData, file data and MAC",
			template: "https://example.com/t?picc_data={picc}&enc={enc}&cmac={mac}",
			opts:     SDMOptions{MetaRead: 2, FileRead: 1, CounterRet: 1, Read: AccessFree},
			wantData: fileData,
		},
		{
			name:     "plain UID and counter",
			template: "http://www.example.com/tap?uid={uid}&ctr={ctr}&cmac={mac}",
			opts:     SDMOptions{FileRead: 1, CounterRet: AccessFree, Read: AccessFree},
		},
		{
			name:     "plain mirrors with file data and a long path",
			template: "https://example.com/some/long/path/segment?uid={uid}&ctr={ctr}&enc={enc}&cmac={mac}",
			opts:     SDMOptions{FileRead: 3, CounterRet: 3, Read: AccessFree},
			wantData: fileData,
		},
		{
			name:     "no prefix abbreviation",
			template: "ntag424://t?e={picc}&c={mac}",
			opts:     SDMOptions{MetaRead: 0, FileRead: 0, Read: AccessFree},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan, err := PlanSDM(tt.template, tt.opts)
			if err != nil {
				t.Fatalf("PlanSDM: %v", err)
			}
			encoded, err := plan.Settings.Encode()
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			back, err := ParseEncodedFileSettings(encoded)
			if err != nil || *back != plan.Settings {
				t.Fatalf("settings do not survive Encode: %+v, %v", back, err)
			}

			keys := Keys{MetaRead: tapMeta, FileRead: tapFile}
			for _, counter := range []uint32{1, 0x12A} {
				ndef := simulateTap(t, plan, counter, fileData)
				url := ndefURL(t, ndef)
				if strings.Contains(url, "{") || strings.Contains(url, "00000000000000000000") {
					t.Fatalf("mirrors not filled: %s", url)
				}
				tap, err := VerifyURL(url, keys)
				if err != nil {
					t.Fatalf("VerifyURL(%s): %v", url, err)
				}
				if !bytes.Equal(tap.UID, tapUID) || tap.ReadCounter != counter {
					t.Errorf("tap = %X / %d, want %X / %d", tap.UID, tap.ReadCounter, tapUID, counter)
				}
				if tt.wantData == nil && tap.FileData != nil {
					t.Errorf("unexpected file data %X", tap.FileData)
				}
				if tt.wantData != nil && !bytes.Equal(tap.FileData, tt.wantData) {
					t.Errorf("file data = %X, want %X", tap.FileData, tt.wantData)
				}
			}
		})
	}
}

func TestPlanSDMLayout(t *testing.T) {
	plan, err := PlanSDM("https://example.com/t?picc_data={picc}&cmac={mac}", SDMOptions{MetaRead: 2, FileRead: 1, Read: AccessFree})
	if err != nil {
		t.Fatal(err)
	}
	url := "example.com/t?picc_data=" + strings.Repeat("0", 32) + "&cmac=" + strings.Repeat("0", 16)
	want := append(mustHexNoT("D1010E"+"55"+"04"), url...)
	want[2] = byte(1 + len(url))
	want = append([]byte{0, byte(len(want))}, want...)
	if !bytes.Equal(plan.NDEF, want) {
		t.Fatalf("NDEF = %q, want %q", plan.NDEF, want)
	}

	s := plan.Settings
	if got := string(plan.NDEF[s.PICCDataOffset : s.PICCDataOffset+32]); got != strings.Repeat("0", 32) {
		t.Errorf("PICCDataOffset %d points at %q", s.PICCDataOffset, got)
	}
	if prefix := string(plan.NDEF[s.PICCDataOffset-12 : s.PICCDataOffset]); !strings.HasSuffix(prefix, "picc_data=") {
		t.Errorf("text before PICCData = %q", prefix)
	}
	if s.MACInputOffset != s.MACOffset {
		t.Errorf("without {enc} the MAC input should be empty, got %d..%d", s.MACInputOffset, s.MACOffset)
	}
	if s.MACOffset != uint32(len(plan.NDEF)-16) {
		t.Errorf("MACOffset = %d, want %d", s.MACOffset, len(plan.NDEF)-16)
	}
	if !s.SDMEnabled || !s.ASCIIEncoding || !s.MirrorUID || !s.MirrorReadCounter || s.CommMode != CommPlain {
		t.Errorf("settings = %+v", s)
	}
	if s.SDMMetaRead != 2 || s.SDMFileRead != 1 || s.Read != AccessFree {
		t.Errorf("keys not carried: %+v", s)
	}
}

func TestPlanSDMEncLayout(t *testing.T) {
	plan, err := PlanSDM("https://example.com/?e={picc}&enc={enc}&c={mac}", SDMOptions{MetaRead: 2, FileRead: 1, EncLength: 64})
	if err != nil {
		t.Fatal(err)
	}
	s := plan.Settings
	if !s.EncryptFileData || s.ENCLength != 64 || s.MACInputOffset != s.ENCOffset {
		t.Errorf("settings = %+v", s)
	}
	if s.MACOffset != s.ENCOffset+64+3 {
		t.Errorf("MACOffset = %d, want %d (ENC end plus \"&c=\")", s.MACOffset, s.ENCOffset+64+3)
	}
	if got := string(plan.NDEF[s.MACInputOffset:s.MACOffset]); got != strings.Repeat("0", 64)+"&c=" {
		t.Errorf("MAC input = %q", got)
	}
}

func TestPlanSDMValidation(t *testing.T) {
	ok := SDMOptions{MetaRead: 2, FileRead: 1}
	tests := map[string]struct {
		template string
		opts     SDMOptions
	}{
		"no MAC":                    {"https://x/?e={picc}", ok},
		"picc with uid":             {"https://x/?e={picc}&u={uid}&c={mac}", ok},
		"picc with ctr":             {"https://x/?e={picc}&n={ctr}&c={mac}", ok},
		"uid without ctr":           {"https://x/?uid={uid}&c={mac}", ok},
		"ctr without uid":           {"https://x/?ctr={ctr}&c={mac}", ok},
		"nothing mirrored":          {"https://x/?c={mac}", ok},
		"duplicate placeholder":     {"https://x/?e={picc}&f={picc}&c={mac}", ok},
		"unknown placeholder":       {"https://x/?e={picc}&z={nope}&c={mac}", ok},
		"enc before picc":           {"https://x/?enc={enc}&e={picc}&c={mac}", ok},
		"mac before picc":           {"https://x/?c={mac}&e={picc}", ok},
		"mac before enc":            {"https://x/?e={picc}&c={mac}&enc={enc}", ok},
		"file read not a key":       {"https://x/?e={picc}&c={mac}", SDMOptions{MetaRead: 2, FileRead: AccessFree}},
		"meta read not a key":       {"https://x/?e={picc}&c={mac}", SDMOptions{MetaRead: AccessFree, FileRead: 1}},
		"enc length not a block":    {"https://x/?e={picc}&enc={enc}&c={mac}", SDMOptions{MetaRead: 2, FileRead: 1, EncLength: 40}},
		"message too long":          {"https://x/" + strings.Repeat("a", 260) + "?e={picc}&c={mac}", ok},
		"access right not a nibble": {"https://x/?e={picc}&c={mac}", SDMOptions{MetaRead: 2, FileRead: 1, Read: 0x40}},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := PlanSDM(tt.template, tt.opts); err == nil {
				t.Error("accepted")
			}
		})
	}
}

// AN12196 Table 18 configures: PICCData at 0x20 (key 2), MAC at 0x43 with an
// empty MAC input, MAC key 1. The URL below is the one that layout implies, and
// planning it has to reproduce the table's settings block.
func TestPlanSDMAgainstAN12196Table18(t *testing.T) {
	plan, err := PlanSDM("https://choose.url.com/ntag424?e={picc}&c={mac}", SDMOptions{
		MetaRead: 2, FileRead: 1, CounterRet: 1,
		ReadWrite: 0, Change: 0, Read: AccessFree, Write: 0,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := plan.Settings.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if want := mustHex(t, "4000E0C1F121200000430000430000"); !bytes.Equal(got, want) {
		t.Errorf("settings = %X, want %X", got, want)
	}
	if ndefURL(t, plan.NDEF) != "https://choose.url.com/ntag424?e="+strings.Repeat("0", 32)+"&c="+strings.Repeat("0", 16) {
		t.Errorf("URL = %s", ndefURL(t, plan.NDEF))
	}
}
