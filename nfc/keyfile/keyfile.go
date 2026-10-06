// Package keyfile reads the file an operator keeps the agent's card keys in:
// the MIFARE Classic keys, the DESFire AES keys and the NTAG 424 DNA key set,
// in one JSON document.
//
// The file is the whole of the mechanism. Nothing here encrypts it, asks an
// operating-system keychain or reloads it behind the agent's back; the
// operator's protection of it is the file's permissions, which Load enforces
// the way ssh does for a private key.
//
// An error from this package names the section and the field a problem is in,
// and never a value from the file: a mistyped key must not reach a log.
package keyfile

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
	"github.com/dotside-studios/davi-nfc-agent/nfc/ntag424"
)

const (
	// maxFileSize bounds what Load reads. A key file is a few hundred bytes; a
	// larger one is the wrong file.
	maxFileSize = 1 << 20

	classicKeySize = 6
	desfireKeySize = 16

	// maxNTAG424Key is the highest NTAG 424 DNA key number, and maxDESFireKey
	// the highest AES key number a DESFire application holds.
	maxNTAG424Key = 4
	maxDESFireKey = 13

	// maxSystemIDSize is what diversification leaves room for: one 32-byte
	// block, less the constant and a 7-byte UID.
	maxSystemIDSize = 24
)

// Keys is what a key file holds, in the types the readers take.
type Keys struct {
	// Classic are 6-byte MIFARE Classic keys, tried before the built-in
	// defaults.
	Classic [][]byte

	// DESFire are AES-128 keys by key number.
	DESFire nfc.DESFireKeys

	// NTAG424 is the NTAG 424 DNA key set.
	NTAG424 nfc.NTAG424Keys
}

// Empty reports whether the file held no keys at all.
func (k Keys) Empty() bool {
	return len(k.Classic) == 0 && len(k.DESFire) == 0 && k.NTAG424.Empty()
}

// Copy returns keys that share no memory with k.
func (k Keys) Copy() Keys {
	out := Keys{DESFire: k.DESFire.Copy(), NTAG424: k.NTAG424.Copy()}
	for _, key := range k.Classic {
		out.Classic = append(out.Classic, slices.Clone(key))
	}
	return out
}

// Apply hands the keys to every reader s operates, including readers opened
// later. Each kind replaces what the supervisor held, so a file without a
// section clears that kind: the file is the whole of the configuration.
//
// Setting NTAG 424 keys drops the sessions the readers have open.
func (k Keys) Apply(s *nfc.Supervisor) {
	c := k.Copy()
	s.SetClassicKeys(c.Classic)
	s.SetDESFireKeys(c.DESFire)
	s.SetNTAG424Keys(c.NTAG424)
}

// Summary says which keys were loaded, by kind and number, for a log line. It
// holds no key material.
func (k Keys) Summary() string {
	var parts []string
	if n := len(k.Classic); n > 0 {
		parts = append(parts, fmt.Sprintf("MIFARE Classic keys: %d", n))
	}
	if len(k.DESFire) > 0 {
		parts = append(parts, "DESFire keys: slots "+slotList(k.DESFire))
	}
	if !k.NTAG424.Empty() {
		var held []string
		if len(k.NTAG424.Master) > 0 {
			if k.NTAG424.Diversify {
				held = append(held, "master (diversified)")
			} else {
				held = append(held, "master")
			}
		}
		if len(k.NTAG424.Slots) > 0 {
			held = append(held, "slots "+slotList(k.NTAG424.Slots))
		}
		s := "NTAG 424 keys: " + strings.Join(held, ", ")
		if k.NTAG424.AllowLRP {
			s += ", LRP allowed"
		}
		parts = append(parts, s)
	}
	if len(parts) == 0 {
		return "no keys"
	}
	return strings.Join(parts, "; ")
}

func slotList(slots map[byte][]byte) string {
	nos := make([]int, 0, len(slots))
	for n := range slots {
		nos = append(nos, int(n))
	}
	sort.Ints(nos)
	out := make([]string, len(nos))
	for i, n := range nos {
		out[i] = strconv.Itoa(n)
	}
	return strings.Join(out, ",")
}

// Load reads and validates the key file at path. It refuses a file that is not
// a regular file, and on Unix one that group or others can access: a key
// readable by another account is not a secret, and the operator is told to
// chmod 600 rather than left with keys that work.
//
// Windows has no mode bits to check. Access there is an ACL, which the mode a
// Go program sees does not reflect, so the check is skipped and the file's
// ACL is the operator's to set.
func Load(path string) (Keys, error) {
	f, err := os.Open(path)
	if err != nil {
		return Keys{}, fmt.Errorf("key file: %w", unwrapPathError(err))
	}
	defer func() { _ = f.Close() }()

	// Checked on the open handle, so the file inspected is the file read.
	info, err := f.Stat()
	if err != nil {
		return Keys{}, fmt.Errorf("key file: %w", unwrapPathError(err))
	}
	if !info.Mode().IsRegular() {
		return Keys{}, errors.New("key file: not a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return Keys{}, fmt.Errorf("key file: permissions %04o are too open, group and others must have no access; run: chmod 600 <file>", info.Mode().Perm())
	}
	if info.Size() > maxFileSize {
		return Keys{}, fmt.Errorf("key file: larger than %d bytes", maxFileSize)
	}

	data, err := io.ReadAll(io.LimitReader(f, maxFileSize+1))
	if err != nil {
		return Keys{}, fmt.Errorf("key file: %w", unwrapPathError(err))
	}
	return Parse(data)
}

func unwrapPathError(err error) error {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

type fileJSON struct {
	Classic []string     `json:"classic"`
	DESFire *desfireJSON `json:"desfire"`
	NTAG424 *ntag424JSON `json:"ntag424"`
}

type desfireJSON struct {
	Slots map[string]string `json:"slots"`
}

type ntag424JSON struct {
	Master    string            `json:"master"`
	Diversify bool              `json:"diversify"`
	SystemID  string            `json:"systemID"`
	Slots     map[string]string `json:"slots"`
	AllowLRP  bool              `json:"allowLRP"`
}

// Parse validates a key file's contents. Unknown fields, malformed hex and keys
// of the wrong length are errors.
func Parse(data []byte) (Keys, error) {
	var f fileJSON
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return Keys{}, fmt.Errorf("key file: %s", describeJSONError(err))
	}
	if _, err := dec.Token(); err != io.EOF {
		return Keys{}, errors.New("key file: unexpected data after the JSON document")
	}

	var keys Keys
	for i, s := range f.Classic {
		key, err := decodeKey(s, classicKeySize)
		if err != nil {
			return Keys{}, fmt.Errorf("key file: classic[%d]: %w", i, err)
		}
		keys.Classic = append(keys.Classic, key)
	}

	if f.DESFire != nil {
		slots, err := decodeSlots(f.DESFire.Slots, desfireKeySize, maxDESFireKey)
		if err != nil {
			return Keys{}, fmt.Errorf("key file: desfire.slots: %w", err)
		}
		if len(slots) > 0 {
			keys.DESFire = nfc.DESFireKeys(slots)
		}
	}

	if f.NTAG424 != nil {
		set, err := parseNTAG424(f.NTAG424)
		if err != nil {
			return Keys{}, err
		}
		keys.NTAG424 = set
	}
	return keys, nil
}

func parseNTAG424(n *ntag424JSON) (nfc.NTAG424Keys, error) {
	var set nfc.NTAG424Keys
	var err error
	if n.Master != "" {
		if set.Master, err = decodeKey(n.Master, ntag424.KeySize); err != nil {
			return set, fmt.Errorf("key file: ntag424.master: %w", err)
		}
	}
	if n.SystemID != "" {
		raw, err := hex.DecodeString(n.SystemID)
		if err != nil {
			return set, errors.New("key file: ntag424.systemID: not valid hex")
		}
		if len(raw) > maxSystemIDSize {
			return set, fmt.Errorf("key file: ntag424.systemID: %d bytes, at most %d", len(raw), maxSystemIDSize)
		}
		set.SystemID = raw
	}
	if set.Slots, err = decodeSlots(n.Slots, ntag424.KeySize, maxNTAG424Key); err != nil {
		return set, fmt.Errorf("key file: ntag424.slots: %w", err)
	}
	set.Diversify = n.Diversify
	set.AllowLRP = n.AllowLRP

	if set.Diversify && len(set.Master) == 0 {
		return set, errors.New("key file: ntag424.diversify needs ntag424.master")
	}
	if len(set.SystemID) > 0 && !set.Diversify {
		return set, errors.New("key file: ntag424.systemID is only used with ntag424.diversify")
	}
	return set, nil
}

func decodeKey(s string, size int) ([]byte, error) {
	if len(s) != size*2 {
		return nil, fmt.Errorf("must be %d hex characters (%d bytes)", size*2, size)
	}
	key, err := hex.DecodeString(s)
	if err != nil {
		return nil, errors.New("not valid hex")
	}
	return key, nil
}

// decodeSlots reads a map of decimal key numbers to hex keys. The errors name
// a slot by the number the file used, which is not key material.
func decodeSlots(in map[string]string, size, maxNo int) (map[byte][]byte, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[byte][]byte, len(in))
	for name, s := range in {
		n, err := strconv.ParseUint(name, 10, 8)
		if err != nil || int(n) > maxNo || strconv.FormatUint(n, 10) != name {
			return nil, fmt.Errorf("slot names must be decimal key numbers 0 to %d", maxNo)
		}
		key, err := decodeKey(s, size)
		if err != nil {
			return nil, fmt.Errorf("slot %d: %w", n, err)
		}
		out[byte(n)] = key
	}
	return out, nil
}

// describeJSONError says what is wrong with the document without quoting it.
// encoding/json's own messages name an unknown field and the value of a
// mistyped one, either of which could be a key.
func describeJSONError(err error) string {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syn):
		return fmt.Sprintf("not valid JSON (at byte %d)", syn.Offset)
	case errors.As(err, &typ):
		if strings.Contains(typ.Field, ".") || !safeFieldName(typ.Field) {
			return fmt.Sprintf("a value must be a JSON %s", typ.Type.String())
		}
		return fmt.Sprintf("field %q must be a JSON %s", typ.Field, typ.Type.String())
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "empty or truncated JSON"
	}
	if name, ok := strings.CutPrefix(err.Error(), "json: unknown field "); ok {
		if unq, uerr := strconv.Unquote(name); uerr == nil && safeFieldName(unq) {
			return fmt.Sprintf("unknown field %q", unq)
		}
		return "unknown field (name withheld)"
	}
	return "not a valid key file"
}

// safeFieldName admits a name that cannot be a key: short, and not made only of
// hex digits.
func safeFieldName(s string) bool {
	if s == "" || len(s) > 24 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return true
		}
	}
	return false
}
