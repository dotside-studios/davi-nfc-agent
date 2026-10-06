// Package fixture is the file format the hardware tests write and the unit
// tests replay: what a reader and a card said to each other, byte for byte.
//
// A run on real hardware (see package hwtest) records every command the reader's
// driver sent to the card and every answer. The files hold those bytes next to
// the unwrapped frames and results the tests asserted, so a later unit test can
// feed the recorded reader answers to the parsers and compare. Nothing in a
// fixture is synthesised: a file is either written from a recording or is not a
// fixture.
//
// The package carries no hardware dependency and no build tag, so the loaders
// and replays run in the ordinary test suite whenever a fixture is committed
// under a testdata directory.
package fixture

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Version is the format version this package writes and reads.
const Version = 1

// The kinds of fixture.
const (
	KindRawFraming = "raw-framing"
	KindLRP        = "lrp"
)

// Wire is one exchange as the reader's driver made it: the command bytes handed
// to the PC/SC library and the bytes it answered, in hex. A transmit that
// failed carries Error instead of a response.
type Wire struct {
	Command  string `json:"command"`
	Response string `json:"response,omitempty"`
	Error    string `json:"error,omitempty"`
}

// CommandBytes decodes Command.
func (w Wire) CommandBytes() ([]byte, error) { return decodeHex("command", w.Command) }

// ResponseBytes decodes Response.
func (w Wire) ResponseBytes() ([]byte, error) { return decodeHex("response", w.Response) }

func decodeHex(what, s string) ([]byte, error) {
	b, err := hex.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("fixture: %s %q is not hex: %w", what, s, err)
	}
	return b, nil
}

// Hex renders bytes the way fixtures hold them: upper case, no separators.
func Hex(b []byte) string { return strings.ToUpper(hex.EncodeToString(b)) }

// Raw is the capture for issue #96: framing-level (raw) exchanges with a Type 2
// tag through one reader.
type Raw struct {
	Version int    `json:"version"`
	Kind    string `json:"kind"`

	// Reader is the PC/SC reader name and ATR the card's, in hex.
	Reader string `json:"reader"`
	ATR    string `json:"atr"`

	// Method is how the reader carried a raw exchange: "acr122" (Direct
	// Transmit carrying PN532 InCommunicateThru), "part3" (a PC/SC Part 3
	// transparent session) or "none". CanTransceiveRaw is the device
	// capability the agent reported for it.
	Method           string `json:"method"`
	CanTransceiveRaw bool   `json:"canTransceiveRaw"`

	Tag RawTag `json:"tag"`

	// Setup is what the reader exchanged with the card before the first raw
	// frame: the probe, the UID and the detection reads.
	Setup []Wire `json:"setup,omitempty"`

	Exchanges []RawExchange `json:"exchanges"`

	// NDEFAfter records that a normal NDEF read still works once the raw
	// exchanges are done.
	NDEFAfter *NDEFCheck `json:"ndefAfter,omitempty"`
}

// RawTag names the tag the frames were sent to.
type RawTag struct {
	UID  string `json:"uid"`
	Type string `json:"type,omitempty"`
}

// RawExchange is one raw frame sent through the agent and what came of it.
type RawExchange struct {
	Name string `json:"name"`

	// Frame and Reply are the unwrapped bytes, as the API carries them. Reply
	// is empty and Error set for a frame that failed.
	Frame     string `json:"frame"`
	Reply     string `json:"reply,omitempty"`
	Error     string `json:"error,omitempty"`
	ErrorCode string `json:"errorCode,omitempty"`

	// Note carries an observation worth reading with the bytes.
	Note string `json:"note,omitempty"`

	// Wire is what the reader saw for this one frame: the wrapped command and
	// the reader's answer. For an ACR122 that is one exchange; for a Part 3
	// reader it is the session start, the transceive and the session end.
	Wire []Wire `json:"wire,omitempty"`
}

// NDEFCheck is the outcome of reading the tag normally after the raw frames.
type NDEFCheck struct {
	Read    bool   `json:"read"`
	Records int    `json:"records,omitempty"`
	Empty   bool   `json:"empty,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Suites a recorded LRP step ran under.
const (
	SuiteAES = "aes"
	SuiteLRP = "lrp"
)

// LRP is the capture for issue #99: an NTAG 424 DNA in LRP mode.
type LRP struct {
	Version int    `json:"version"`
	Kind    string `json:"kind"`

	Reader string `json:"reader"`
	ATR    string `json:"atr,omitempty"`
	UID    string `json:"uid"`

	// Keys are the AES keys the steps authenticated with, by key number, in
	// hex. They are present only when the run was told to include them, which
	// it must be for a replay to authenticate: use a spare tag with throwaway
	// keys, and treat the file as a secret otherwise.
	Keys map[string]string `json:"keys,omitempty"`

	Steps []LRPStep `json:"steps"`

	// URLs are the SUN URLs read back from the tag.
	URLs []SUNURL `json:"urls,omitempty"`
}

// LRPStep is one stage of the run and every APDU the reader exchanged during it.
type LRPStep struct {
	Name string `json:"name"`

	// Suite is the cipher suite the card was in for the step: SuiteAES or
	// SuiteLRP. A replay of the secure messaging follows only LRP steps.
	Suite string `json:"suite,omitempty"`

	// Error is what the step returned, when it failed or was expected to.
	Error string `json:"error,omitempty"`

	Note string `json:"note,omitempty"`

	Wire []Wire `json:"wire,omitempty"`

	// KeysAfter are keys the step changed on the card, by key number, in hex.
	// Present only with Keys.
	KeysAfter map[string]string `json:"keysAfter,omitempty"`
}

// SUNURL is a URL the tag mirrored, with the key numbers its SDM settings name,
// so it can be verified against Keys.
type SUNURL struct {
	Name string `json:"name"`
	URL  string `json:"url"`

	// MetaReadKey is the key number that encrypted PICCData, absent when the
	// tag mirrored it in the clear. FileReadKey is the key the MAC derives
	// from.
	MetaReadKey *int `json:"metaReadKey,omitempty"`
	FileReadKey int  `json:"fileReadKey"`
	LRP         bool `json:"lrp"`

	// Counter is the read counter the tag reported in the URL.
	Counter uint32 `json:"counter"`
}

// Write stores v as name.json under dir, creating dir. The file is written with
// mode 0600 because an LRP fixture made with keys holds them.
func Write(dir, name string, v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(dir, name+".json")
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		return "", err
	}
	return path, nil
}

// LoadRaw reads a raw framing fixture.
func LoadRaw(path string) (*Raw, error) {
	var f Raw
	if err := load(path, KindRawFraming, &f.Version, &f.Kind, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// LoadLRP reads an LRP fixture.
func LoadLRP(path string) (*LRP, error) {
	var f LRP
	if err := load(path, KindLRP, &f.Version, &f.Kind, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

func load(path, kind string, version *int, gotKind *string, into any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(into); err != nil {
		return fmt.Errorf("fixture %s: %w", path, err)
	}
	if *gotKind != kind {
		return fmt.Errorf("fixture %s: kind %q, want %q", path, *gotKind, kind)
	}
	if *version != Version {
		return fmt.Errorf("fixture %s: version %d, this code reads version %d", path, *version, Version)
	}
	return nil
}

// Find lists the fixtures of one kind under dir, by file name pattern
// (raw-*.json, lrp-*.json). A directory that does not exist holds none.
func Find(dir, kind string) ([]string, error) {
	prefix := map[string]string{KindRawFraming: "raw-", KindLRP: "lrp-"}[kind]
	if prefix == "" {
		return nil, fmt.Errorf("fixture: unknown kind %q", kind)
	}
	paths, err := filepath.Glob(filepath.Join(dir, prefix+"*.json"))
	if err != nil {
		return nil, err
	}
	return paths, nil
}
