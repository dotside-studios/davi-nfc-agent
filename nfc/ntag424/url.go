package ntag424

import (
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// Verifying the URL a tag serves.
//
// A tag mirrors its data into query parameters, and what the MAC covers is the
// URL text itself: the characters from the start of the encrypted file data up
// to the start of the MAC. So the raw query has to be read, not just its
// decoded values, and the parameters have to keep the order the tag wrote them
// in.

// ErrNotSDM reports a URL that carries no SDM parameters, or too few of them to
// verify. It is separate from a MAC mismatch: this URL never came from a tag,
// rather than came from one and failed.
var ErrNotSDM = errors.New("ntag424: URL carries no SDM data")

// Parameter names tags are configured with. The application note's own examples
// use two different sets, and deployments pick their own, so the common spellings
// are all recognised. A tag using something else is read with ParseURLWith.
var (
	defaultNames = Names{
		PICCData:    []string{"picc_data", "e", "picc"},
		EncFileData: []string{"enc", "d", "encdata"},
		MAC:         []string{"cmac", "c", "mac"},
		UID:         []string{"uid"},
		Counter:     []string{"ctr", "cnt", "counter"},
	}
)

// DefaultNames returns the parameter names recognised by VerifyURL, so a
// caller can extend them rather than restate them.
func DefaultNames() Names {
	return Names{
		PICCData:    append([]string(nil), defaultNames.PICCData...),
		EncFileData: append([]string(nil), defaultNames.EncFileData...),
		MAC:         append([]string(nil), defaultNames.MAC...),
		UID:         append([]string(nil), defaultNames.UID...),
		Counter:     append([]string(nil), defaultNames.Counter...),
	}
}

// Names are the query parameters a tag mirrors into, most preferred first.
type Names struct {
	PICCData    []string
	EncFileData []string
	MAC         []string
	UID         []string
	Counter     []string
}

// URLData is what a tapped URL carries, before any of it is verified.
type URLData struct {
	// PICCData is the encrypted PICCData block, empty when the tag mirrors its
	// UID and counter in the clear.
	PICCData []byte

	// UID and Counter are the plain mirrors, set only when the tag mirrors them
	// unencrypted.
	UID     []byte
	Counter uint32

	// HasPlainPICCData reports whether the plain mirrors above were present.
	HasPlainPICCData bool

	// EncFileData is the encrypted file data, nil when the tag mirrors none.
	EncFileData []byte

	// MAC is the tag's SDMMAC.
	MAC []byte

	// MACInput is the text the MAC covers: the URL from the start of the
	// encrypted file data to the start of the MAC. Empty when the tag mirrors
	// no file data.
	MACInput []byte
}

// Tap is a verified read of a tag: what the tag said, once its MAC has been
// checked against the key that could only be the tag's.
type Tap struct {
	// UID is the tag's serial number.
	UID []byte

	// ReadCounter is this read's number. It rises by one per tap, so a counter
	// at or below one already recorded for this UID is a replay of an earlier
	// URL, which no MAC can detect. Keeping the last counter per tag is the
	// caller's job.
	ReadCounter uint32

	// FileData is the mirrored file data, decrypted, or nil when the tag
	// mirrored none.
	FileData []byte
}

// UIDString renders the tag's UID as uppercase hex, as the rest of the agent
// writes one.
func (t *Tap) UIDString() string {
	return strings.ToUpper(hex.EncodeToString(t.UID))
}

// VerifyURL checks a tapped URL against a tag's keys and reports what it says.
//
// A nil error means this URL was produced by a tag holding these keys, and was
// not edited afterwards. It does not mean the tap is fresh: a URL captured once
// verifies forever, so compare Tap.ReadCounter against the highest counter
// already seen for this UID and refuse one that does not advance.
func VerifyURL(rawURL string, keys Keys) (*Tap, error) {
	return VerifyURLWith(rawURL, keys, defaultNames)
}

// VerifyURLWith is VerifyURL for a tag whose parameters are named something
// other than the common spellings.
func VerifyURLWith(rawURL string, keys Keys, names Names) (*Tap, error) {
	data, err := ParseURLWith(rawURL, names)
	if err != nil {
		return nil, err
	}
	return Verify(data, keys)
}

// Verify checks parsed URL data against a tag's keys. Use it when the URL was
// taken apart elsewhere, or when a tag's layout needs the MAC input chosen by
// hand.
func Verify(data *URLData, keys Keys) (*Tap, error) {
	picc, err := piccDataFor(data, keys)
	if err != nil {
		return nil, err
	}

	if err := VerifyMAC(keys.FileRead, picc, data.MACInput, data.MAC); err != nil {
		return nil, err
	}

	tap := &Tap{UID: picc.UID, ReadCounter: picc.ReadCounter}
	if len(data.EncFileData) > 0 {
		// Only now, with the MAC checked, is it worth decrypting: file data
		// from an unverified URL is whatever the sender chose.
		tap.FileData, err = DecryptFileData(keys.FileRead, picc, data.EncFileData)
		if err != nil {
			return nil, err
		}
	}
	return tap, nil
}

// piccDataFor recovers the tap's UID and counter, from the encrypted block when
// there is one and from the plain mirrors otherwise.
func piccDataFor(data *URLData, keys Keys) (*PICCData, error) {
	if len(data.PICCData) > 0 {
		return DecryptPICCData(keys.MetaRead, data.PICCData)
	}
	if data.HasPlainPICCData {
		return &PICCData{
			UID:             data.UID,
			ReadCounter:     data.Counter,
			UIDMirrored:     len(data.UID) > 0,
			CounterMirrored: true,
		}, nil
	}
	return nil, fmt.Errorf("%w: neither encrypted PICCData nor a plain UID", ErrNotSDM)
}

// ParseURL takes a tapped URL apart, without verifying anything.
func ParseURL(rawURL string) (*URLData, error) {
	return ParseURLWith(rawURL, defaultNames)
}

// ParseURLWith is ParseURL for tags whose parameters are named something other
// than the common spellings.
func ParseURLWith(rawURL string, names Names) (*URLData, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("ntag424: %w", err)
	}
	query := parsed.Query()
	raw := parsed.RawQuery

	data := &URLData{}
	if data.MAC, err = hexParam(query, names.MAC, MACSize); err != nil {
		return nil, err
	}
	if len(data.MAC) == 0 {
		return nil, fmt.Errorf("%w: no MAC parameter", ErrNotSDM)
	}

	if data.PICCData, err = hexParam(query, names.PICCData, PICCDataSize); err != nil {
		return nil, err
	}
	if len(data.PICCData) == 0 {
		if data.UID, data.Counter, data.HasPlainPICCData, err = plainMirrors(query, names); err != nil {
			return nil, err
		}
	}

	if data.EncFileData, err = hexParam(query, names.EncFileData, 0); err != nil {
		return nil, err
	}
	if len(data.EncFileData) > 0 {
		if data.MACInput, err = macInput(raw, names); err != nil {
			return nil, err
		}
	}
	return data, nil
}

// macInput is the URL text the MAC covers: from the first character of the
// encrypted file data's value to the character before the MAC's value. It spans
// the separator and the MAC parameter's own name, which is why it cannot be
// rebuilt from the decoded values.
func macInput(rawQuery string, names Names) ([]byte, error) {
	start, ok := valueOffset(rawQuery, names.EncFileData)
	if !ok {
		return nil, fmt.Errorf("%w: encrypted file data is not in the query", ErrNotSDM)
	}
	end, ok := valueOffset(rawQuery, names.MAC)
	if !ok {
		return nil, fmt.Errorf("%w: the MAC is not in the query", ErrNotSDM)
	}
	if end < start {
		return nil, fmt.Errorf("%w: the MAC precedes the data it covers", ErrNotSDM)
	}
	return []byte(rawQuery[start:end]), nil
}

// valueOffset finds where a parameter's value starts in the raw query.
func valueOffset(rawQuery string, names []string) (int, bool) {
	for _, name := range names {
		for _, prefix := range []string{name + "=", "&" + name + "="} {
			if i := strings.Index(rawQuery, prefix); i >= 0 {
				// A bare name must start the query or follow a separator, so a
				// parameter named "c" does not match inside "picc_data".
				if prefix[0] != '&' && i != 0 {
					continue
				}
				return i + len(prefix), true
			}
		}
	}
	return 0, false
}

// hexParam reads the first of the named parameters as hex. size, when non-zero,
// is the exact number of bytes the value must decode to.
func hexParam(query url.Values, names []string, size int) ([]byte, error) {
	for _, name := range names {
		value := query.Get(name)
		if value == "" {
			continue
		}
		raw, err := hex.DecodeString(value)
		if err != nil {
			return nil, fmt.Errorf("ntag424: %s is not hex: %w", name, err)
		}
		if size > 0 && len(raw) != size {
			return nil, fmt.Errorf("ntag424: %s is %d bytes, want %d", name, len(raw), size)
		}
		return raw, nil
	}
	return nil, nil
}

// plainMirrors reads a UID and counter mirrored in the clear.
//
// The counter's text is most significant digit first, which is not the order of
// the counter's bytes inside PICCData. A tag that mirrors it the other way needs
// its counter read by the caller and passed to Verify.
func plainMirrors(query url.Values, names Names) (uid []byte, counter uint32, ok bool, err error) {
	uid, err = hexParam(query, names.UID, uidLength)
	if err != nil {
		return nil, 0, false, err
	}
	raw, err := hexParam(query, names.Counter, counterLength)
	if err != nil {
		return nil, 0, false, err
	}
	if len(uid) == 0 && len(raw) == 0 {
		return nil, 0, false, nil
	}
	for _, b := range raw {
		counter = counter<<8 | uint32(b)
	}
	return uid, counter, true, nil
}
