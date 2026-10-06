package fixture

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"

	"github.com/dotside-studios/davi-nfc-agent/nfc/ev2"
	"github.com/dotside-studios/davi-nfc-agent/nfc/ntag424"
)

// ErrNoKeys reports an LRP fixture recorded without keys, which cannot be
// replayed: authenticating needs the key.
var ErrNoKeys = errors.New("fixture: recorded without keys, there is nothing to authenticate with")

// LRPReplayStats counts what a replay checked.
type LRPReplayStats struct {
	// Authentications is how many AuthenticateLRPFirst and NonFirst
	// exchanges were reproduced from the recorded random number and matched
	// the recorded bytes.
	Authentications int

	// Commands and Responses are secure messages whose MAC verified under the
	// session: the reader's commands, and the card's answers.
	Commands, Responses int

	// Unchecked counts commands the replay cannot follow and so dropped the
	// session over: a chained answer, an error status, or a command that
	// ended the session.
	Unchecked int

	// URLs is how many recorded SUN URLs verified.
	URLs int
}

// ReplayLRP replays the LRP steps of a fixture through ev2.LRPSession.
//
// Each authentication is reproduced from the key and the reader's random number,
// which the recording holds in the clear: the replay feeds the card's recorded
// answers to ev2.LRPAuthenticator, with the recorded RndA as its random source,
// and requires its commands to equal the recorded bytes and its session to
// accept the card's final answer. Every secure command after that must carry a
// MAC the session computes the same way, and every answer a MAC that verifies.
// The data inside a CommFull message is not decrypted: its MAC covers the
// ciphertext, so the MAC checks carry the proof.
//
// SUN URLs are then verified against the keys with ntag424.VerifyURLFresh.
func ReplayLRP(f *LRP) (LRPReplayStats, error) {
	var stats LRPReplayStats
	if len(f.Keys) == 0 {
		return stats, ErrNoKeys
	}
	keys, err := keyTable(f.Keys)
	if err != nil {
		return stats, err
	}

	r := &lrpReplayer{keys: keys, stats: &stats}
	for _, step := range f.Steps {
		if step.Suite != SuiteLRP {
			r.drop()
		} else {
			for i, w := range step.Wire {
				if err := r.wire(w); err != nil {
					return stats, fmt.Errorf("fixture: step %q, exchange %d: %w", step.Name, i, err)
				}
			}
		}
		if step.Error == "" && len(step.KeysAfter) > 0 {
			after, err := keyTable(step.KeysAfter)
			if err != nil {
				return stats, fmt.Errorf("fixture: step %q: %w", step.Name, err)
			}
			for n, k := range after {
				r.keys[n] = k
			}
		}
	}

	store := &ntag424.MemoryCounterStore{}
	for _, u := range f.URLs {
		k := ntag424.Keys{LRP: u.LRP, FileRead: r.keys[byte(u.FileReadKey)]}
		if u.MetaReadKey != nil {
			k.MetaRead = r.keys[byte(*u.MetaReadKey)]
		}
		tap, err := ntag424.VerifyURLFresh(u.URL, k, store)
		if err != nil {
			return stats, fmt.Errorf("fixture: URL %q: %w", u.Name, err)
		}
		if tap.ReadCounter != u.Counter {
			return stats, fmt.Errorf("fixture: URL %q: counter %d, recorded %d", u.Name, tap.ReadCounter, u.Counter)
		}
		stats.URLs++
	}
	return stats, nil
}

func keyTable(in map[string]string) (map[byte][]byte, error) {
	out := make(map[byte][]byte, len(in))
	for n, h := range in {
		no, err := strconv.ParseUint(n, 10, 8)
		if err != nil {
			return nil, fmt.Errorf("fixture: key number %q: %w", n, err)
		}
		key, err := decodeHex("key", h)
		if err != nil {
			return nil, err
		}
		out[byte(no)] = key
	}
	return out, nil
}

// Instruction bytes the replay follows.
const (
	insAuthFirst    = 0x71
	insAuthNonFirst = 0x77
	insAdditional   = 0xAF
	insChangeKey    = 0xC4

	lrpCapLen = 6

	insReadData        = 0xAD
	insWriteData       = 0x8D
	insGetFileSettings = 0xF5
	insGetKeyVersion   = 0x64
	insGetFileCounters = 0xF6
)

type lrpReplayer struct {
	keys  map[byte][]byte
	stats *LRPReplayStats

	sess      *ev2.LRPSession
	authKeyNo byte

	// auth is an exchange awaiting its second message, with the card's answer
	// to the first.
	auth      *ev2.LRPAuthenticator
	authKey   byte
	firstResp []byte
}

func (r *lrpReplayer) drop() {
	r.sess, r.auth, r.firstResp = nil, nil, nil
}

func (r *lrpReplayer) wire(w Wire) error {
	cmd, err := w.CommandBytes()
	if err != nil {
		return err
	}
	if w.Error != "" {
		r.drop()
		return nil
	}
	resp, err := w.ResponseBytes()
	if err != nil {
		return err
	}

	if r.auth != nil {
		return r.finishAuth(cmd, resp)
	}
	if len(cmd) < 5 || cmd[0] != 0x90 {
		return nil
	}

	switch cmd[1] {
	case insAuthFirst, insAuthNonFirst:
		return r.startAuth(cmd, resp)
	default:
		return r.inSession(cmd, resp)
	}
}

func (r *lrpReplayer) startAuth(cmd, resp []byte) error {
	if len(cmd) < 7 {
		return fmt.Errorf("authentication command is %d bytes", len(cmd))
	}
	if cmd[6] != lrpCapLen {
		// An AES-style first message, which is how the driver learns a card is
		// in LRP mode: the card answers it in LRP mode, and nothing follows.
		r.drop()
		return nil
	}
	keyNo := cmd[5]
	key, ok := r.keys[keyNo]
	if !ok {
		return fmt.Errorf("authenticates with key %d, which the fixture holds no key for", keyNo)
	}

	mode, ti, counter := ev2.AuthFirst, []byte(nil), uint16(0)
	if cmd[1] == insAuthNonFirst {
		if r.sess == nil {
			return errors.New("AuthenticateLRPNonFirst with no session to continue")
		}
		mode, ti, counter = ev2.AuthNonFirst, r.sess.TI(), r.sess.Counter()
	}
	a, err := ntag424.NewLRPAuthenticator(mode, keyNo, key, ti)
	if err != nil {
		return err
	}
	a.SetCounter(counter)
	if !bytes.Equal(a.Command(), cmd) {
		return fmt.Errorf("authentication command % X, the driver builds % X", cmd, a.Command())
	}

	if !isMoreData(resp) {
		r.drop()
		return nil
	}
	r.drop()
	r.auth, r.authKey, r.firstResp = a, keyNo, resp
	return nil
}

func (r *lrpReplayer) finishAuth(cmd, resp []byte) error {
	a, first := r.auth, r.firstResp
	keyNo := r.authKey
	r.drop()

	if len(cmd) < 5+16 || cmd[0] != 0x90 || cmd[1] != insAdditional {
		return fmt.Errorf("expected the second authentication message, got % X", cmd)
	}
	a.SetRandom(bytes.NewReader(cmd[5 : 5+16]))
	second, err := a.Challenge(first)
	if err != nil {
		return fmt.Errorf("authentication challenge: %w", err)
	}
	if !bytes.Equal(second, cmd) {
		return fmt.Errorf("second authentication message % X, the driver builds % X from the recorded RndA", cmd, second)
	}

	if len(resp) < 2 || resp[len(resp)-2] != 0x91 || resp[len(resp)-1] != 0x00 {
		// The card refused the reader's proof: nothing further to follow.
		return nil
	}
	sess, err := a.Finish(resp)
	if err != nil {
		return fmt.Errorf("the card's answer does not verify under the key: %w", err)
	}
	r.sess, r.authKeyNo = sess, keyNo
	r.stats.Authentications++
	return nil
}

func (r *lrpReplayer) inSession(cmd, resp []byte) error {
	if r.sess == nil {
		return nil
	}
	if !isOK(resp) {
		r.stats.Unchecked++
		r.drop()
		return nil
	}
	if cmd[1] == insChangeKey && len(cmd) > 5 && cmd[5] == r.authKeyNo {
		r.stats.Unchecked++
		r.drop()
		return nil
	}

	mode := ev2.CommMAC
	if _, _, err := r.sess.VerifyCommand(cmd, ev2.CommMAC, 0); err == nil {
		r.stats.Commands++
	} else if looksPlain(cmd) {
		mode = ev2.CommPlain
		if _, _, err := r.sess.VerifyCommand(cmd, ev2.CommPlain, 0); err != nil {
			return err
		}
	} else {
		return fmt.Errorf("command %02X: %w", cmd[1], err)
	}
	if _, err := r.sess.Response(resp, mode); err != nil {
		return fmt.Errorf("answer to command %02X: %w", cmd[1], err)
	}
	if mode != ev2.CommPlain {
		r.stats.Responses++
	}
	return nil
}

func isOK(resp []byte) bool {
	return len(resp) >= 2 && resp[len(resp)-2] == 0x91 && resp[len(resp)-1] == 0x00
}

func isMoreData(resp []byte) bool {
	return len(resp) >= 2 && resp[len(resp)-2] == 0x91 && resp[len(resp)-1] == 0xAF
}

// looksPlain reports a command whose length says it carries no MAC: the data
// commands and reads whose fixed layout leaves no room for one. A command in
// CommMode.Plain is counted by the card but not authenticated, so its MAC
// cannot be checked, and only a layout that is plain by construction is
// accepted as such. Anything else that fails its MAC is an error.
func looksPlain(cmd []byte) bool {
	lc := int(cmd[4])
	if len(cmd) < 5+lc {
		return false
	}
	payload := cmd[5 : 5+lc]
	switch cmd[1] {
	case insReadData:
		return len(payload) == 7
	case insWriteData:
		return len(payload) >= 7 && len(payload) == 7+(int(payload[4])|int(payload[5])<<8|int(payload[6])<<16)
	case insGetFileSettings, insGetKeyVersion, insGetFileCounters:
		return len(payload) == 1
	}
	return false
}
