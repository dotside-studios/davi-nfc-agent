package nfc

import (
	"bytes"
	"errors"
	"testing"
)

// scriptExchange answers each command with the next scripted reply and records
// what was sent.
func scriptExchange(replies ...[]byte) (func([]byte) ([]byte, error), *[][]byte) {
	var sent [][]byte
	return func(cmd []byte) ([]byte, error) {
		sent = append(sent, append([]byte(nil), cmd...))
		if len(replies) == 0 {
			return nil, errors.New("script exhausted")
		}
		reply := replies[0]
		replies = replies[1:]
		return reply, nil
	}, &sent
}

func TestExchangeChained_FollowsMoreDataUntilTheCardStops(t *testing.T) {
	exchange, sent := scriptExchange(
		[]byte{0x61, 0x10},
		append(bytes.Repeat([]byte{0xAA}, 16), 0x61, 0x10),
		append(bytes.Repeat([]byte{0xBB}, 16), 0x90, 0x00),
	)

	reply, follow, err := ExchangeChained([]byte{0x00, 0xCA, 0x00, 0x00, 0x00}, exchange)
	if err != nil {
		t.Fatal(err)
	}

	want := append(append(bytes.Repeat([]byte{0xAA}, 16), bytes.Repeat([]byte{0xBB}, 16)...), 0x90, 0x00)
	if !bytes.Equal(reply, want) {
		t.Errorf("reply = % X, want % X", reply, want)
	}
	getResponse := []byte{0x00, 0xC0, 0x00, 0x00, 0x10}
	if len(follow) != 2 || !bytes.Equal(follow[0], getResponse) || !bytes.Equal(follow[1], getResponse) {
		t.Errorf("follow-ups = % X, want two % X", follow, getResponse)
	}
	if len(*sent) != 3 {
		t.Errorf("%d commands sent, want 3", len(*sent))
	}
}

func TestExchangeChained_RetriesWrongLeOnceWithTheCorrectedLe(t *testing.T) {
	for name, tc := range map[string]struct {
		cmd, retry []byte
	}{
		"case 1, no Le":    {[]byte{0x00, 0xCA, 0x00, 0x00}, []byte{0x00, 0xCA, 0x00, 0x00, 0x08}},
		"case 2, Le":       {[]byte{0x00, 0xCA, 0x00, 0x00, 0x00}, []byte{0x00, 0xCA, 0x00, 0x00, 0x08}},
		"case 3, data":     {[]byte{0x00, 0xCA, 0x00, 0x00, 0x02, 0xAA, 0xBB}, []byte{0x00, 0xCA, 0x00, 0x00, 0x02, 0xAA, 0xBB, 0x08}},
		"case 4, data, Le": {[]byte{0x00, 0xCA, 0x00, 0x00, 0x02, 0xAA, 0xBB, 0x00}, []byte{0x00, 0xCA, 0x00, 0x00, 0x02, 0xAA, 0xBB, 0x08}},
	} {
		t.Run(name, func(t *testing.T) {
			data := []byte{1, 2, 3, 4, 5, 6, 7, 8}
			exchange, sent := scriptExchange([]byte{0x6C, 0x08}, append(data, 0x90, 0x00))

			reply, follow, err := ExchangeChained(tc.cmd, exchange)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(reply, append(data, 0x90, 0x00)) {
				t.Errorf("reply = % X", reply)
			}
			if len(follow) != 1 || !bytes.Equal(follow[0], tc.retry) {
				t.Errorf("retry = % X, want % X", follow, tc.retry)
			}
			if !bytes.Equal((*sent)[0], tc.cmd) {
				t.Errorf("the original command was changed: % X", (*sent)[0])
			}
		})
	}
}

func TestExchangeChained_WrongLeIsRetriedOnlyOnce(t *testing.T) {
	exchange, sent := scriptExchange([]byte{0x6C, 0x08}, []byte{0x6C, 0x04})

	reply, _, err := ExchangeChained([]byte{0x00, 0xCA, 0x00, 0x00, 0x00}, exchange)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reply, []byte{0x6C, 0x04}) || len(*sent) != 2 {
		t.Errorf("reply = % X after %d commands, want 6C 04 after 2", reply, len(*sent))
	}
}

func TestExchangeChained_WrongLeThenMoreData(t *testing.T) {
	exchange, sent := scriptExchange([]byte{0x6C, 0x04}, []byte{0x61, 0x02}, []byte{0x01, 0x02, 0x90, 0x00})

	reply, follow, err := ExchangeChained([]byte{0x00, 0xCA, 0x00, 0x00, 0x00}, exchange)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reply, []byte{0x01, 0x02, 0x90, 0x00}) || len(follow) != 2 || len(*sent) != 3 {
		t.Errorf("reply = % X, follow-ups = % X", reply, follow)
	}
}

func TestExchangeChained_PassesEverythingElseThrough(t *testing.T) {
	for name, reply := range map[string][]byte{
		"success":      {0x01, 0x02, 0x90, 0x00},
		"error":        {0x6A, 0x82},
		"DESFire more": {0xAA, 0x91, 0xAF},
		"no status":    {0x01},
	} {
		t.Run(name, func(t *testing.T) {
			exchange, sent := scriptExchange(reply)
			got, follow, err := ExchangeChained([]byte{0x00, 0xCA, 0x00, 0x00, 0x00}, exchange)
			if err != nil || !bytes.Equal(got, reply) || len(follow) != 0 || len(*sent) != 1 {
				t.Errorf("got % X, follow-ups %d, sent %d, err %v", got, len(follow), len(*sent), err)
			}
		})
	}

	exchange, sent := scriptExchange([]byte{0x61, 0x10})
	got, _, _ := ExchangeChained([]byte{0x30, 0x00}, exchange)
	if !bytes.Equal(got, []byte{0x61, 0x10}) || len(*sent) != 1 {
		t.Errorf("a command shorter than an APDU header was chained: % X after %d sends", got, len(*sent))
	}
}

func TestExchangeChained_ErrorStatusAfterDataKeepsTheDataAndTheFinalStatus(t *testing.T) {
	exchange, _ := scriptExchange([]byte{0x61, 0x02}, []byte{0x01, 0x02, 0x61, 0x02}, []byte{0x6A, 0x83})

	got, _, err := ExchangeChained([]byte{0x00, 0xCA, 0x00, 0x00, 0x00}, exchange)
	if err != nil || !bytes.Equal(got, []byte{0x01, 0x02, 0x6A, 0x83}) {
		t.Errorf("got % X, %v", got, err)
	}
}

func TestExchangeChained_BoundsGetResponseRounds(t *testing.T) {
	sent := 0
	exchange := func([]byte) ([]byte, error) {
		sent++
		return []byte{0xEE, 0x61, 0x01}, nil
	}

	got, follow, err := ExchangeChained([]byte{0x00, 0xCA, 0x00, 0x00, 0x00}, exchange)
	if err != nil {
		t.Fatal(err)
	}
	if len(follow) != MaxGetResponseRounds || sent != MaxGetResponseRounds+1 {
		t.Errorf("%d follow-ups and %d sends, want %d and %d", len(follow), sent, MaxGetResponseRounds, MaxGetResponseRounds+1)
	}
	if n := len(got); n != MaxGetResponseRounds+1+2 || got[n-2] != 0x61 {
		t.Errorf("reply has %d bytes, want the data of every round and the last 61xx", n)
	}
}

func TestExchangeChained_TransportErrorEndsTheRun(t *testing.T) {
	boom := errors.New("card removed")
	exchange, _ := scriptExchange([]byte{0x61, 0x10})
	calls := 0
	_, follow, err := ExchangeChained([]byte{0x00, 0xCA, 0x00, 0x00, 0x00}, func(cmd []byte) ([]byte, error) {
		calls++
		if calls == 2 {
			return nil, boom
		}
		return exchange(cmd)
	})
	if !errors.Is(err, boom) || len(follow) != 1 {
		t.Errorf("err = %v, follow-ups = %d", err, len(follow))
	}
}

func TestGetResponseCLA(t *testing.T) {
	for _, tc := range []struct{ cla, want byte }{
		{0x00, 0x00},
		{0x03, 0x03},
		{0x0C, 0x00}, // secure messaging bits are not carried over
		{0x42, 0x42},
		{0x5F, 0x4F},
		{0x80, 0x80},
		{0xA0, 0xA0},
	} {
		if got := getResponseCLA(tc.cla); got != tc.want {
			t.Errorf("getResponseCLA(%02X) = %02X, want %02X", tc.cla, got, tc.want)
		}
	}
}

func TestExchangeChained_GetResponseKeepsTheChannelOfTheCommand(t *testing.T) {
	exchange, _ := scriptExchange([]byte{0x61, 0x01}, []byte{0xAA, 0x90, 0x00})
	_, follow, err := ExchangeChained([]byte{0x02, 0xCA, 0x00, 0x00, 0x00}, exchange)
	if err != nil || len(follow) != 1 || follow[0][0] != 0x02 {
		t.Errorf("follow-up = % X, %v", follow, err)
	}
}

func TestWithShortLe_RefusesWhatItCannotRewrite(t *testing.T) {
	for name, cmd := range map[string][]byte{
		"shorter than a header": {0x00, 0xCA, 0x00},
		"extended case 2":       {0x00, 0xCA, 0x00, 0x00, 0x00, 0x01, 0x00},
		"extended case 3":       {0x00, 0xCA, 0x00, 0x00, 0x00, 0x00, 0x02, 0xAA, 0xBB},
		"length does not fit":   {0x00, 0xCA, 0x00, 0x00, 0x05, 0xAA},
	} {
		if out, ok := withShortLe(cmd, 0x08); ok {
			t.Errorf("%s: rewrote to % X", name, out)
		}
	}
}

func TestBuildExtendedAPDU(t *testing.T) {
	le := func(n int) *int { return &n }
	data := bytes.Repeat([]byte{0x5A}, 300)
	header := []byte{0x00, 0xCA, 0x01, 0x02}
	join := func(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

	for name, tc := range map[string]struct {
		data []byte
		le   *int
		want []byte
	}{
		"case 1":             {nil, nil, header},
		"case 2E":            {nil, le(0x0102), join(header, []byte{0x00, 0x01, 0x02})},
		"case 2E, max Le":    {nil, le(MaxExtendedLe), join(header, []byte{0x00, 0x00, 0x00})},
		"case 3E":            {data, nil, join(header, []byte{0x00, 0x01, 0x2C}, data)},
		"case 4E":            {data, le(2), join(header, []byte{0x00, 0x01, 0x2C}, data, []byte{0x00, 0x02})},
		"case 4E, max Le":    {data, le(MaxExtendedLe), join(header, []byte{0x00, 0x01, 0x2C}, data, []byte{0x00, 0x00})},
		"short data, ext Le": {[]byte{0xAA}, le(512), join(header, []byte{0x00, 0x00, 0x01, 0xAA, 0x02, 0x00})},
	} {
		got := BuildExtendedAPDU(0x00, 0xCA, 0x01, 0x02, tc.data, tc.le)
		if !bytes.Equal(got, tc.want) {
			t.Errorf("%s: % X, want % X", name, got, tc.want)
		}
	}

	if BuildExtendedAPDU(0, 0, 0, 0, make([]byte, MaxExtendedLc+1), nil) != nil {
		t.Error("data beyond 65535 bytes built an APDU")
	}
	if BuildExtendedAPDU(0, 0, 0, 0, nil, le(MaxExtendedLe+1)) != nil {
		t.Error("an Le beyond 65536 built an APDU")
	}
}

func TestBuildAPDU_DataBeyondAShortLcBuildsTheExtendedForm(t *testing.T) {
	data := bytes.Repeat([]byte{0x11}, 256)
	zero := byte(0)

	want := bytes.Join([][]byte{{0x00, 0xD6, 0x00, 0x00, 0x00, 0x01, 0x00}, data, {0x00, 0x00}}, nil)
	if got := BuildAPDU(0x00, 0xD6, 0x00, 0x00, data, &zero); !bytes.Equal(got, want) {
		t.Errorf("% X, want % X", got, want)
	}

	if got := BuildAPDU(0x00, 0xD6, 0x00, 0x00, data[:255], nil); len(got) != 4+1+255 || got[4] != 0xFF {
		t.Errorf("255 bytes of data left the short form: %d bytes", len(got))
	}
	if BuildAPDU(0, 0, 0, 0, make([]byte, MaxExtendedLc+1), nil) != nil {
		t.Error("data beyond 65535 bytes built an APDU")
	}
}

// The ATRs below are built for the test from the ISO 7816-4 layout, not
// captured from a card: 3B, T0 with the count of historical bytes, then the
// historical bytes with a category indicator and the card capabilities object
// (compact TLV tag 7, length 3).
func TestATRExtendedLength(t *testing.T) {
	for name, tc := range map[string]struct {
		atr              []byte
		supported, known bool
	}{
		"b7 set, no status": {
			atr:       []byte{0x3B, 0x05, 0x80, 0x73, 0xC0, 0x21, 0x40},
			supported: true, known: true,
		},
		"b7 and the chaining bit set": {
			atr:       []byte{0x3B, 0x05, 0x80, 0x73, 0xC0, 0x21, 0xC0},
			supported: true, known: true,
		},
		"b7 clear": {
			atr:       []byte{0x3B, 0x05, 0x80, 0x73, 0xC0, 0x21, 0x80},
			supported: false, known: true,
		},
		"with a status indicator": {
			atr:       []byte{0x3B, 0x08, 0x00, 0x73, 0xC0, 0x21, 0x40, 0x00, 0x90, 0x00},
			supported: true, known: true,
		},
		"after another object": {
			atr:       []byte{0x3B, 0x07, 0x80, 0x31, 0xC0, 0x73, 0xC0, 0x21, 0x40},
			supported: true, known: true,
		},
		"no card capabilities": {
			atr: []byte{0x3B, 0x03, 0x80, 0x31, 0xC0},
		},
		"no historical bytes": {atr: []byte{0x3B, 0x00}},
		"not an ATR":          {atr: []byte{0x01}},
		"truncated object":    {atr: []byte{0x3B, 0x04, 0x80, 0x73, 0xC0, 0x21}},
	} {
		supported, known := ATRExtendedLength(tc.atr)
		if supported != tc.supported || known != tc.known {
			t.Errorf("%s: supported, known = %v, %v, want %v, %v", name, supported, known, tc.supported, tc.known)
		}
	}
}
