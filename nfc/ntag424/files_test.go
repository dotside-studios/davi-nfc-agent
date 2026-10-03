package ntag424

import (
	"bytes"
	"errors"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc/ev2"
)

// sessionPair returns the reader's and the card's halves of one session.
func sessionPair(t *testing.T) (reader, card *Session) {
	t.Helper()
	ti := mustHex(t, "7A21085E")
	enc := mustHex(t, "1309C877509E5A215007FF0ED19CA564")
	mac := mustHex(t, "4C6626F5E72EA694202139295C7A7FC7")
	reader, err := ev2.NewSession(ti, enc, mac)
	if err != nil {
		t.Fatal(err)
	}
	card, err = ev2.NewSession(ti, enc, mac)
	if err != nil {
		t.Fatal(err)
	}
	return reader, card
}

var allModes = map[string]CommMode{"plain": CommPlain, "mac": CommMAC, "full": CommFull}

// roundTrip sends cmd to the card half, checks its header and data, and
// returns the card's answer carrying reply.
func roundTrip(t *testing.T, card *Session, cmd []byte, mode CommMode, headerLen int, wantHeader, wantData, reply []byte) []byte {
	t.Helper()
	header, data, err := card.VerifyCommand(cmd, mode, headerLen)
	if err != nil {
		t.Fatalf("VerifyCommand: %v", err)
	}
	if !bytes.Equal(header, wantHeader) {
		t.Errorf("header = %X, want %X", header, wantHeader)
	}
	if !bytes.Equal(data, wantData) {
		t.Errorf("data = %X, want %X", data, wantData)
	}
	resp, err := card.Answer(0x00, reply, mode)
	if err != nil {
		t.Fatalf("Answer: %v", err)
	}
	return resp
}

func TestReadDataRoundTrip(t *testing.T) {
	for name, mode := range allModes {
		t.Run(name, func(t *testing.T) {
			reader, card := sessionPair(t)
			cmd, err := ReadData(reader, 0x02, 0x000102, 0x000010, mode)
			if err != nil {
				t.Fatal(err)
			}
			if cmd[1] != 0xAD {
				t.Errorf("ins = %#x", cmd[1])
			}
			reply := bytes.Repeat([]byte{0x5A}, 16)
			resp := roundTrip(t, card, cmd, mode, 7, mustHex(t, "02020100100000"), nil, reply)
			got, err := ParseReadData(reader, resp, mode)
			if err != nil || !bytes.Equal(got, reply) {
				t.Errorf("ParseReadData = %X, %v", got, err)
			}
		})
	}
}

func TestReadDataPlainAPDU(t *testing.T) {
	got, err := ReadDataPlain(0x02, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := mustHex(t, "90AD00000702000000000000"+"00"); !bytes.Equal(got, want) {
		t.Errorf("APDU = %X, want %X", got, want)
	}
	data, err := ParseReadData(nil, mustHex(t, "0102039100"), CommPlain)
	if err != nil || !bytes.Equal(data, []byte{1, 2, 3}) {
		t.Errorf("ParseReadData = %X, %v", data, err)
	}
	if _, err := ParseReadData(nil, mustHex(t, "919D"), CommPlain); !errors.Is(err, ErrPermissionDenied) {
		t.Errorf("919D error = %v", err)
	}
	if _, err := ReadDataPlain(2, 1<<24, 0); err == nil {
		t.Error("offset over three bytes accepted")
	}
}

func TestWriteDataRoundTrip(t *testing.T) {
	payload := []byte("hello, tag")
	for name, mode := range allModes {
		t.Run(name, func(t *testing.T) {
			reader, card := sessionPair(t)
			cmd, err := WriteData(reader, 0x02, 4, payload, mode)
			if err != nil {
				t.Fatal(err)
			}
			if cmd[1] != 0x8D {
				t.Errorf("ins = %#x", cmd[1])
			}
			header := mustHex(t, "02040000"+"0A0000")
			resp := roundTrip(t, card, cmd, mode, 7, header, payload, nil)
			if err := ParseWriteData(reader, resp, mode); err != nil {
				t.Errorf("ParseWriteData: %v", err)
			}
		})
	}
}

func TestWriteDataPlainAPDU(t *testing.T) {
	got, err := WriteDataPlain(0x02, 0, []byte{0xAA, 0xBB})
	if err != nil {
		t.Fatal(err)
	}
	if want := mustHex(t, "908D00000902000000020000AABB00"); !bytes.Equal(got, want) {
		t.Errorf("APDU = %X, want %X", got, want)
	}
}

func TestWriteDataChunkLimits(t *testing.T) {
	reader, _ := sessionPair(t)
	for name, mode := range allModes {
		max := MaxWriteChunk(mode)
		cmd, err := WriteData(reader, 2, 0, make([]byte, max), mode)
		if err != nil {
			t.Fatalf("%s: %d bytes refused: %v", name, max, err)
		}
		if lc := int(cmd[4]); lc != len(cmd)-6 || lc > 255 {
			t.Errorf("%s: Lc = %d for a %d-byte APDU", name, lc, len(cmd))
		}
		if _, err := WriteData(reader, 2, 0, make([]byte, max+1), mode); err == nil {
			t.Errorf("%s: %d bytes accepted", name, max+1)
		}
	}
	if _, err := WriteDataPlain(2, 0, make([]byte, MaxWriteChunk(CommPlain)+1)); err == nil {
		t.Error("oversized plain write accepted")
	}
}

func TestFileCountersRoundTrip(t *testing.T) {
	reader, card := sessionPair(t)
	cmd, err := GetFileCounters(reader, NDEFFileNo)
	if err != nil {
		t.Fatal(err)
	}
	if cmd[1] != 0xF6 {
		t.Errorf("ins = %#x", cmd[1])
	}
	resp := roundTrip(t, card, cmd, CommFull, 1, []byte{0x02}, nil, []byte{0x2A, 0x01, 0x00})
	got, err := ParseFileCounters(reader, resp)
	if err != nil || got != 0x12A {
		t.Errorf("counter = %d, %v, want 298", got, err)
	}
}

func TestKeyVersionRoundTrip(t *testing.T) {
	if got, want := GetKeyVersionPlain(3), mustHex(t, "906400000103"+"00"); !bytes.Equal(got, want) {
		t.Errorf("plain APDU = %X, want %X", got, want)
	}
	if v, err := ParseKeyVersion(nil, mustHex(t, "019100")); err != nil || v != 1 {
		t.Errorf("plain parse = %d, %v", v, err)
	}

	reader, card := sessionPair(t)
	cmd, err := GetKeyVersion(reader, 3)
	if err != nil {
		t.Fatal(err)
	}
	resp := roundTrip(t, card, cmd, CommMAC, 1, []byte{3}, nil, []byte{0x07})
	if v, err := ParseKeyVersion(reader, resp); err != nil || v != 7 {
		t.Errorf("version = %d, %v", v, err)
	}
}

func TestSetConfigurationRoundTrips(t *testing.T) {
	tests := []struct {
		name   string
		build  func(*Session) ([]byte, error)
		option byte
		data   []byte
	}{
		{"random ID on", func(s *Session) ([]byte, error) { return SetRandomID(s, true) }, 0x00, []byte{0x02}},
		{"random ID off", func(s *Session) ([]byte, error) { return SetRandomID(s, false) }, 0x00, []byte{0x00}},
		{"failed auth counter", func(s *Session) ([]byte, error) { return SetFailedAuthCounter(s, true, 1000, 10) }, 0x0A, mustHex(t, "01E8030A00")},
		{"generic", func(s *Session) ([]byte, error) { return SetConfiguration(s, 0x04, []byte{1, 2}) }, 0x04, []byte{1, 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader, card := sessionPair(t)
			cmd, err := tt.build(reader)
			if err != nil {
				t.Fatal(err)
			}
			if cmd[1] != 0x5C {
				t.Errorf("ins = %#x", cmd[1])
			}
			resp := roundTrip(t, card, cmd, CommFull, 1, []byte{tt.option}, tt.data, nil)
			if err := ParseSetConfiguration(reader, resp); err != nil {
				t.Errorf("ParseSetConfiguration: %v", err)
			}
		})
	}
}

func TestReadSigRoundTrip(t *testing.T) {
	reader, card := sessionPair(t)
	cmd, err := ReadSig(reader)
	if err != nil {
		t.Fatal(err)
	}
	if cmd[1] != 0x3C {
		t.Errorf("ins = %#x", cmd[1])
	}
	sig := bytes.Repeat([]byte{0xC3}, SigSize)
	resp := roundTrip(t, card, cmd, CommFull, 1, []byte{0x00}, nil, sig)
	got, err := ParseReadSig(reader, resp)
	if err != nil || !bytes.Equal(got, sig) {
		t.Errorf("signature = %X, %v", got, err)
	}

	reader, card = sessionPair(t)
	cmd, _ = ReadSig(reader)
	resp = roundTrip(t, card, cmd, CommFull, 1, []byte{0x00}, nil, sig[:10])
	if _, err := ParseReadSig(reader, resp); err == nil {
		t.Error("short signature accepted")
	}
}

func TestTTStatusRoundTrip(t *testing.T) {
	reader, card := sessionPair(t)
	cmd, err := GetTTStatus(reader)
	if err != nil {
		t.Fatal(err)
	}
	if cmd[1] != 0xF7 {
		t.Errorf("ins = %#x", cmd[1])
	}
	resp := roundTrip(t, card, cmd, CommFull, 0, nil, nil, []byte{'C', 'O'})
	got, err := ParseTTStatus(reader, resp)
	if err != nil || got.Permanent != 'C' || got.Current != 'O' {
		t.Errorf("status = %+v, %v", got, err)
	}
}

func TestSessionBuildersNeedASession(t *testing.T) {
	for name, err := range map[string]error{
		"ReadData":        second(ReadData(nil, 2, 0, 0, CommFull)),
		"WriteData":       second(WriteData(nil, 2, 0, nil, CommFull)),
		"GetFileCounters": second(GetFileCounters(nil, 2)),
		"GetKeyVersion":   second(GetKeyVersion(nil, 0)),
		"SetConfig":       second(SetConfiguration(nil, 0, nil)),
		"ReadSig":         second(ReadSig(nil)),
		"GetTTStatus":     second(GetTTStatus(nil)),
	} {
		if err == nil {
			t.Errorf("%s accepted a nil session", name)
		}
	}
}

func second(_ []byte, err error) error { return err }

// AN12196 Table 18's settings block, read back, then the same block with the
// type and size the card puts in front of it.
func TestParseFileSettingsAN12196Table18(t *testing.T) {
	block := mustHex(t, "4000E0C1F121200000430000430000")
	want := FileSettings{
		SDMEnabled:        true,
		CommMode:          CommPlain,
		Read:              AccessFree,
		MirrorUID:         true,
		MirrorReadCounter: true,
		ASCIIEncoding:     true,
		SDMCounterRet:     0x01,
		SDMMetaRead:       0x02,
		SDMFileRead:       0x01,
		PICCDataOffset:    0x20,
		MACInputOffset:    0x43,
		MACOffset:         0x43,
	}

	got, err := ParseEncodedFileSettings(block)
	if err != nil {
		t.Fatalf("ParseEncodedFileSettings: %v", err)
	}
	if *got != want {
		t.Errorf("settings = %+v, want %+v", *got, want)
	}

	card := append(mustHex(t, "00"), block[:3]...)
	card = append(card, mustHex(t, "000100")...)
	card = append(card, block[3:]...)
	got, err = ParseFileSettings(card)
	if err != nil {
		t.Fatalf("ParseFileSettings: %v", err)
	}
	want.FileSize = 256
	if *got != want {
		t.Errorf("settings = %+v, want %+v", *got, want)
	}
}

func TestFileSettingsEncodeParseRoundTrip(t *testing.T) {
	tests := map[string]FileSettings{
		"sdm off": {CommMode: CommFull, ReadWrite: 1, Change: 0, Read: 2, Write: AccessNever},
		"plain mirrors": {
			SDMEnabled: true, CommMode: CommMAC, Read: AccessFree,
			MirrorUID: true, MirrorReadCounter: true, ASCIIEncoding: true,
			SDMMetaRead: AccessFree, SDMFileRead: 3, SDMCounterRet: AccessNever,
			UIDOffset: 0x20, ReadCounterOffset: 0x30, MACInputOffset: 0x40, MACOffset: 0x40,
		},
		"everything": {
			SDMEnabled: true, Read: AccessFree, Write: 1, ReadWrite: 2, Change: 3,
			MirrorUID: true, MirrorReadCounter: true, ReadCounterLimit: true,
			EncryptFileData: true, ASCIIEncoding: true,
			SDMMetaRead: 2, SDMFileRead: 1, SDMCounterRet: 1,
			PICCDataOffset: 0x20, MACInputOffset: 0x50, ENCOffset: 0x50, ENCLength: 32,
			MACOffset: 0x80, ReadCounterLimitValue: 0x123456,
		},
		"mac off": {
			SDMEnabled: true, CommMode: CommPlain, Read: AccessFree,
			MirrorUID: true, ASCIIEncoding: true,
			SDMMetaRead: AccessFree, SDMFileRead: AccessNever, SDMCounterRet: AccessNever,
			UIDOffset: 0x20,
		},
	}
	for name, f := range tests {
		t.Run(name, func(t *testing.T) {
			b, err := f.Encode()
			if err != nil {
				t.Fatal(err)
			}
			got, err := ParseEncodedFileSettings(b)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if *got != f {
				t.Errorf("round trip = %+v, want %+v", *got, f)
			}
		})
	}
}

func TestParseFileSettingsRejectsMalformed(t *testing.T) {
	good := mustHex(t, "4000E0C1F121200000430000430000")
	for name, b := range map[string][]byte{
		"empty":           nil,
		"truncated":       good[:10],
		"trailing":        append(bytes.Clone(good), 0x00),
		"bad comm mode":   mustHex(t, "0200E0"),
		"short SDM block": mustHex(t, "4000E0C1"),
	} {
		if _, err := ParseEncodedFileSettings(b); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	if _, err := ParseFileSettings(mustHex(t, "0000E0")); err == nil {
		t.Error("response without a size accepted")
	}
}

func TestFileSettingsResponseRoundTrip(t *testing.T) {
	plain := mustHex(t, "000000EE000100"+"9100")
	f, err := ParseFileSettingsResponse(nil, plain)
	if err != nil {
		t.Fatal(err)
	}
	if f.Read != AccessFree || f.Write != AccessFree || f.FileSize != 256 || f.FileType != 0 {
		t.Errorf("settings = %+v", f)
	}
	if _, err := ParseFileSettingsResponse(nil, mustHex(t, "919D")); !errors.Is(err, ErrPermissionDenied) {
		t.Errorf("919D error = %v", err)
	}
	if got, want := GetFileSettingsPlain(2), mustHex(t, "90F5000001"+"02"+"00"); !bytes.Equal(got, want) {
		t.Errorf("plain APDU = %X, want %X", got, want)
	}

	reader, card := sessionPair(t)
	cmd, err := GetFileSettings(reader, 2)
	if err != nil {
		t.Fatal(err)
	}
	block := mustHex(t, "000000EE000100")
	resp := roundTrip(t, card, cmd, CommMAC, 1, []byte{2}, nil, block)
	if f, err := ParseFileSettingsResponse(reader, resp); err != nil || f.FileSize != 256 {
		t.Errorf("session parse = %+v, %v", f, err)
	}
}
