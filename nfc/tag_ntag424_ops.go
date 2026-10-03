package nfc

import (
	"crypto/ecdsa"
	"errors"
	"fmt"

	"github.com/dotside-studios/davi-nfc-agent/nfc/ev2"
	"github.com/dotside-studios/davi-nfc-agent/nfc/ntag424"
)

// NTAG424Operator is what an NTAG 424 DNA's driver offers beyond NDEF: the
// card's own commands, run in a session under the keys the agent holds.
type NTAG424Operator interface {
	// GetFileSettings reads a file's settings.
	GetFileSettings(fileNo byte) (*ntag424.FileSettings, error)

	// ChangeFileSettings rewrites a file's settings, under the key its change
	// right names.
	ChangeFileSettings(fileNo byte, settings ntag424.FileSettings) error

	// ChangeKey replaces key keyNo, authenticating with authKeyNo. A key other
	// than the session's is sent against the old key held for it. The key
	// cannot be recovered afterwards, and the agent's own key set is not
	// updated: set the new keys before the next operation.
	ChangeKey(keyNo byte, newKey []byte, version byte, authKeyNo byte) error

	// GetCardUID returns the card's real UID, which differs from the one it
	// presents when the random UID is on.
	GetCardUID() ([]byte, error)

	// GetKeyVersion reads a key's version byte.
	GetKeyVersion(keyNo byte) (byte, error)

	// ReadSig reads the 56-byte originality signature, unverified.
	ReadSig() ([]byte, error)

	// ReadOriginality reads the originality signature and verifies it over the
	// card's real UID, resolved with GetCardUID on a random-ID card. A nil
	// key means NXP's, and genuine is false for a signature that does not
	// verify.
	ReadOriginality(pub *ecdsa.PublicKey) (sig []byte, genuine bool, err error)

	// ConfigureSDM writes a plan's NDEF message and file settings, then reads
	// the file back as a tap and verifies it under the keys held. The read
	// counts as a tap.
	ConfigureSDM(plan *ntag424.SDMPlan) (*NTAG424SDMResult, error)

	// RandomID reports whether the card presented a random UID.
	RandomID() bool
}

var _ NTAG424Operator = (*pcscNTAG424Tag)(nil)

// NTAG424KeySource is an NTAG424Operator that can name the key the agent holds
// for a key number. It exists so a caller can program the card with a key the
// agent already has, without that key crossing any boundary it need not.
type NTAG424KeySource interface {
	ConfiguredKey(keyNo byte) ([]byte, error)
}

var _ NTAG424KeySource = (*pcscNTAG424Tag)(nil)

// ConfiguredKey returns the key held for keyNo for this card, diversified by
// its UID when the key set says so.
func (t *pcscNTAG424Tag) ConfiguredKey(keyNo byte) ([]byte, error) {
	key, err := t.keyFor(keyNo)
	if err != nil {
		return nil, NewAuthError("ConfiguredKey (NTAG 424)", t.UID(), err)
	}
	return key, nil
}

// NTAG424SDMResult is the outcome of ConfigureSDM. When the settings were
// applied but the tap could not be verified, the result carries the URL read
// back with a nil Tap, alongside the error.
type NTAG424SDMResult struct {
	// URL is the URL the card mirrored on the read-back.
	URL string

	// Tap is that read, verified under the held keys.
	Tap *ntag424.Tap
}

func (t *pcscNTAG424Tag) GetFileSettings(fileNo byte) (*ntag424.FileSettings, error) {
	return t.getFileSettings(fileNo)
}

func (t *pcscNTAG424Tag) ChangeFileSettings(fileNo byte, settings ntag424.FileSettings) error {
	encoded, err := settings.Encode()
	if err != nil {
		return err
	}

	// The current change right names the key. When it cannot be read, key 0 is
	// the factory change key.
	keyNo := byte(0)
	if cur, err := t.getFileSettings(fileNo); err == nil {
		k, ok := t.changeKeyFor(cur)
		if !ok {
			return NewAuthError("ChangeFileSettings (NTAG 424)", t.UID(),
				fmt.Errorf("no held key may change file %d", fileNo))
		}
		keyNo = k
	} else if IsCardRemovedError(err) {
		return err
	}

	err = t.withSession(keyNo, func(s *ev2.Session) error {
		cmd, err := ntag424.ChangeFileSettings(s, fileNo, encoded)
		if err != nil {
			return err
		}
		resp, err := t.transmitRaw(cmd)
		if err != nil {
			return err
		}
		_, err = ntag424.CheckResponse(s, resp, ntag424.CommFull)
		return err
	})
	if err != nil {
		return err
	}
	t.mu.Lock()
	t.ndefSettings = nil
	t.mu.Unlock()
	return nil
}

func (t *pcscNTAG424Tag) ChangeKey(keyNo byte, newKey []byte, version byte, authKeyNo byte) error {
	var old []byte
	if keyNo != authKeyNo {
		var err error
		if old, err = t.keyFor(keyNo); err != nil {
			return NewAuthError("ChangeKey (NTAG 424)", t.UID(), err)
		}
	}
	err := t.withSession(authKeyNo, func(s *ev2.Session) error {
		cmd, err := ntag424.ChangeKey(s, keyNo, authKeyNo, old, newKey, version)
		if err != nil {
			return err
		}
		resp, err := t.transmitRaw(cmd)
		if err != nil {
			return err
		}
		return ntag424.CheckChangeKeyResponse(s, resp, keyNo, authKeyNo)
	})
	if err == nil && keyNo == authKeyNo {
		t.dropSession()
	}
	return err
}

func (t *pcscNTAG424Tag) GetCardUID() ([]byte, error) {
	t.mu.Lock()
	real := t.realUID
	t.mu.Unlock()
	if real != nil {
		return append([]byte(nil), real...), nil
	}
	if !t.RandomID() {
		return t.keyUID(), nil
	}
	if t.heldKeys().Empty() {
		return nil, NewAuthError("GetCardUID (NTAG 424)", t.uid, fmt.Errorf("no keys held"))
	}
	if err := t.resolveUID(); err != nil {
		return nil, err
	}
	return append([]byte(nil), t.keyUID()...), nil
}

func (t *pcscNTAG424Tag) GetKeyVersion(keyNo byte) (byte, error) {
	if s, _ := t.currentSession(); s != nil {
		cmd, err := ntag424.GetKeyVersion(s, keyNo)
		if err == nil {
			var resp []byte
			if resp, err = t.transmitRaw(cmd); err == nil {
				var v byte
				if v, err = ntag424.ParseKeyVersion(s, resp); err == nil {
					return v, nil
				}
			}
		}
		t.dropSession()
		if !errors.Is(err, ntag424.ErrSessionLost) {
			return 0, err
		}
	}
	if err := t.selectApp(); err != nil {
		return 0, err
	}
	resp, err := t.transmitRaw(ntag424.GetKeyVersionPlain(keyNo))
	if err != nil {
		return 0, err
	}
	return ntag424.ParseKeyVersion(nil, resp)
}

func (t *pcscNTAG424Tag) ReadSig() ([]byte, error) {
	keyNo, err := t.sessionKey()
	if err != nil {
		return nil, err
	}
	var sig []byte
	err = t.withSession(keyNo, func(s *ev2.Session) error {
		cmd, err := ntag424.ReadSig(s)
		if err != nil {
			return err
		}
		resp, err := t.transmitRaw(cmd)
		if err != nil {
			return err
		}
		sig, err = ntag424.ParseReadSig(s, resp)
		return err
	})
	return sig, err
}

func (t *pcscNTAG424Tag) ReadOriginality(pub *ecdsa.PublicKey) ([]byte, bool, error) {
	uid, err := t.GetCardUID()
	if err != nil {
		return nil, false, err
	}
	sig, err := t.ReadSig()
	if err != nil {
		return nil, false, err
	}
	return sig, ntag424.VerifyOriginality(uid, sig, pub), nil
}

func (t *pcscNTAG424Tag) ConfigureSDM(plan *ntag424.SDMPlan) (*NTAG424SDMResult, error) {
	const op = "ConfigureSDM (NTAG 424)"
	if plan == nil || len(plan.NDEF) < 2 {
		return nil, fmt.Errorf("%s: no plan", op)
	}
	if nlen := int(plan.NDEF[0])<<8 | int(plan.NDEF[1]); nlen != len(plan.NDEF)-2 {
		return nil, fmt.Errorf("%s: plan NLEN %d does not match its %d message bytes", op, nlen, len(plan.NDEF)-2)
	}

	if err := t.WriteData(plan.NDEF[2:]); err != nil {
		return nil, fmt.Errorf("%s: write NDEF: %w", op, err)
	}
	if err := t.ChangeFileSettings(n4FileNDEF, plan.Settings); err != nil {
		return nil, fmt.Errorf("%s: change file settings: %w", op, err)
	}

	data, err := t.ReadData()
	if err != nil {
		return nil, fmt.Errorf("%s: read back: %w", op, err)
	}
	msg, err := DecodeNDEF(data)
	if err != nil {
		return nil, fmt.Errorf("%s: decode read back: %w", op, err)
	}
	url, err := msg.GetURI()
	if err != nil {
		return nil, fmt.Errorf("%s: read back holds no URL: %w", op, err)
	}
	result := &NTAG424SDMResult{URL: url}

	var keys ntag424.Keys
	if n := plan.Settings.SDMMetaRead; n <= 4 {
		if keys.MetaRead, err = t.keyFor(n); err != nil {
			return result, fmt.Errorf("%s: verify: %w", op, err)
		}
	}
	if n := plan.Settings.SDMFileRead; n <= 4 {
		if keys.FileRead, err = t.keyFor(n); err != nil {
			return result, fmt.Errorf("%s: verify: %w", op, err)
		}
	}
	tap, err := ntag424.VerifyURLWith(url, keys, planNames(plan))
	if err != nil {
		return result, fmt.Errorf("%s: verify: %w", op, err)
	}
	if uid := t.keyUID(); len(uid) == len(tap.UID) && !t.RandomID() && string(uid) != string(tap.UID) {
		return result, fmt.Errorf("%s: verify: tap UID %s is not the card's", op, tap.UIDString())
	}
	result.Tap = tap
	return result, nil
}

// planNames are the parameter names a verifier should look for: the common
// spellings, with the ones the plan's own URL uses first. They are read from
// the text in front of each mirror, since the template's names are not kept.
func planNames(plan *ntag424.SDMPlan) ntag424.Names {
	names := ntag424.DefaultNames()
	s := plan.Settings
	add := func(dst *[]string, offset uint32) {
		if name := paramNameBefore(plan.NDEF, offset); name != "" {
			*dst = append([]string{name}, *dst...)
		}
	}
	if s.SDMMetaRead <= 4 {
		add(&names.PICCData, s.PICCDataOffset)
	} else {
		add(&names.UID, s.UIDOffset)
		add(&names.Counter, s.ReadCounterOffset)
	}
	if s.EncryptFileData {
		add(&names.EncFileData, s.ENCOffset)
	}
	add(&names.MAC, s.MACOffset)
	return names
}

// paramNameBefore reads the query parameter name that ends in "=" just before
// offset in the NDEF file, or "" when the text there is not one.
func paramNameBefore(ndef []byte, offset uint32) string {
	end := int(offset)
	if end < 1 || end > len(ndef) || ndef[end-1] != '=' {
		return ""
	}
	start := end - 1
	for start > 0 && ndef[start-1] != '?' && ndef[start-1] != '&' {
		start--
	}
	return string(ndef[start : end-1])
}
