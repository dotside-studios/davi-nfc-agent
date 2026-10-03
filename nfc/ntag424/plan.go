package ntag424

import (
	"fmt"
	"strings"
)

// Planning an SDM configuration from a URL template, so a caller names where
// the mirrors go in the URL and gets the NDEF message and file settings that
// put them there. Offsets are the part that goes wrong by hand.

// Placeholders PlanSDM recognises in a template.
const (
	PlaceholderPICC = "{picc}"
	PlaceholderUID  = "{uid}"
	PlaceholderCtr  = "{ctr}"
	PlaceholderEnc  = "{enc}"
	PlaceholderMAC  = "{mac}"
)

// Widths of each mirror in the NDEF message, in ASCII characters.
const (
	piccMirrorChars = 2 * PICCDataSize
	uidMirrorChars  = 2 * uidLength
	ctrMirrorChars  = 2 * counterLength
	macMirrorChars  = 2 * MACSize
	encBlockChars   = 2 * 16
)

// NDEFFileSize is the size of the NDEF file, NLEN included, which bounds the
// message a plan can hold.
const NDEFFileSize = 256

// SDMOptions are the choices a template does not carry: which keys protect the
// file and each mirror. A zero key number is key 0, so set each one.
type SDMOptions struct {
	// MetaRead is the key that encrypts {picc}. It is ignored for {uid} and
	// {ctr}, which are mirrored in the clear.
	MetaRead byte

	// FileRead is the key the MAC and {enc} derive from. It must be a key
	// number 0 to 4.
	FileRead byte

	// CounterRet is the key that may read the counter back.
	CounterRet byte

	// Access rights of the file itself.
	Change, Read, Write, ReadWrite byte

	// EncLength is the width of {enc} in mirrored characters: a multiple of 32,
	// which is 16 bytes of file data per 32 characters. Zero means 32.
	EncLength uint32
}

// SDMPlan is an NDEF message and the file settings that make a tag mirror into
// it.
type SDMPlan struct {
	// NDEF is the whole NDEF file content: NLEN, then one URI record with every
	// mirror as ASCII '0' characters of its final width.
	NDEF []byte

	// Settings are the file settings to write with ChangeFileSettings. Their
	// offsets count from the start of NDEF, so the first record byte is at 2.
	Settings FileSettings
}

type placeholder struct {
	name  string
	width int
}

var placeholders = []placeholder{
	{PlaceholderPICC, piccMirrorChars},
	{PlaceholderUID, uidMirrorChars},
	{PlaceholderCtr, ctrMirrorChars},
	{PlaceholderEnc, 0},
	{PlaceholderMAC, macMirrorChars},
}

var uriPrefixes = []struct {
	code   byte
	prefix string
}{
	{0x02, "https://www."},
	{0x01, "http://www."},
	{0x04, "https://"},
	{0x03, "http://"},
}

// PlanSDM lays out an SDM URL.
//
// The template is the URL with {picc} (encrypted PICCData), or {uid} and {ctr}
// (the same in the clear), optionally {enc} (encrypted file data) and always
// {mac}, each at most once. Where it matters, they must appear in this order:
// PICCData or UID and counter, then {enc}, then {mac}.
//
// The MAC covers the URL from the start of {enc} to the start of {mac}, or
// nothing when there is no {enc}, which is the input VerifyURL checks it
// against.
func PlanSDM(urlTemplate string, opts SDMOptions) (*SDMPlan, error) {
	text, pos, err := expandTemplate(urlTemplate, opts)
	if err != nil {
		return nil, err
	}

	_, hasPICC := pos[PlaceholderPICC]
	_, hasUID := pos[PlaceholderUID]
	_, hasCtr := pos[PlaceholderCtr]
	_, hasEnc := pos[PlaceholderEnc]
	macPos, hasMAC := pos[PlaceholderMAC]

	switch {
	case !hasMAC:
		return nil, fmt.Errorf("ntag424: the template needs %s", PlaceholderMAC)
	case hasPICC && (hasUID || hasCtr):
		return nil, fmt.Errorf("ntag424: %s cannot be combined with %s or %s", PlaceholderPICC, PlaceholderUID, PlaceholderCtr)
	case hasUID != hasCtr:
		return nil, fmt.Errorf("ntag424: %s and %s go together, a MAC cannot be checked without both", PlaceholderUID, PlaceholderCtr)
	case !hasPICC && !hasUID:
		return nil, fmt.Errorf("ntag424: the template needs %s, or %s and %s", PlaceholderPICC, PlaceholderUID, PlaceholderCtr)
	case opts.FileRead > 0x04:
		return nil, fmt.Errorf("ntag424: FileRead %#x is not a key number", opts.FileRead)
	case hasPICC && opts.MetaRead > 0x04:
		return nil, fmt.Errorf("ntag424: MetaRead %#x is not a key number", opts.MetaRead)
	}

	encLength := opts.EncLength
	if encLength == 0 {
		encLength = encBlockChars
	}
	if hasEnc && encLength%encBlockChars != 0 {
		return nil, fmt.Errorf("ntag424: EncLength %d is not a multiple of %d", encLength, encBlockChars)
	}

	prefixCode, prefix := byte(0x00), ""
	for _, p := range uriPrefixes {
		if strings.HasPrefix(text, p.prefix) {
			prefixCode, prefix = p.code, p.prefix
			break
		}
	}
	body := text[len(prefix):]
	payloadLen := 1 + len(body)
	if 2+4+payloadLen > NDEFFileSize {
		return nil, fmt.Errorf("ntag424: the NDEF message is %d bytes, the file holds %d", 2+4+payloadLen, NDEFFileSize)
	}
	record := []byte{0xD1, 0x01, byte(payloadLen), 'U', prefixCode}
	textStart := 2 + len(record) - len(prefix)
	record = append(record, body...)
	ndef := append([]byte{byte(len(record) >> 8), byte(len(record))}, record...)

	at := func(name string) uint32 { return uint32(textStart + pos[name]) }

	settings := FileSettings{
		SDMEnabled:    true,
		CommMode:      CommPlain,
		ReadWrite:     opts.ReadWrite,
		Change:        opts.Change,
		Read:          opts.Read,
		Write:         opts.Write,
		ASCIIEncoding: true,
		SDMFileRead:   opts.FileRead,
		SDMCounterRet: opts.CounterRet,
		MACOffset:     at(PlaceholderMAC),
	}
	var firstMirrorEnd int
	if hasPICC {
		settings.MirrorUID, settings.MirrorReadCounter = true, true
		settings.SDMMetaRead = opts.MetaRead
		settings.PICCDataOffset = at(PlaceholderPICC)
		firstMirrorEnd = pos[PlaceholderPICC] + piccMirrorChars
	} else {
		settings.MirrorUID, settings.MirrorReadCounter = true, true
		settings.SDMMetaRead = AccessFree
		settings.UIDOffset = at(PlaceholderUID)
		settings.ReadCounterOffset = at(PlaceholderCtr)
		firstMirrorEnd = max(pos[PlaceholderUID]+uidMirrorChars, pos[PlaceholderCtr]+ctrMirrorChars)
	}

	if hasEnc {
		encPos := pos[PlaceholderEnc]
		if encPos < firstMirrorEnd {
			return nil, fmt.Errorf("ntag424: %s must come after the PICCData or UID and counter mirrors", PlaceholderEnc)
		}
		if macPos < encPos+int(encLength) {
			return nil, fmt.Errorf("ntag424: %s must come after %s", PlaceholderMAC, PlaceholderEnc)
		}
		settings.EncryptFileData = true
		settings.ENCOffset = at(PlaceholderEnc)
		settings.ENCLength = encLength
		settings.MACInputOffset = at(PlaceholderEnc)
	} else {
		if macPos < firstMirrorEnd {
			return nil, fmt.Errorf("ntag424: %s must come after the PICCData or UID and counter mirrors", PlaceholderMAC)
		}
		settings.MACInputOffset = settings.MACOffset
	}

	if _, err := settings.Encode(); err != nil {
		return nil, err
	}
	return &SDMPlan{NDEF: ndef, Settings: settings}, nil
}

// expandTemplate replaces each placeholder with '0' characters of its width and
// reports where each one landed in the result.
func expandTemplate(tmpl string, opts SDMOptions) (string, map[string]int, error) {
	encWidth := int(opts.EncLength)
	if encWidth == 0 {
		encWidth = encBlockChars
	}

	var out strings.Builder
	pos := make(map[string]int)
	rest := tmpl
	for {
		i := strings.IndexByte(rest, '{')
		if i < 0 {
			out.WriteString(rest)
			return out.String(), pos, nil
		}
		out.WriteString(rest[:i])
		rest = rest[i:]

		var matched *placeholder
		for k := range placeholders {
			if strings.HasPrefix(rest, placeholders[k].name) {
				matched = &placeholders[k]
				break
			}
		}
		if matched == nil {
			return "", nil, fmt.Errorf("ntag424: unknown placeholder in %q", rest)
		}
		if _, dup := pos[matched.name]; dup {
			return "", nil, fmt.Errorf("ntag424: %s appears twice", matched.name)
		}
		width := matched.width
		if matched.name == PlaceholderEnc {
			width = encWidth
		}
		pos[matched.name] = out.Len()
		out.WriteString(strings.Repeat("0", width))
		rest = rest[len(matched.name):]
	}
}
