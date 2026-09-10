package nfc

import (
	"fmt"

	"github.com/dotside-studios/davi-nfc-agent/nfc/ev2"
)

// Access to a file whose rights name a key, rather than granting the operation
// to anyone. The agent authenticates with the key it holds for that number and
// then drives the file inside the session, which MACs every command and counts
// it, so the card refuses one replayed or reordered.

// Communication settings, byte 1 of a file's settings. They fix how much
// protection the card requires on a command touching that file, and it refuses
// one carrying less.
const (
	dfCommPlain = 0x00
	dfCommMAC   = 0x01
	dfCommFull  = 0x03
)

// How much file data one session command carries. Each read and write is
// addressed by offset and sent on its own, rather than chained across
// additional frames, so every command is a whole MACed exchange the card counts
// once. That costs round trips and keeps the framing unambiguous.
//
// The budget is the native frame less the 7-byte header and the 8-byte MAC.
// Under CommFull the data is padded to whole AES blocks before it travels, so
// half as much plaintext fits.
const (
	dfSessionChunk     = 32
	dfSessionChunkFull = 16
)

// commMode maps the card's communication setting onto the session's.
func commMode(setting byte) (ev2.CommMode, error) {
	switch setting {
	case dfCommPlain:
		return ev2.CommPlain, nil
	case dfCommMAC:
		return ev2.CommMAC, nil
	case dfCommFull:
		return ev2.CommFull, nil
	default:
		return 0, fmt.Errorf("unknown communication setting %#02x", setting)
	}
}

// chunkSize is how much file data one command in this mode carries.
func chunkSize(mode ev2.CommMode) int {
	if mode == ev2.CommFull {
		return dfSessionChunkFull
	}
	return dfSessionChunk
}

// SetDESFireKeys gives the tag the AES keys to authenticate with, implementing
// desfireKeyConfigurable. Replacing them drops any session opened under the old
// ones.
func (t *pcscDESFireTag) SetDESFireKeys(keys DESFireKeys) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.keys = keys.Copy()
	t.session = nil
	t.sessionKeyNo = 0
}

// keyFor reports the key held for a number, if any.
func (t *pcscDESFireTag) keyFor(keyNo byte) ([]byte, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	key, ok := t.keys[keyNo]
	return key, ok
}

// authenticate opens a session with the numbered key, or returns the one
// already open under it. The NDEF application must be selected first: a session
// belongs to the application it was opened in.
func (t *pcscDESFireTag) authenticate(keyNo byte) (*ev2.Session, error) {
	t.mu.Lock()
	if t.session != nil && t.sessionKeyNo == keyNo {
		session := t.session
		t.mu.Unlock()
		return session, nil
	}
	t.mu.Unlock()

	key, ok := t.keyFor(keyNo)
	if !ok {
		return nil, NewAuthError("authenticate (DESFire)", t.uid,
			fmt.Errorf("no key held for key %d", keyNo))
	}

	auth, err := ev2.NewAuthenticator(ev2.AuthFirst, keyNo, key, nil)
	if err != nil {
		return nil, NewAuthError("authenticate (DESFire)", t.uid, err)
	}

	first, err := t.transmitRaw(auth.Command())
	if err != nil {
		return nil, err
	}
	second, err := auth.Challenge(first)
	if err != nil {
		return nil, NewAuthError("authenticate (DESFire)", t.uid, err)
	}
	answer, err := t.transmitRaw(second)
	if err != nil {
		return nil, err
	}
	session, err := auth.Finish(answer)
	if err != nil {
		return nil, NewAuthError("authenticate (DESFire)", t.uid, err)
	}

	t.mu.Lock()
	t.session, t.sessionKeyNo = session, keyNo
	t.mu.Unlock()
	return session, nil
}

// sessionExchange sends one command in the session and returns its answer.
func (t *pcscDESFireTag) sessionExchange(session *ev2.Session, ins byte, header, data []byte, mode ev2.CommMode) ([]byte, error) {
	cmd, err := session.Command(ins, header, data, mode)
	if err != nil {
		return nil, err
	}
	raw, err := t.transmitRaw(cmd)
	if err != nil {
		return nil, err
	}
	return session.Response(raw, mode)
}

// fileArgs is the header a read or a write opens with: the file, where in it,
// and how much. The two lengths travel least significant byte first.
func fileArgs(fileNo byte, offset, length int) []byte {
	return []byte{
		fileNo,
		byte(offset), byte(offset >> 8), byte(offset >> 16),
		byte(length), byte(length >> 8), byte(length >> 16),
	}
}

// sessionReadFile reads length bytes from a file inside the session, one
// command per chunk.
func (t *pcscDESFireTag) sessionReadFile(session *ev2.Session, mode ev2.CommMode, fileNo byte, offset, length int) ([]byte, error) {
	out := make([]byte, 0, length)
	for read := 0; read < length; {
		want := min(chunkSize(mode), length-read)
		data, err := t.sessionExchange(session, DFCmdReadData, fileArgs(fileNo, offset+read, want), nil, mode)
		if err != nil {
			return nil, fmt.Errorf("read file %d at %d: %w", fileNo, offset+read, err)
		}
		if len(data) < want {
			return nil, fmt.Errorf("read file %d at %d: card returned %d of %d bytes", fileNo, offset+read, len(data), want)
		}
		out = append(out, data[:want]...)
		read += want
	}
	return out, nil
}

// sessionWriteFile writes data to a file inside the session, one command per
// chunk.
func (t *pcscDESFireTag) sessionWriteFile(session *ev2.Session, mode ev2.CommMode, fileNo byte, offset int, data []byte) error {
	for written := 0; written < len(data); {
		end := min(written+chunkSize(mode), len(data))
		chunk := data[written:end]
		_, err := t.sessionExchange(session, DFCmdWriteData, fileArgs(fileNo, offset+written, len(chunk)), chunk, mode)
		if err != nil {
			return fmt.Errorf("write file %d at %d: %w", fileNo, offset+written, err)
		}
		written = end
	}
	return nil
}
