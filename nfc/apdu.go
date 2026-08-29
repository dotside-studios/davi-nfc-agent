package nfc

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// APDU status words
const (
	SW1Success     = 0x90
	SW2Success     = 0x00
	SW1MoreData    = 0x61 // More data available
	SW1WrongLength = 0x6C // Wrong Le field
)

// Common APDU command classes
const (
	CLAStandard   = 0x00 // Standard ISO7816-4
	CLAPCSC       = 0xFF // PC/SC pseudo-APDU (reader commands)
	CLADESFire    = 0x90 // DESFire native command wrapper
	CLAProprietry = 0x80 // Proprietary commands
)

// PC/SC pseudo-APDU instructions
const (
	INSGetUID     = 0xCA // Get UID
	INSLoadKey    = 0x82 // Load authentication key
	INSAuth       = 0x86 // General authenticate
	INSReadBinary = 0xB0 // Read binary
	INSUpdateBin  = 0xD6 // Update binary
	INSDirectCmd  = 0x00 // Direct transmit (for wrapped commands)
	INSSelectFile = 0xA4 // Select file
)

// MIFARE key types
const (
	MIFAREKeyA = 0x60
	MIFAREKeyB = 0x61
)

// APDUResponse represents a parsed APDU response
type APDUResponse struct {
	Data []byte
	SW1  byte
	SW2  byte
}

// IsSuccess returns true if the response indicates success (SW1=90, SW2=00)
func (r APDUResponse) IsSuccess() bool {
	return r.SW1 == SW1Success && r.SW2 == SW2Success
}

// HasMoreData returns true if more data is available (SW1=61)
func (r APDUResponse) HasMoreData() bool {
	return r.SW1 == SW1MoreData
}

// Error returns an error if the response is not successful
func (r APDUResponse) Error() error {
	if r.IsSuccess() || r.HasMoreData() {
		return nil
	}
	return fmt.Errorf("APDU error: SW1=%02X SW2=%02X", r.SW1, r.SW2)
}

// StatusWord returns the 2-byte status word as uint16
func (r APDUResponse) StatusWord() uint16 {
	return uint16(r.SW1)<<8 | uint16(r.SW2)
}

// ParseAPDUResponse parses a raw response into APDUResponse
func ParseAPDUResponse(raw []byte) (APDUResponse, error) {
	if len(raw) < 2 {
		return APDUResponse{}, errors.New("response too short")
	}
	return APDUResponse{
		Data: raw[:len(raw)-2],
		SW1:  raw[len(raw)-2],
		SW2:  raw[len(raw)-1],
	}, nil
}

// BuildAPDU constructs an APDU command
func BuildAPDU(cla, ins, p1, p2 byte, data []byte, le *byte) []byte {
	cmd := []byte{cla, ins, p1, p2}

	if len(data) > 0 {
		cmd = append(cmd, byte(len(data)))
		cmd = append(cmd, data...)
	}

	if le != nil {
		cmd = append(cmd, *le)
	}

	return cmd
}

// GetUIDAPDU returns the APDU for getting the card UID
func GetUIDAPDU() []byte {
	le := byte(0x00)
	return BuildAPDU(CLAPCSC, INSGetUID, 0x00, 0x00, nil, &le)
}

// LoadKeyAPDU returns the APDU for loading a key into reader memory
// keySlot: 0x00-0x1F for volatile, 0x20+ for non-volatile
func LoadKeyAPDU(keySlot byte, key []byte) []byte {
	if len(key) != 6 {
		return nil
	}
	return BuildAPDU(CLAPCSC, INSLoadKey, 0x00, keySlot, key, nil)
}

// MIFAREAuthAPDU returns the APDU for MIFARE authentication
// block: block number to authenticate
// keyType: MIFAREKeyA (0x60) or MIFAREKeyB (0x61)
// keySlot: slot where key was loaded
func MIFAREAuthAPDU(block byte, keyType byte, keySlot byte) []byte {
	// Authentication data block structure:
	// Version (1) | 0x00 | Block (1) | Key Type (1) | Key Number (1)
	data := []byte{0x01, 0x00, block, keyType, keySlot}
	return BuildAPDU(CLAPCSC, INSAuth, 0x00, 0x00, data, nil)
}

// ReadBinaryAPDU returns the APDU for reading binary data
// For MIFARE: block/page number in P2, length in Le
func ReadBinaryAPDU(offset byte, length byte) []byte {
	return BuildAPDU(CLAPCSC, INSReadBinary, 0x00, offset, nil, &length)
}

// UpdateBinaryAPDU returns the APDU for writing binary data
// For MIFARE: block/page number in P2
func UpdateBinaryAPDU(offset byte, data []byte) []byte {
	return BuildAPDU(CLAPCSC, INSUpdateBin, 0x00, offset, data, nil)
}

// DirectTransmitAPDU wraps a command for direct transmission to the card
// Used for native commands (e.g., Ultralight READ, WRITE)
func DirectTransmitAPDU(cmd []byte) []byte {
	// For ACR122U and similar: FF 00 00 00 Lc [data] Le
	le := byte(0x00)
	return BuildAPDU(CLAPCSC, INSDirectCmd, 0x00, 0x00, cmd, &le)
}

// SelectFileAPDU returns the APDU for selecting an elementary file by its file
// identifier: 00 A4 00 0C Lc <fid>, with P1=0x00 (select by EF identifier) and
// P2=0x0C (no FCI in the response), which is the form NFC Forum Type 4 tags
// require to select the CC and NDEF files. There is no Le: the select returns
// only a status word.
//
// This is distinct from selecting an application, which is by DF name; see
// SelectFileByAIDAPDU. Selecting an EF with the by-name P1 (0x04) is what a
// compliant tag answers with 6A82.
func SelectFileAPDU(fid []byte) []byte {
	return BuildAPDU(CLAStandard, INSSelectFile, 0x00, 0x0C, fid, nil)
}

// SelectFileByAIDAPDU returns the APDU for selecting application by AID
func SelectFileByAIDAPDU(aid []byte) []byte {
	le := byte(0x00)
	return BuildAPDU(CLAStandard, INSSelectFile, 0x04, 0x00, aid, &le)
}

// ReadBinaryExtAPDU returns an extended APDU for reading with 2-byte offset
func ReadBinaryExtAPDU(offset uint16, length byte) []byte {
	p1 := byte((offset >> 8) & 0x7F) // High byte (bit 7 must be 0)
	p2 := byte(offset & 0xFF)        // Low byte
	return BuildAPDU(CLAStandard, INSReadBinary, p1, p2, nil, &length)
}

// UpdateBinaryExtAPDU returns an extended APDU for writing with 2-byte offset
func UpdateBinaryExtAPDU(offset uint16, data []byte) []byte {
	p1 := byte((offset >> 8) & 0x7F)
	p2 := byte(offset & 0xFF)
	return BuildAPDU(CLAStandard, INSUpdateBin, p1, p2, data, nil)
}

// GetVersionAPDU returns the APDU for getting NTAG/Ultralight version
// This is wrapped in a direct transmit command
func GetVersionAPDU() []byte {
	// NTAG GET_VERSION command: 0x60
	return DirectTransmitAPDU([]byte{0x60})
}

// UltralightReadAPDU returns the native Ultralight READ command (wrapped)
// Reads 4 pages (16 bytes) starting from the specified page
func UltralightReadAPDU(page byte) []byte {
	// Ultralight READ command: 0x30 [page]
	return DirectTransmitAPDU([]byte{0x30, page})
}

// UltralightWriteAPDU returns the native Ultralight WRITE command (wrapped)
// Writes 4 bytes to the specified page
func UltralightWriteAPDU(page byte, data []byte) []byte {
	if len(data) != 4 {
		return nil
	}
	// Ultralight WRITE command: 0xA2 [page] [4 bytes]
	cmd := append([]byte{0xA2, page}, data...)
	return DirectTransmitAPDU(cmd)
}

// DESFire command helpers

// DESFireWrapAPDU wraps a DESFire native command in ISO7816 APDU
func DESFireWrapAPDU(cmd byte, data []byte) []byte {
	le := byte(0x00)
	return BuildAPDU(CLADESFire, cmd, 0x00, 0x00, data, &le)
}

// DESFire native command codes
const (
	DFCmdSelectApplication = 0x5A
	DFCmdGetApplicationIDs = 0x6A
	DFCmdGetFileIDs        = 0x6F
	DFCmdReadData          = 0xBD
	DFCmdWriteData         = 0x3D
	DFCmdAuthenticate      = 0x0A // Legacy DES auth
	DFCmdAuthenticateISO   = 0x1A // 3DES auth
	DFCmdAuthenticateAES   = 0xAA // AES auth
	DFCmdGetVersion        = 0x60
	DFCmdAdditionalFrame   = 0xAF
)

// DESFireSelectAppAPDU returns APDU for selecting a DESFire application
func DESFireSelectAppAPDU(aid []byte) []byte {
	if len(aid) != 3 {
		return nil
	}
	return DESFireWrapAPDU(DFCmdSelectApplication, aid)
}

// DESFireGetAppIDsAPDU returns APDU for listing application IDs
func DESFireGetAppIDsAPDU() []byte {
	return DESFireWrapAPDU(DFCmdGetApplicationIDs, nil)
}

// DESFireGetFileIDsAPDU returns APDU for listing file IDs
func DESFireGetFileIDsAPDU() []byte {
	return DESFireWrapAPDU(DFCmdGetFileIDs, nil)
}

// DESFireReadDataAPDU returns APDU for reading file data
func DESFireReadDataAPDU(fileNo byte, offset uint32, length uint32) []byte {
	data := make([]byte, 7)
	data[0] = fileNo
	// Offset and length are 3 bytes each, little-endian
	data[1] = byte(offset)
	data[2] = byte(offset >> 8)
	data[3] = byte(offset >> 16)
	data[4] = byte(length)
	data[5] = byte(length >> 8)
	data[6] = byte(length >> 16)
	return DESFireWrapAPDU(DFCmdReadData, data)
}

// DESFireWriteDataAPDU returns APDU for writing file data
func DESFireWriteDataAPDU(fileNo byte, offset uint32, writeData []byte) []byte {
	length := uint32(len(writeData))
	header := make([]byte, 7)
	header[0] = fileNo
	header[1] = byte(offset)
	header[2] = byte(offset >> 8)
	header[3] = byte(offset >> 16)
	header[4] = byte(length)
	header[5] = byte(length >> 8)
	header[6] = byte(length >> 16)
	data := append(header, writeData...)
	return DESFireWrapAPDU(DFCmdWriteData, data)
}

// DESFireAuthAPDU returns APDU for authentication
func DESFireAuthAPDU(keyNo byte, authType byte) []byte {
	return DESFireWrapAPDU(authType, []byte{keyNo})
}

// DESFireAdditionalFrameAPDU returns APDU for sending additional frame data
func DESFireAdditionalFrameAPDU(data []byte) []byte {
	return DESFireWrapAPDU(DFCmdAdditionalFrame, data)
}

// Utility functions

// BytesToHex converts bytes to uppercase hex string
func BytesToHex(data []byte) string {
	const hexChars = "0123456789ABCDEF"
	result := make([]byte, len(data)*2)
	for i, b := range data {
		result[i*2] = hexChars[b>>4]
		result[i*2+1] = hexChars[b&0x0F]
	}
	return string(result)
}

// HexToBytes converts a hex string to bytes
func HexToBytes(hex string) ([]byte, error) {
	if len(hex)%2 != 0 {
		return nil, errors.New("hex string must have even length")
	}
	result := make([]byte, len(hex)/2)
	for i := 0; i < len(hex); i += 2 {
		var b byte
		for j := 0; j < 2; j++ {
			c := hex[i+j]
			b <<= 4
			switch {
			case c >= '0' && c <= '9':
				b |= c - '0'
			case c >= 'A' && c <= 'F':
				b |= c - 'A' + 10
			case c >= 'a' && c <= 'f':
				b |= c - 'a' + 10
			default:
				return nil, fmt.Errorf("invalid hex character: %c", c)
			}
		}
		result[i/2] = b
	}
	return result, nil
}

// Uint16ToBytes converts uint16 to big-endian bytes
func Uint16ToBytes(v uint16) []byte {
	b := make([]byte, 2)
	binary.BigEndian.PutUint16(b, v)
	return b
}

// BytesToUint16 converts big-endian bytes to uint16
func BytesToUint16(b []byte) uint16 {
	if len(b) < 2 {
		return 0
	}
	return binary.BigEndian.Uint16(b)
}
