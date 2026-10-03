package nfc

// MaxGetResponseRounds bounds the GET RESPONSE commands one chained exchange
// sends. A card that keeps answering 61xx past it is cut off with its last
// status word, so a misbehaving card cannot hold the reader for ever.
const MaxGetResponseRounds = 64

// INSGetResponse is the ISO 7816-4 GET RESPONSE instruction.
const INSGetResponse = 0xC0

// getResponseCLA derives the CLA of a GET RESPONSE from the command's own. An
// interindustry command keeps its channel bits and loses everything else: the
// first interindustry range (000x xxxx) keeps b2b1, the further range
// (01xx xxxx) keeps b8b7 and b4..b1. A proprietary class (b8 set) has no
// defined channel bits, so the command's CLA is reused whole, which is what
// cards that chain under their own class, such as GSM SIMs, expect.
func getResponseCLA(cla byte) byte {
	switch {
	case cla&0x80 != 0:
		return cla
	case cla&0x40 != 0:
		return cla & 0x4F
	default:
		return cla & 0x03
	}
}

// withShortLe returns cmd with its Le replaced by le, or with Le appended when
// the command has none. It reports false for a command it cannot rewrite: one
// shorter than a header, or in the extended form, where 6Cxx does not apply.
func withShortLe(cmd []byte, le byte) ([]byte, bool) {
	if len(cmd) < 4 {
		return nil, false
	}
	body := len(cmd) - 4
	switch body {
	case 0:
		return append(append([]byte(nil), cmd...), le), true
	case 1:
		out := append([]byte(nil), cmd...)
		out[4] = le
		return out, true
	}
	lc := int(cmd[4])
	if lc == 0 {
		return nil, false
	}
	switch body {
	case 1 + lc:
		return append(append([]byte(nil), cmd...), le), true
	case 2 + lc:
		out := append([]byte(nil), cmd...)
		out[len(out)-1] = le
		return out, true
	}
	return nil, false
}

// ExchangeChained sends cmd through exchange and follows what the card says
// about the reply, as ISO 7816-4 describes. A 6Cxx reply is answered once by
// re-sending the command with Le set to xx. A 61xx reply is answered with GET
// RESPONSE for xx bytes, repeated while the card keeps answering 61xx, up to
// MaxGetResponseRounds. The returned reply holds every data field received,
// concatenated, and the final status word. follow lists each command sent
// beyond cmd, in order.
//
// A command shorter than an APDU header is passed through unchanged. A card
// still answering 61xx after the round limit has its last status word returned
// as it is, so the caller can tell the reply is incomplete. An error from
// exchange ends the run with that error.
func ExchangeChained(cmd []byte, exchange func([]byte) ([]byte, error)) (reply []byte, follow [][]byte, err error) {
	reply, err = exchange(cmd)
	if err != nil || len(cmd) < 4 || len(reply) < 2 {
		return reply, nil, err
	}

	if reply[len(reply)-2] == SW1WrongLength {
		retry, ok := withShortLe(cmd, reply[len(reply)-1])
		if ok {
			follow = append(follow, retry)
			if reply, err = exchange(retry); err != nil {
				return nil, follow, err
			}
		}
	}

	var body []byte
	for rounds := 0; len(reply) >= 2 && reply[len(reply)-2] == SW1MoreData && rounds < MaxGetResponseRounds; rounds++ {
		body = append(body, reply[:len(reply)-2]...)
		next := []byte{getResponseCLA(cmd[0]), INSGetResponse, 0x00, 0x00, reply[len(reply)-1]}
		follow = append(follow, next)
		if reply, err = exchange(next); err != nil {
			return nil, follow, err
		}
	}
	if len(body) == 0 || len(reply) < 2 {
		return reply, follow, nil
	}
	return append(body, reply...), follow, nil
}
