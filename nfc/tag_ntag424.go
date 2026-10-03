package nfc

import (
	"encoding/hex"
	"errors"
	"fmt"
	"sync"

	"github.com/dotside-studios/davi-nfc-agent/nfc/ev2"
	"github.com/dotside-studios/davi-nfc-agent/nfc/ntag424"
)

// pcscNTAG424Tag is an NTAG 424 DNA.
//
// Its NDEF file is an ordinary NFC Forum Type 4 file, so a file that anyone may
// read and write in plain is the Type 4 driver's. This type supplies the
// identity (the name, the 416-byte layout, the 254-byte NDEF ceiling) and the
// card's AES side: an EV2 session opened with the keys the agent holds, through
// which a file whose rights name a key, or whose communication mode is MAC or
// full, is read and written, and through which the card's settings, keys and
// SDM configuration are changed. See [NTAG424Operator].
//
// The session lives on the tag, and the tag lives for as long as the card is on
// the reader, so it survives the polls between operations. Anything that makes
// the card forget it (an ISO SELECT, a raw exchange from a client, a failed
// command) drops it here too.
type pcscNTAG424Tag struct {
	pcscISO14443Tag

	// Guarded by mu: a scan publishes the tag to whoever broadcasts it, and
	// Capabilities and UID are read there while an operation may be running.
	mu           sync.Mutex
	keys         NTAG424Keys
	session      *ev2.Session
	sessionKeyNo byte

	// realUID is the card's own UID, once learned. With the random UID on, uid
	// is the 4-byte value the card presented for this tap instead.
	realUID []byte

	// resolved and resolveErr record that learning the real UID was tried for
	// the keys now held, so a failure is not repeated on every poll.
	resolved   bool
	resolveErr error

	// failedAuth holds the key numbers the card refused, with the refusal, and
	// delayed that it answered 91 AD. Each failed authentication counts toward
	// the card's lockout, so neither is retried until the keys change.
	failedAuth map[byte]error
	delayed    bool
	lrp        bool

	// ndefSettings is the NDEF file's settings as last read. It informs
	// Capabilities only; every operation reads them afresh.
	ndefSettings *ntag424.FileSettings
}

func newPCSCNTAG424Tag(dev CardTransport, uid string) *pcscNTAG424Tag {
	return &pcscNTAG424Tag{
		pcscISO14443Tag: pcscISO14443Tag{
			pcscBaseTag: pcscBaseTag{
				device:       dev,
				uid:          uid,
				detectedType: DetectedNTAG424,
			},
		},
	}
}

// Native status bytes and file numbers the driver tests for.
const (
	n4FileNDEF = ntag424.NDEFFileNo

	n4StatusAuthRequired = 0xAE
)

// Frame budget of one native read or write. A read answer carries data, a MAC
// and the status; enciphered data is padded, so less of it fits.
const (
	n4ReadChunk     = 192
	n4ReadChunkFull = 160
)

var errNTAG424NoUndiversifiedKey = errors.New("a random UID with diversified keys needs an explicit key slot in NTAG424Keys.Slots to learn the card's UID")

// UID is the card's real UID once it has been learned, and otherwise the UID it
// presented.
func (t *pcscNTAG424Tag) UID() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.realUID != nil {
		return BytesToHex(t.realUID)
	}
	return t.uid
}

// UIDAliases are the other UIDs the card answers to for this presence: the one
// it presented, when that differs from the real one.
func (t *pcscNTAG424Tag) UIDAliases() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.realUID != nil && BytesToHex(t.realUID) != t.uid {
		return []string{t.uid}
	}
	return nil
}

// RandomID reports whether the card presented a random UID for this tap: four
// bytes opening 0x08.
func (t *pcscNTAG424Tag) RandomID() bool {
	raw, err := hex.DecodeString(t.uid)
	return err == nil && len(raw) == 4 && raw[0] == 0x08
}

// SetNTAG424Keys gives the tag the keys to authenticate with, implementing
// ntag424KeyConfigurable. Keys equal to those held change nothing, so an open
// session survives the reader applying them on every operation; different keys
// drop it.
func (t *pcscNTAG424Tag) SetNTAG424Keys(keys NTAG424Keys) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if ntag424KeysEqual(t.keys, keys) {
		return
	}
	t.keys = keys.Copy()
	t.session = nil
	t.failedAuth = nil
	t.delayed = false
	t.resolved, t.resolveErr = false, nil
}

func (t *pcscNTAG424Tag) heldKeys() NTAG424Keys {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.keys
}

// holds reports whether a key is held for a number, without asking the card
// anything.
func (t *pcscNTAG424Tag) holds(keyNo byte) bool {
	keys := t.heldKeys()
	if key, ok := keys.Slots[keyNo]; ok {
		return len(key) == ntag424.KeySize
	}
	return keyNo <= 4 && len(keys.Master) == ntag424.KeySize
}

// anyHeldKey is the lowest key number held, for a command that needs a session
// but no particular key.
func (t *pcscNTAG424Tag) anyHeldKey() (byte, bool) {
	for n := byte(0); n <= 4; n++ {
		if t.holds(n) {
			return n, true
		}
	}
	return 0, false
}

// sessionKey is the key to open a session with for a command that does not care
// which: the one already open, else the lowest held.
func (t *pcscNTAG424Tag) sessionKey() (byte, error) {
	t.mu.Lock()
	open, keyNo := t.session != nil, t.sessionKeyNo
	t.mu.Unlock()
	if open {
		return keyNo, nil
	}
	if n, ok := t.anyHeldKey(); ok {
		return n, nil
	}
	return 0, NewAuthError("NTAG 424 session", t.UID(), fmt.Errorf("no key held"))
}

// keyUID is the UID per-card keys derive from: the real one, or the presented
// one when the card is not random.
func (t *pcscNTAG424Tag) keyUID() []byte {
	t.mu.Lock()
	real := t.realUID
	t.mu.Unlock()
	if real != nil {
		return real
	}
	raw, _ := hex.DecodeString(t.uid)
	return raw
}

// keyFor returns the key for a number. A key derived from the UID of a card
// whose UID is random first needs the real one, which costs an authentication
// under a key that does not.
func (t *pcscNTAG424Tag) keyFor(keyNo byte) ([]byte, error) {
	keys := t.heldKeys()
	t.mu.Lock()
	known := t.realUID != nil
	t.mu.Unlock()

	if !known && t.RandomID() && keys.Diversify {
		if _, explicit := keys.Slots[keyNo]; !explicit {
			if err := t.resolveUID(); err != nil {
				return nil, err
			}
		}
	}
	key, ok := keys.Key(keyNo, t.keyUID())
	if !ok {
		return nil, fmt.Errorf("no key held for key %d", keyNo)
	}
	return key, nil
}

// resolverKey picks the key that can open the session GetCardUID needs on a
// card whose UID is random: one that does not derive from the UID.
func (t *pcscNTAG424Tag) resolverKey() (byte, error) {
	keys := t.heldKeys()
	best, found := byte(0), false
	for n, key := range keys.Slots {
		if len(key) == ntag424.KeySize && (!found || n < best) {
			best, found = n, true
		}
	}
	if found {
		return best, nil
	}
	if !keys.Diversify && len(keys.Master) == ntag424.KeySize {
		return 0, nil
	}
	return 0, errNTAG424NoUndiversifiedKey
}

// resolveUID learns the real UID of a card presenting a random one.
func (t *pcscNTAG424Tag) resolveUID() error {
	t.mu.Lock()
	if t.realUID != nil {
		t.mu.Unlock()
		return nil
	}
	if t.resolved {
		err := t.resolveErr
		t.mu.Unlock()
		return err
	}
	t.mu.Unlock()

	err := t.fetchRealUID()
	if err != nil && IsCardRemovedError(err) {
		return err
	}
	t.mu.Lock()
	t.resolved, t.resolveErr = true, err
	t.mu.Unlock()
	return err
}

func (t *pcscNTAG424Tag) fetchRealUID() error {
	keyNo, err := t.resolverKey()
	if err != nil {
		return NewAuthError("resolve UID (NTAG 424)", t.uid, err)
	}
	return t.withSession(keyNo, func(s *ev2.Session) error {
		cmd, err := ntag424.GetCardUID(s)
		if err != nil {
			return err
		}
		resp, err := t.transmitRaw(cmd)
		if err != nil {
			return err
		}
		uid, err := ntag424.ParseCardUID(s, resp)
		if err != nil {
			return err
		}
		t.mu.Lock()
		t.realUID = uid
		t.mu.Unlock()
		return nil
	})
}

// ResolveUID learns the card's real UID when it presents a random one and keys
// are held, so the reader can publish and route by it. It asks at most once per
// presence and set of keys, and a failure leaves the presented UID in place.
func (t *pcscNTAG424Tag) ResolveUID() {
	if !t.RandomID() || t.heldKeys().Empty() {
		return
	}
	_ = t.resolveUID()
}

// dropSession forgets the session. It must run before anything that makes the
// card forget it, and after anything that leaves it unsure.
func (t *pcscNTAG424Tag) dropSession() {
	t.mu.Lock()
	t.session = nil
	t.mu.Unlock()
}

func (t *pcscNTAG424Tag) currentSession() (*ev2.Session, byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.session, t.sessionKeyNo
}

// Transceive sends a client's raw APDU. The client may select, authenticate or
// send commands of its own, any of which leaves the card in a state this tag's
// session does not describe, so the session is dropped first.
func (t *pcscNTAG424Tag) Transceive(data []byte) ([]byte, error) {
	t.mu.Lock()
	t.session = nil
	t.ndefSettings = nil
	t.mu.Unlock()
	return t.pcscISO14443Tag.Transceive(data)
}

// authStatus classifies a card's answer to a step of authentication: nil for
// success or a request for the next frame, a StatusError otherwise.
func authStatus(resp []byte) error {
	if len(resp) < 2 {
		return fmt.Errorf("response is %d bytes, too short for a status word", len(resp))
	}
	sw1, sw2 := resp[len(resp)-2], resp[len(resp)-1]
	if (sw1 == 0x91 || sw1 == 0x90) && (sw2 == 0x00 || sw2 == 0xAF) {
		return nil
	}
	return &ntag424.StatusError{SW1: sw1, SW2: sw2}
}

// authenticate opens a session with the numbered key, or returns the one
// already open under it. The NDEF application is selected first, which also
// clears any session the card held.
//
// A tag in LRP mode is refused. The card's delay (91 AD) and a key it refused
// are remembered and not tried again until the keys change, because each
// failure counts toward the card's lockout.
func (t *pcscNTAG424Tag) authenticate(keyNo byte) (*ev2.Session, error) {
	const op = "authenticate (NTAG 424)"

	t.mu.Lock()
	if t.session != nil && t.sessionKeyNo == keyNo {
		s := t.session
		t.mu.Unlock()
		return s, nil
	}
	var refused error
	switch {
	case t.lrp:
		refused = ntag424.ErrLRP
	case t.delayed:
		refused = ntag424.ErrAuthDelay
	default:
		refused = t.failedAuth[keyNo]
	}
	t.mu.Unlock()
	if refused != nil {
		return nil, NewAuthError(op, t.UID(), refused)
	}

	key, err := t.keyFor(keyNo)
	if err != nil {
		if IsCardRemovedError(err) || IsAuthError(err) {
			return nil, err
		}
		return nil, NewAuthError(op, t.UID(), err)
	}

	t.dropSession()
	if resp, err := t.transmitRaw(SelectFileByAIDAPDU(ndefAppAID)); err != nil {
		return nil, err
	} else if n := len(resp); n < 2 || resp[n-2] != 0x90 || resp[n-1] != 0x00 {
		return nil, NewAuthError(op, t.UID(), fmt.Errorf("select NDEF application: card answered % X", resp))
	}

	auth, err := ev2.NewAuthenticator(ev2.AuthFirst, keyNo, key, nil)
	if err != nil {
		return nil, NewAuthError(op, t.UID(), err)
	}

	first, err := t.transmitRaw(auth.Command())
	if err != nil {
		return nil, err
	}
	if ntag424.IsLRPAuthResponse(first) {
		t.mu.Lock()
		t.lrp = true
		t.mu.Unlock()
		return nil, NewAuthError(op, t.UID(), ntag424.ErrLRP)
	}
	if err := authStatus(first); err != nil {
		return nil, t.authRefused(op, keyNo, err)
	}
	second, err := auth.Challenge(first)
	if err != nil {
		return nil, NewAuthError(op, t.UID(), err)
	}
	answer, err := t.transmitRaw(second)
	if err != nil {
		return nil, err
	}
	if err := authStatus(answer); err != nil {
		return nil, t.authRefused(op, keyNo, err)
	}
	session, err := auth.Finish(answer)
	if err != nil {
		return nil, t.authRefused(op, keyNo, err)
	}

	t.mu.Lock()
	t.session, t.sessionKeyNo = session, keyNo
	t.mu.Unlock()
	return session, nil
}

// authRefused records why the card refused a key and builds the error.
func (t *pcscNTAG424Tag) authRefused(op string, keyNo byte, cause error) error {
	t.mu.Lock()
	if errors.Is(cause, ntag424.ErrAuthDelay) {
		t.delayed = true
	} else {
		if t.failedAuth == nil {
			t.failedAuth = make(map[byte]error)
		}
		t.failedAuth[keyNo] = cause
	}
	t.mu.Unlock()
	return NewAuthError(op, t.UID(), cause)
}

// withSession runs fn in a session under the numbered key. A card that no
// longer holds the session (91 AE, 91 1E, 91 7E) gets one fresh authentication
// and one more try; any other failure drops the session, since after one the
// card's counter and this side's may no longer agree.
func (t *pcscNTAG424Tag) withSession(keyNo byte, fn func(*ev2.Session) error) error {
	for attempt := 0; ; attempt++ {
		s, err := t.authenticate(keyNo)
		if err != nil {
			return err
		}
		err = fn(s)
		if err == nil {
			return nil
		}
		t.dropSession()
		if attempt == 0 && errors.Is(err, ntag424.ErrSessionLost) {
			continue
		}
		return err
	}
}

// selectApp selects the NDEF application, which clears the card's session. Run
// before a command sent outside one, so the card does not read it as inside.
func (t *pcscNTAG424Tag) selectApp() error {
	t.dropSession()
	resp, err := t.transmitRaw(SelectFileByAIDAPDU(ndefAppAID))
	if err != nil {
		return err
	}
	if n := len(resp); n < 2 || resp[n-2] != 0x90 || resp[n-1] != 0x00 {
		return fmt.Errorf("select NDEF application: card answered % X", resp)
	}
	return nil
}

// getFileSettings reads a file's settings: inside the open session when there
// is one, plainly when the card allows it, and otherwise under key 0, the
// factory change key.
func (t *pcscNTAG424Tag) getFileSettings(fileNo byte) (*ntag424.FileSettings, error) {
	inSession := func(s *ev2.Session) (fs *ntag424.FileSettings, err error) {
		cmd, err := ntag424.GetFileSettings(s, fileNo)
		if err != nil {
			return nil, err
		}
		resp, err := t.transmitRaw(cmd)
		if err != nil {
			return nil, err
		}
		return ntag424.ParseFileSettingsResponse(s, resp)
	}

	if s, _ := t.currentSession(); s != nil {
		fs, err := inSession(s)
		if err == nil {
			t.cacheSettings(fileNo, fs)
			return fs, nil
		}
		t.dropSession()
		if !errors.Is(err, ntag424.ErrSessionLost) {
			return nil, err
		}
	}

	if err := t.selectApp(); err != nil {
		return nil, err
	}
	resp, err := t.transmitRaw(ntag424.GetFileSettingsPlain(fileNo))
	if err != nil {
		return nil, err
	}
	fs, err := ntag424.ParseFileSettingsResponse(nil, resp)
	if err == nil {
		t.cacheSettings(fileNo, fs)
		return fs, nil
	}
	var status *ntag424.StatusError
	if !errors.As(err, &status) || status.SW2 != n4StatusAuthRequired || !t.holds(0) {
		return nil, err
	}

	err = t.withSession(0, func(s *ev2.Session) error {
		var err error
		fs, err = inSession(s)
		return err
	})
	if err != nil {
		return nil, err
	}
	t.cacheSettings(fileNo, fs)
	return fs, nil
}

// settingsDenied reports that the card would not show the file's settings
// without a key this agent cannot supply, which means the file is protected.
func settingsDenied(err error) bool {
	var status *ntag424.StatusError
	if errors.As(err, &status) {
		return status.SW2 == n4StatusAuthRequired || errors.Is(err, ntag424.ErrPermissionDenied)
	}
	return false
}

func (t *pcscNTAG424Tag) cacheSettings(fileNo byte, fs *ntag424.FileSettings) {
	if fileNo != n4FileNDEF {
		return
	}
	cp := *fs
	t.mu.Lock()
	t.ndefSettings = &cp
	t.mu.Unlock()
}

func (t *pcscNTAG424Tag) cachedSettings() *ntag424.FileSettings {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.ndefSettings == nil {
		return nil
	}
	cp := *t.ndefSettings
	return &cp
}

// How a file operation reaches the card.
type n4Route struct {
	// native is false for the ISO path, which needs no session; otherwise
	// keyNo opens the session and mode is the file's communication mode.
	native bool
	keyNo  byte
	mode   ntag424.CommMode
}

// routeFor decides how the NDEF file is read or written, from its settings. A
// right that grants the operation to anyone, in plain, is the ISO path; a right
// that names a key, or a file in MAC or full mode, needs a session. The second
// result is why no route exists: the rights deny every key, or the key they
// name is not held.
func (t *pcscNTAG424Tag) routeFor(fs *ntag424.FileSettings, write bool) (n4Route, error) {
	rights := [2]byte{fs.Read, fs.ReadWrite}
	if write {
		rights[0] = fs.Write
	}

	free := rights[0] == ntag424.AccessFree || rights[1] == ntag424.AccessFree
	if free && fs.CommMode == ntag424.CommPlain {
		return n4Route{}, nil
	}

	route := n4Route{native: true, mode: fs.CommMode}
	if free {
		keyNo, ok := t.anyHeldKey()
		if !ok {
			return n4Route{}, fmt.Errorf("the file needs a %s session and no key is held", modeName(fs.CommMode))
		}
		route.keyNo = keyNo
		return route, nil
	}
	for _, r := range rights {
		if r <= 4 {
			if !t.holds(r) {
				return n4Route{}, fmt.Errorf("the file's access rights name key %d, which is not held", r)
			}
			route.keyNo = r
			return route, nil
		}
	}
	return n4Route{}, fmt.Errorf("the file's access rights deny the operation to every key")
}

func modeName(m ntag424.CommMode) string {
	switch m {
	case ntag424.CommMAC:
		return "MAC"
	case ntag424.CommFull:
		return "full"
	}
	return "plain"
}

func (t *pcscNTAG424Tag) ReadData() ([]byte, error) {
	fs, err := t.getFileSettings(n4FileNDEF)
	if err != nil {
		if IsCardRemovedError(err) {
			return nil, err
		}
		if settingsDenied(err) {
			return nil, NewNoPayloadError("ReadData (NTAG 424)", t.UID(), err)
		}
		// Settings that cannot be read leave the ISO path, which reads what
		// the card shows anyone.
		t.dropSession()
		return t.pcscISO14443Tag.ReadData()
	}

	route, err := t.routeFor(fs, false)
	if err != nil {
		return nil, NewNoPayloadError("ReadData (NTAG 424)", t.UID(), err)
	}
	if !route.native {
		t.dropSession()
		return t.pcscISO14443Tag.ReadData()
	}

	var out []byte
	err = t.withSession(route.keyNo, func(s *ev2.Session) error {
		nlenData, err := t.readChunks(s, route.mode, 0, 2)
		if err != nil {
			return fmt.Errorf("read NLEN: %w", err)
		}
		nlen := int(nlenData[0])<<8 | int(nlenData[1])
		if nlen == 0 {
			return nil
		}
		if fs.FileSize > 2 && nlen > int(fs.FileSize)-2 {
			return fmt.Errorf("NLEN %d exceeds the %d bytes the NDEF file holds", nlen, fs.FileSize-2)
		}
		out, err = t.readChunks(s, route.mode, 2, nlen)
		if err != nil {
			return fmt.Errorf("read NDEF data: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, NewNoPayloadError("ReadData (NTAG 424)", t.UID(), nil)
	}
	return out, nil
}

// readChunks reads length bytes of the NDEF file inside a session, one command
// per chunk.
func (t *pcscNTAG424Tag) readChunks(s *ev2.Session, mode ntag424.CommMode, offset, length int) ([]byte, error) {
	chunk := n4ReadChunk
	if mode == ntag424.CommFull {
		chunk = n4ReadChunkFull
	}
	out := make([]byte, 0, length)
	for len(out) < length {
		want := min(chunk, length-len(out))
		cmd, err := ntag424.ReadData(s, n4FileNDEF, uint32(offset+len(out)), uint32(want), mode)
		if err != nil {
			return nil, err
		}
		resp, err := t.transmitRaw(cmd)
		if err != nil {
			return nil, err
		}
		data, err := ntag424.ParseReadData(s, resp, mode)
		if err != nil {
			return nil, err
		}
		if len(data) != want {
			return nil, fmt.Errorf("card returned %d of %d bytes at %d", len(data), want, offset+len(out))
		}
		out = append(out, data...)
	}
	return out, nil
}

func (t *pcscNTAG424Tag) WriteData(data []byte) error {
	fs, err := t.getFileSettings(n4FileNDEF)
	if err != nil {
		if IsCardRemovedError(err) {
			return err
		}
		if settingsDenied(err) {
			return NewReadOnlyError("WriteData (NTAG 424)", t.UID(), err)
		}
		t.dropSession()
		return t.pcscISO14443Tag.WriteData(data)
	}
	if capacity := int(fs.FileSize) - 2; fs.FileSize > 2 && len(data) > capacity {
		return NewCapacityExceededError("WriteData (NTAG 424)", t.UID(), len(data), capacity)
	}

	route, err := t.routeFor(fs, true)
	if err != nil {
		return NewReadOnlyError("WriteData (NTAG 424)", t.UID(), err)
	}
	if !route.native {
		t.dropSession()
		return t.pcscISO14443Tag.WriteData(data)
	}

	return t.withSession(route.keyNo, func(s *ev2.Session) error {
		if err := t.writeChunks(s, route.mode, 0, []byte{0x00, 0x00}); err != nil {
			return fmt.Errorf("clear NLEN: %w", err)
		}
		if err := t.writeChunks(s, route.mode, 2, data); err != nil {
			return fmt.Errorf("write NDEF data: %w", err)
		}
		nlen := len(data)
		if err := t.writeChunks(s, route.mode, 0, []byte{byte(nlen >> 8), byte(nlen)}); err != nil {
			return fmt.Errorf("write NLEN: %w", err)
		}
		return nil
	})
}

// writeChunks writes data into the NDEF file inside a session, one command per
// chunk.
func (t *pcscNTAG424Tag) writeChunks(s *ev2.Session, mode ntag424.CommMode, offset int, data []byte) error {
	chunk := ntag424.MaxWriteChunk(mode)
	for written := 0; written < len(data); {
		part := data[written:min(written+chunk, len(data))]
		cmd, err := ntag424.WriteData(s, n4FileNDEF, uint32(offset+written), part, mode)
		if err != nil {
			return err
		}
		resp, err := t.transmitRaw(cmd)
		if err != nil {
			return err
		}
		if err := ntag424.ParseWriteData(s, resp, mode); err != nil {
			return err
		}
		written += len(part)
	}
	return nil
}

func (t *pcscNTAG424Tag) IsWritable() (bool, error) {
	fs, err := t.getFileSettings(n4FileNDEF)
	if err != nil {
		if IsCardRemovedError(err) {
			return false, err
		}
		if settingsDenied(err) {
			return false, nil
		}
		t.dropSession()
		return t.pcscISO14443Tag.IsWritable()
	}
	_, err = t.routeFor(fs, true)
	return err == nil, nil
}

// changeKeyFor names the key that may change the file's settings, or false when
// none is held or the rights deny every key. A change right open to anyone
// still needs a session, which any held key opens.
func (t *pcscNTAG424Tag) changeKeyFor(fs *ntag424.FileSettings) (byte, bool) {
	switch fs.Change {
	case ntag424.AccessNever:
		return 0, false
	case ntag424.AccessFree:
		return t.anyHeldKey()
	}
	return fs.Change, fs.Change <= 4 && t.holds(fs.Change)
}

// canLock reports whether the NDEF file's rights can be rewritten: the settings
// as last read name a key that is held, or, before any has been read, key 0 is
// held, which is the factory change key.
func (t *pcscNTAG424Tag) canLock() bool {
	if fs := t.cachedSettings(); fs != nil {
		_, ok := t.changeKeyFor(fs)
		return ok
	}
	return t.holds(0)
}

// Capabilities reports the profile's, with what the keys held and the settings
// last read say layered over it. It sends nothing itself.
func (t *pcscNTAG424Tag) Capabilities() TagCapabilities {
	caps := t.pcscISO14443Tag.Capabilities()
	caps.CanLock = t.canLock()
	if fs := t.cachedSettings(); fs != nil {
		_, err := t.routeFor(fs, true)
		caps.CanWrite = err == nil
		caps.IsReadOnly = !caps.CanWrite
	}
	return caps
}

func (t *pcscNTAG424Tag) CanMakeReadOnly() (bool, error) {
	return t.canLock(), nil
}

// MakeReadOnly rewrites the NDEF file's access rights so that nothing may write
// it. The change right is kept, so a holder of that key can still reopen the
// file, unlike a lock that denies it too.
//
// This needs the change key. It cannot be undone by anyone without it.
func (t *pcscNTAG424Tag) MakeReadOnly() error {
	fs, err := t.getFileSettings(n4FileNDEF)
	if err != nil {
		if IsCardRemovedError(err) {
			return err
		}
		return NewNotSupportedError("MakeReadOnly (NTAG 424)")
	}
	if _, ok := t.changeKeyFor(fs); !ok {
		return NewNotSupportedError("MakeReadOnly (NTAG 424)")
	}

	locked := *fs
	// A file read only through the read-write right would lose its read access
	// with that right, so the read right takes it over first.
	if locked.Read == ntag424.AccessNever {
		locked.Read = locked.ReadWrite
	}
	locked.Write, locked.ReadWrite = ntag424.AccessNever, ntag424.AccessNever
	return t.ChangeFileSettings(n4FileNDEF, locked)
}
