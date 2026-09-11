package nfc

import "fmt"

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

// chunkSizeFor is how much file data one command carries under a file's
// communication setting. Enciphered data is padded to whole blocks before it
// travels, so half as much of it fits.
func chunkSizeFor(comm byte) int {
	if comm == dfCommFull {
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
//
// Which exchange runs depends on the generation the card reported. One that
// reported none tries the newer exchange and falls back to the older, since a
// card that does not implement it refuses it without changing anything.
func (t *pcscDESFireTag) authenticate(keyNo byte) (desfireChannel, error) {
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

	var lastErr error
	for _, authenticate := range t.authenticators() {
		session, err := authenticate(keyNo, key)
		if err == nil {
			t.mu.Lock()
			t.session, t.sessionKeyNo = session, keyNo
			t.mu.Unlock()
			return session, nil
		}
		if IsCardRemovedError(err) {
			return nil, err
		}
		lastErr = err
	}
	return nil, NewAuthError("authenticate (DESFire)", t.uid, lastErr)
}

// sessionExchange sends one command in the session and returns its answer.
func (t *pcscDESFireTag) sessionExchange(session desfireChannel, ins byte, header, data []byte, comm byte) ([]byte, error) {
	cmd, err := session.command(ins, header, data, comm)
	if err != nil {
		return nil, err
	}
	raw, err := t.transmitRaw(cmd)
	if err != nil {
		return nil, err
	}
	return session.response(raw, comm)
}

// canChangeSettings reports whether the file's rights can be rewritten: the
// change right grants it to anyone, or names a key the agent holds.
func (t *pcscDESFireTag) canChangeSettings(ndef desfireFileSettings) bool {
	switch ndef.change {
	case dfAccessFree:
		return true
	case dfAccessNever:
		return false
	}
	_, held := t.keyFor(ndef.change)
	return held
}

// encodeAccessRights packs the four rights the way the card reads them: two
// bytes, least significant first, holding read-write and change, then read and
// write.
func encodeAccessRights(s desfireFileSettings) []byte {
	return []byte{
		s.readWrite<<4 | s.change,
		s.read<<4 | s.write,
	}
}

// changeFileSettings rewrites the NDEF file's communication setting and access
// rights. It travels inside a session under the change key, or plainly when the
// change right is open to anyone.
func (t *pcscDESFireTag) changeFileSettings(current, want desfireFileSettings) error {
	settings := append([]byte{want.comm}, encodeAccessRights(want)...)

	if current.change == dfAccessFree {
		_, status, err := t.dfTransceive(DESFireWrapAPDU(DFCmdChangeFileSettings,
			append([]byte{dfNDEFFileNo}, settings...)))
		if err != nil || status != dfStatusOK {
			return dfStatusErr("change file settings", status, err)
		}
		return nil
	}

	session, err := t.authenticate(current.change)
	if err != nil {
		return err
	}
	// The card requires this one enciphered whatever the file's own setting is:
	// the rights travel encrypted or not at all.
	if _, err := t.sessionExchange(session, DFCmdChangeFileSettings,
		[]byte{dfNDEFFileNo}, settings, dfCommFull); err != nil {
		return fmt.Errorf("change file settings: %w", err)
	}
	return nil
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
func (t *pcscDESFireTag) sessionReadFile(session desfireChannel, comm byte, fileNo byte, offset, length int) ([]byte, error) {
	out := make([]byte, 0, length)
	for read := 0; read < length; {
		want := min(chunkSizeFor(comm), length-read)
		data, err := t.sessionExchange(session, DFCmdReadData, fileArgs(fileNo, offset+read, want), nil, comm)
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
func (t *pcscDESFireTag) sessionWriteFile(session desfireChannel, comm byte, fileNo byte, offset int, data []byte) error {
	for written := 0; written < len(data); {
		end := min(written+chunkSizeFor(comm), len(data))
		chunk := data[written:end]
		_, err := t.sessionExchange(session, DFCmdWriteData, fileArgs(fileNo, offset+written, len(chunk)), chunk, comm)
		if err != nil {
			return fmt.Errorf("write file %d at %d: %w", fileNo, offset+written, err)
		}
		written = end
	}
	return nil
}
