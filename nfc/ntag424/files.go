package ntag424

import (
	"fmt"

	"github.com/dotside-studios/davi-nfc-agent/nfc/ev2"
)

// File access and file counters. ReadData and WriteData are the native
// commands, for a file whose rights or communication mode the ISO path cannot
// serve. A reply that does not fit one frame (91 AF) is not handled here.

// Instruction bytes for the commands below.
const (
	insReadData        = 0xAD
	insWriteData       = 0x8D
	insGetFileCounters = 0xF6
)

// maxOffset24 bounds an offset or length, which the card carries in three bytes.
const maxOffset24 = 1<<24 - 1

// maxLc is the most data one ISO-wrapped command carries.
const maxLc = 255

// fileSettingsFixed is the length of the fixed part of a GetFileSettings
// answer: type, option, rights (2), size (3).
const fileSettingsFixed = 7

func dataHeader(fileNo byte, offset, length uint32) ([]byte, error) {
	if offset > maxOffset24 || length > maxOffset24 {
		return nil, fmt.Errorf("ntag424: offset %d or length %d does not fit three bytes", offset, length)
	}
	h := []byte{fileNo}
	h = append(h, offset24(offset)...)
	return append(h, offset24(length)...), nil
}

// ReadData builds the command that reads length bytes of a file from offset. A
// length of zero reads to the end of the file. mode is the file's communication
// mode.
func ReadData(s *Session, fileNo byte, offset, length uint32, mode CommMode) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("ntag424: ReadData needs an authenticated session")
	}
	header, err := dataHeader(fileNo, offset, length)
	if err != nil {
		return nil, err
	}
	return s.Command(insReadData, header, nil, mode)
}

// ReadDataPlain builds ReadData for a file read without authentication. If a
// session is open the card still counts the command, so answer it with
// ParseReadData and that session.
func ReadDataPlain(fileNo byte, offset, length uint32) ([]byte, error) {
	header, err := dataHeader(fileNo, offset, length)
	if err != nil {
		return nil, err
	}
	return ev2.WrapAPDU(insReadData, header), nil
}

// ParseReadData reads the card's answer to ReadData or ReadDataPlain. With a nil
// session only the status is checked.
func ParseReadData(s *Session, resp []byte, mode CommMode) ([]byte, error) {
	return CheckResponse(s, resp, mode)
}

// MaxWriteChunk is the most data one WriteData in this mode carries, which
// bounds the chunk a longer write is split into.
func MaxWriteChunk(mode CommMode) int {
	switch mode {
	case CommMAC:
		return maxLc - 7 - MACSize
	case CommFull:
		return (maxLc-7-MACSize)/ev2.BlockSize*ev2.BlockSize - 1
	default:
		return maxLc - 7
	}
}

func checkWriteSize(data []byte, mode CommMode) error {
	if len(data) > MaxWriteChunk(mode) {
		return fmt.Errorf("ntag424: %d bytes exceed the %d one write carries, split it", len(data), MaxWriteChunk(mode))
	}
	return nil
}

// WriteData builds the command that writes data into a file at offset. mode is
// the file's communication mode, and data longer than MaxWriteChunk(mode) is
// refused.
func WriteData(s *Session, fileNo byte, offset uint32, data []byte, mode CommMode) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("ntag424: WriteData needs an authenticated session")
	}
	if err := checkWriteSize(data, mode); err != nil {
		return nil, err
	}
	header, err := dataHeader(fileNo, offset, uint32(len(data)))
	if err != nil {
		return nil, err
	}
	return s.Command(insWriteData, header, data, mode)
}

// WriteDataPlain builds WriteData for a file written without authentication.
func WriteDataPlain(fileNo byte, offset uint32, data []byte) ([]byte, error) {
	if err := checkWriteSize(data, CommPlain); err != nil {
		return nil, err
	}
	header, err := dataHeader(fileNo, offset, uint32(len(data)))
	if err != nil {
		return nil, err
	}
	return ev2.WrapAPDU(insWriteData, append(header, data...)), nil
}

// ParseWriteData verifies the card's answer to WriteData or WriteDataPlain.
func ParseWriteData(s *Session, resp []byte, mode CommMode) error {
	_, err := CheckResponse(s, resp, mode)
	return err
}

// GetFileCounters builds the command that reads a file's SDM read counter.
func GetFileCounters(s *Session, fileNo byte) ([]byte, error) {
	if s == nil {
		return nil, fmt.Errorf("ntag424: GetFileCounters needs an authenticated session")
	}
	return s.Command(insGetFileCounters, []byte{fileNo}, nil, CommFull)
}

// ParseFileCounters reads the card's answer to GetFileCounters: the SDM read
// counter, from the first three bytes of the data.
func ParseFileCounters(s *Session, resp []byte) (uint32, error) {
	data, err := CheckResponse(s, resp, CommFull)
	if err != nil {
		return 0, err
	}
	if len(data) < counterLength {
		return 0, fmt.Errorf("ntag424: file counters are %d bytes, want at least %d", len(data), counterLength)
	}
	return decodeCounter(data[:counterLength]), nil
}

// GetFileSettingsPlain builds GetFileSettings for a card not authenticated to,
// which a file with free access allows.
func GetFileSettingsPlain(fileNo byte) []byte {
	return ev2.WrapAPDU(insGetFileSettings, []byte{fileNo})
}

// ParseFileSettingsResponse reads the card's answer to GetFileSettings or
// GetFileSettingsPlain. With a nil session only the status is checked.
func ParseFileSettingsResponse(s *Session, resp []byte) (*FileSettings, error) {
	data, err := CheckResponse(s, resp, CommMAC)
	if err != nil {
		return nil, err
	}
	return ParseFileSettings(data)
}

// ParseFileSettings reads the block GetFileSettings returns. It is Encode's
// inverse, with the file type and size the card puts in front of the settings:
// type, file option, access rights, a three-byte file size, then the SDM fields
// when SDM is on.
func ParseFileSettings(b []byte) (*FileSettings, error) {
	if len(b) < fileSettingsFixed {
		return nil, fmt.Errorf("ntag424: file settings are %d bytes, want at least %d", len(b), fileSettingsFixed)
	}
	f, err := parseSettingsBody(b[1:4])
	if err != nil {
		return nil, err
	}
	f.FileType = b[0]
	f.FileSize = uint32(b[4]) | uint32(b[5])<<8 | uint32(b[6])<<16
	if err := parseSDM(f, b[fileSettingsFixed:]); err != nil {
		return nil, err
	}
	return f, nil
}

// ParseEncodedFileSettings reads the block Encode builds, which has no file type
// or size.
func ParseEncodedFileSettings(b []byte) (*FileSettings, error) {
	if len(b) < 3 {
		return nil, fmt.Errorf("ntag424: file settings are %d bytes, want at least 3", len(b))
	}
	f, err := parseSettingsBody(b[:3])
	if err != nil {
		return nil, err
	}
	if err := parseSDM(f, b[3:]); err != nil {
		return nil, err
	}
	return f, nil
}

func parseSettingsBody(head []byte) (*FileSettings, error) {
	option := head[0]
	f := &FileSettings{SDMEnabled: option&fileOptionSDMEnabled != 0}
	switch option & fileOptionCommMask {
	case 0x00:
		f.CommMode = CommPlain
	case 0x01:
		f.CommMode = CommMAC
	case 0x03:
		f.CommMode = CommFull
	default:
		return nil, fmt.Errorf("ntag424: file option %#02x has an unknown communication mode", option)
	}
	f.ReadWrite, f.Change = head[1]>>4, head[1]&0x0F
	f.Read, f.Write = head[2]>>4, head[2]&0x0F
	return f, nil
}

func parseSDM(f *FileSettings, rest []byte) error {
	if !f.SDMEnabled {
		return nil
	}
	if len(rest) < 3 {
		return fmt.Errorf("ntag424: SDM settings are %d bytes, want at least 3", len(rest))
	}
	opts := rest[0]
	f.MirrorUID = opts&sdmOptionUID != 0
	f.MirrorReadCounter = opts&sdmOptionReadCounter != 0
	f.ReadCounterLimit = opts&sdmOptionReadCtrLimit != 0
	f.EncryptFileData = opts&sdmOptionENCFileData != 0
	f.ASCIIEncoding = opts&sdmOptionASCIIEncoding != 0
	f.SDMCounterRet = rest[1] & 0x0F
	f.SDMMetaRead, f.SDMFileRead = rest[2]>>4, rest[2]&0x0F
	rest = rest[3:]

	steps := []struct {
		present bool
		dst     *uint32
	}{
		{f.MirrorUID && f.SDMMetaRead == AccessFree, &f.UIDOffset},
		{f.MirrorReadCounter && f.SDMMetaRead == AccessFree, &f.ReadCounterOffset},
		{f.SDMMetaRead <= 0x04, &f.PICCDataOffset},
		{f.SDMFileRead != AccessNever, &f.MACInputOffset},
		{f.EncryptFileData, &f.ENCOffset},
		{f.EncryptFileData, &f.ENCLength},
		{f.SDMFileRead != AccessNever, &f.MACOffset},
		{f.ReadCounterLimit, &f.ReadCounterLimitValue},
	}
	for _, step := range steps {
		if !step.present {
			continue
		}
		if len(rest) < 3 {
			return fmt.Errorf("ntag424: SDM settings end before an offset")
		}
		*step.dst = uint32(rest[0]) | uint32(rest[1])<<8 | uint32(rest[2])<<16
		rest = rest[3:]
	}
	if len(rest) != 0 {
		return fmt.Errorf("ntag424: %d unexpected bytes after the SDM settings", len(rest))
	}
	return nil
}
