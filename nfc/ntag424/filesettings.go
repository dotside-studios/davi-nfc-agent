package ntag424

import (
	"encoding/binary"
	"fmt"
)

// File settings, which is where SDM is turned on: which fields the tag mirrors,
// which keys protect them, and where each one is written as a byte offset into
// the NDEF message. A wrong offset puts the data somewhere no verifier reads.

// FileSettings is a file's configuration, as ChangeFileSettings writes it.
type FileSettings struct {
	// SDMEnabled turns mirroring on. With it false the file behaves as plain
	// storage and every SDM field below is ignored.
	SDMEnabled bool

	// CommMode is how the file's own read and write commands are protected.
	// Plain is the usual choice for an NDEF file, whose point is being readable
	// by anyone.
	CommMode CommMode

	// Access rights, each a key number 0x0 to 0x4, or AccessFree for anyone and
	// AccessNever for no one.
	ReadWrite, Change, Read, Write byte

	// What the tag mirrors.
	MirrorUID         bool
	MirrorReadCounter bool
	ReadCounterLimit  bool
	EncryptFileData   bool

	// ASCIIEncoding writes the mirrored fields as hexadecimal text rather than
	// raw bytes, which is what a URL needs.
	ASCIIEncoding bool

	// SDM access rights. MetaRead holds the key that encrypts PICCData, or
	// AccessFree to mirror it in the clear; FileRead holds the key the MAC and
	// any encrypted file data derive from; CounterRet governs reading the
	// counter back.
	SDMMetaRead, SDMFileRead, SDMCounterRet byte

	// Offsets into the NDEF message, in bytes. Which ones are read depends on
	// the flags above, and Encode writes exactly those the card expects.
	UIDOffset             uint32
	ReadCounterOffset     uint32
	PICCDataOffset        uint32
	MACInputOffset        uint32
	MACOffset             uint32
	ENCOffset             uint32
	ENCLength             uint32
	ReadCounterLimitValue uint32
}

// Access rights that name nobody rather than a key.
const (
	// AccessFree lets anyone perform the operation, with no authentication.
	AccessFree = 0x0E

	// AccessNever refuses the operation to everyone, permanently as far as this
	// setting goes.
	AccessNever = 0x0F
)

// File option bits.
const (
	fileOptionSDMEnabled = 0x40
	fileOptionCommMask   = 0x03
)

// SDM option bits.
const (
	sdmOptionUID           = 0x80
	sdmOptionReadCounter   = 0x40
	sdmOptionReadCtrLimit  = 0x20
	sdmOptionENCFileData   = 0x10
	sdmOptionASCIIEncoding = 0x01
)

// Encode renders the settings as the card stores them. The layout is positional
// and conditional: an offset is present only when the flag that uses it is set,
// so the same bytes mean different things under different flags.
func (f FileSettings) Encode() ([]byte, error) {
	comm, err := commModeBits(f.CommMode)
	if err != nil {
		return nil, err
	}

	option := comm
	if f.SDMEnabled {
		option |= fileOptionSDMEnabled
	}
	out := []byte{option}

	rights, err := accessRights(f.ReadWrite, f.Change, f.Read, f.Write)
	if err != nil {
		return nil, err
	}
	out = append(out, rights...)

	if !f.SDMEnabled {
		return out, nil
	}

	var sdm byte
	if f.MirrorUID {
		sdm |= sdmOptionUID
	}
	if f.MirrorReadCounter {
		sdm |= sdmOptionReadCounter
	}
	if f.ReadCounterLimit {
		sdm |= sdmOptionReadCtrLimit
	}
	if f.EncryptFileData {
		sdm |= sdmOptionENCFileData
	}
	if f.ASCIIEncoding {
		sdm |= sdmOptionASCIIEncoding
	}
	out = append(out, sdm)

	// The SDM rights lead with a reserved nibble rather than a key, so they are
	// packed here rather than through accessRights.
	sdmRights, err := accessRights(0x0F, f.SDMCounterRet, f.SDMMetaRead, f.SDMFileRead)
	if err != nil {
		return nil, err
	}
	out = append(out, sdmRights...)

	// The offsets, in the order the card reads them.
	if f.MirrorUID && f.SDMMetaRead == AccessFree {
		out = append(out, offset24(f.UIDOffset)...)
	}
	if f.MirrorReadCounter && f.SDMMetaRead == AccessFree {
		out = append(out, offset24(f.ReadCounterOffset)...)
	}
	if f.SDMMetaRead <= 0x04 {
		out = append(out, offset24(f.PICCDataOffset)...)
	}
	if f.EncryptFileData {
		out = append(out, offset24(f.ENCOffset)...)
		out = append(out, offset24(f.ENCLength)...)
	}
	if f.SDMFileRead != AccessNever {
		out = append(out, offset24(f.MACInputOffset)...)
		out = append(out, offset24(f.MACOffset)...)
	}
	if f.ReadCounterLimit {
		out = append(out, offset24(f.ReadCounterLimitValue)...)
	}
	return out, nil
}

// commModeBits maps a communication mode onto the two bits the file option
// carries it in.
func commModeBits(mode CommMode) (byte, error) {
	switch mode {
	case CommPlain:
		return 0x00, nil
	case CommMAC:
		return 0x01, nil
	case CommFull:
		return 0x03, nil
	default:
		return 0, fmt.Errorf("ntag424: unknown communication mode %d", mode)
	}
}

// accessRights packs four nibbles into the two bytes the card stores them in,
// in the order it reads them.
func accessRights(first, second, third, fourth byte) ([]byte, error) {
	for _, v := range []byte{first, second, third, fourth} {
		if v > 0x0F {
			return nil, fmt.Errorf("ntag424: access right %#x is not a key number", v)
		}
	}
	return []byte{first<<4 | second, third<<4 | fourth}, nil
}

// offset24 writes an offset as the card stores one: three bytes, least
// significant first.
func offset24(v uint32) []byte {
	b := make([]byte, 4)
	binary.LittleEndian.PutUint32(b, v)
	return b[:3]
}
