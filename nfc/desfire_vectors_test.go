package nfc

import (
	"bytes"
	"fmt"
	"testing"
)

// Differential vectors for the DESFire commands this driver builds and the
// version fields it reads, taken from implementations that have been run
// against real cards for years:
//
//   - libfreefare, libfreefare/mifare_desfire.c and mifare_desfire_aid.c
//     (github.com/nfc-tools/libfreefare)
//   - the Proxmark3 client, client/src/mifare/desfirecore.c and
//     client/src/cmdhfmfdes.c (github.com/RfidResearchGroup/proxmark3)
//   - nfcutils, src/lsnfc.c (github.com/nfc-tools/nfcutils)
//
// The emulator in nfctest answers whatever this driver sends, so it cannot
// catch a command that is well-formed and wrong. These can: they say what the
// bytes are, sourced from somewhere other than this package. The first of them
// found an application identifier sent in the wrong byte order, which three
// rounds of work on this driver had gone past.
//
// libfreefare speaks the native protocol over libnfc, so its command buffers
// carry no ISO wrapper. The expectations below add the 90 xx 00 00 Lc … 00
// envelope this driver sends over PC/SC, which is the only difference.
//
// Not covered: the chunking in tag_desfire_session.go, which is this driver's
// own choice rather than a protocol claim. libfreefare chains a long transfer
// across additional frames; this driver addresses each chunk by offset instead,
// to keep every command a whole MACed exchange. What can be checked is that the
// chunks fit what the card says it accepts, which is below.

func TestDESFireCommandVectors(t *testing.T) {
	tests := []struct {
		name string
		got  []byte
		want []byte
		// source names where the expectation comes from.
		source string
	}{
		{
			name: "SelectApplication, the NDEF application",
			got:  DESFireSelectAppAPDU(dfNDEFAppAID),
			// 5A | AID least significant byte first. libfreefare builds the AID
			// with htole32 and copies three bytes, so 0x000001 is 01 00 00;
			// Proxmark3's DesfireAIDUintToByte writes data[0] = aid & 0xff.
			want:   []byte{0x90, 0x5A, 0x00, 0x00, 0x03, 0x01, 0x00, 0x00, 0x00},
			source: "mifare_desfire.c select_application, mifare_desfire_aid.c aid_new; desfirecore.c DesfireAIDUintToByte",
		},
		{
			name: "GetVersion",
			got:  DESFireWrapAPDU(DFCmdGetVersion, nil),
			// 0x60 with no arguments; the first frame carries seven bytes of
			// hardware information.
			want:   []byte{0x90, 0x60, 0x00, 0x00, 0x00},
			source: "mifare_desfire.c mifare_desfire_get_version",
		},
		{
			name: "GetFileSettings, the NDEF file",
			got:  DESFireGetFileSettingsAPDU(dfNDEFFileNo),
			// F5 | file number.
			want:   []byte{0x90, 0xF5, 0x00, 0x00, 0x01, 0x02, 0x00},
			source: "mifare_desfire.c mifare_desfire_get_file_settings",
		},
		{
			name: "ReadData, two bytes at offset zero",
			got:  DESFireReadDataAPDU(dfNDEFFileNo, 0, 2),
			// BD | file | offset (3, LSB first) | length (3, LSB first).
			want:   []byte{0x90, 0xBD, 0x00, 0x00, 0x07, 0x02, 0x00, 0x00, 0x00, 0x02, 0x00, 0x00, 0x00},
			source: "mifare_desfire.c read_data",
		},
		{
			name: "ReadData, a length and offset past one byte",
			got:  DESFireReadDataAPDU(dfNDEFFileNo, 0x000102, 0x000304),
			want: []byte{0x90, 0xBD, 0x00, 0x00, 0x07, 0x02,
				0x02, 0x01, 0x00, // offset 0x000102
				0x04, 0x03, 0x00, // length 0x000304
				0x00},
			source: "mifare_desfire.c read_data",
		},
		{
			name: "WriteData, the NLEN prefix",
			got:  DESFireWriteDataAPDU(dfNDEFFileNo, 0, []byte{0x00, 0x09}),
			// 3D | file | offset (3) | length (3) | data.
			want: []byte{0x90, 0x3D, 0x00, 0x00, 0x09, 0x02,
				0x00, 0x00, 0x00, // offset 0
				0x02, 0x00, 0x00, // length 2
				0x00, 0x09,
				0x00},
			source: "mifare_desfire.c write_data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !bytes.Equal(tt.got, tt.want) {
				t.Errorf("built  % X\nwant   % X\nsource %s", tt.got, tt.want, tt.source)
			}
		})
	}
}

// The header a session-protected read or write carries in the clear is the same
// seven bytes, with the instruction held separately by the session.
//
// libfreefare encrypts a write from offset 8 of its buffer, which is the
// instruction plus these seven, so the split matches.
func TestDESFireFileArgsVector(t *testing.T) {
	got := fileArgs(dfNDEFFileNo, 0x000102, 0x000304)
	want := []byte{0x02, 0x02, 0x01, 0x00, 0x04, 0x03, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("fileArgs = % X, want % X (mifare_desfire.c write_data, preprocess offset 8)", got, want)
	}
}

// Access rights are four nibbles in a 16-bit value, read at the top and change
// at the bottom, travelling least significant byte first.
//
// libfreefare packs them with MDAR(read, write, read_write, change) and reads
// them back with MDAR_READ(ar) = ar >> 12 down to MDAR_CHANGE_AR(ar) = ar & 0xF,
// appending the value little-endian.
func TestDESFireAccessRightsVectors(t *testing.T) {
	tests := []struct {
		name     string
		settings desfireFileSettings
		// wire is the two bytes as they travel, least significant first.
		wire []byte
	}{
		{
			// The NDEF file libfreefare creates: MDAR(E, E, E, 0) = 0xEEE0.
			name:     "NDEF file, free to read and write, change behind key 0",
			settings: desfireFileSettings{read: 0x0E, write: 0x0E, readWrite: 0x0E, change: 0x00},
			wire:     []byte{0xE0, 0xEE},
		},
		{
			// The Capability Container file: MDAR(E, 0, 0, 0) = 0xE000.
			name:     "CC file, free to read, everything else behind key 0",
			settings: desfireFileSettings{read: 0x0E, write: 0x00, readWrite: 0x00, change: 0x00},
			wire:     []byte{0x00, 0xE0},
		},
		{
			// What MakeReadOnly writes: reading stays free, nothing else is
			// permitted and nothing may change that.
			name:     "locked, read free and the rest denied",
			settings: desfireFileSettings{read: 0x0E, write: 0x0F, readWrite: 0x0F, change: 0x0F},
			wire:     []byte{0xFF, 0xEF},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := encodeAccessRights(tt.settings); !bytes.Equal(got, tt.wire) {
				t.Errorf("encodeAccessRights = % X, want % X", got, tt.wire)
			}

			// And the same bytes read back, which is how GetFileSettings
			// delivers them.
			response := append([]byte{dfFileTypeStdData, dfCommPlain}, tt.wire...)
			response = append(response, 0x00, 0x01, 0x00) // 256-byte file
			parsed, ok := parseDESFireFileSettings(response)
			if !ok {
				t.Fatal("parseDESFireFileSettings refused a well-formed response")
			}
			if parsed.read != tt.settings.read || parsed.write != tt.settings.write ||
				parsed.readWrite != tt.settings.readWrite || parsed.change != tt.settings.change {
				t.Errorf("round trip: read=%X write=%X rw=%X change=%X, want %X %X %X %X",
					parsed.read, parsed.write, parsed.readWrite, parsed.change,
					tt.settings.read, tt.settings.write, tt.settings.readWrite, tt.settings.change)
			}
			if parsed.size != 256 {
				t.Errorf("size = %d, want 256 (three bytes, least significant first)", parsed.size)
			}
		})
	}
}

// The Capability Container an NDEF-formatted DESFire carries, as libfreefare
// writes it for mapping version 2.0. It is the source of two figures this
// driver depends on: MLe, the most a read returns, and MLc, the most a write
// carries.
func TestDESFireCapabilityContainerFrameSizes(t *testing.T) {
	cc := []byte{
		0x00, 0x0F, // CCLEN
		0x20,       // mapping version 2.0
		0x00, 0x3B, // MLe
		0x00, 0x34, // MLc
		0x04, 0x06, 0xE1, 0x04, 0x01, 0x00, 0x00, 0x00, // NDEF File Control TLV
	}

	mle := int(cc[3])<<8 | int(cc[4])
	mlc := int(cc[5])<<8 | int(cc[6])

	if mle != dfFrameData {
		t.Errorf("MLe = %d but dfFrameData = %d; the frame size this driver assumes is not the one the card advertises", mle, dfFrameData)
	}
	// A write spends seven of those bytes on the command header.
	if want := dfFrameData - 7; mlc != want {
		t.Errorf("MLc = %d, want %d (the frame less the command header)", mlc, want)
	}
	// Both session chunk sizes have to fit what a write may carry.
	if dfSessionChunk > mlc || dfSessionChunkFull > mlc {
		t.Errorf("session chunks %d and %d exceed MLc %d", dfSessionChunk, dfSessionChunkFull, mlc)
	}
}

// The hardware version a DESFire reports, and what it says about the card.
//
// Proxmark3's getCardType matches on product type 0x01 with a major version:
// 0x01 is an EV1, 0x12 an EV2, 0x22 an EV2 XL, 0x33 an EV3 and 0xA0 a DUOX.
// This driver names the three it drives and leaves the rest as a plain DESFire,
// which is the safe reading: an unnamed generation still speaks the commands
// they share.
func TestDESFireVersionVectors(t *testing.T) {
	// vendor, product, subtype, major, minor, storage, protocol, then 91 AF.
	frame := func(major, storage byte) []byte {
		return []byte{0x04, 0x01, 0x01, major, 0x00, storage, 0x05, 0x91, 0xAF}
	}

	tests := []struct {
		name    string
		major   byte
		storage byte
		want    DetectedTagType
		source  string
	}{
		{"EV1", 0x01, 0x18, DetectedDESFireEV1, "desfirecore.c getCardType: type 01, major 01"},
		{"EV2", 0x12, 0x1A, DetectedDESFireEV2, "desfirecore.c getCardType: type 01, major 12"},
		{"EV3", 0x33, 0x1A, DetectedDESFireEV3, "desfirecore.c getCardType: type 01, major 33"},
		{"EV2 XL, which this driver does not name", 0x22, 0x1C, DetectedDESFire,
			"desfirecore.c getCardType names major 22 an EV2 XL; here it falls back"},
		{"DUOX, which this driver does not name", 0xA0, 0x1A, DetectedDESFire,
			"desfirecore.c getCardType names major A0 a DUOX; here it falls back"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version, ok := ParseWrappedVersion(frame(tt.major, tt.storage))
			if !ok {
				t.Fatal("ParseWrappedVersion refused a well-formed frame")
			}
			if got := version.Kind(); got != tt.want {
				t.Errorf("Kind() = %v, want %v (%s)", got, tt.want, tt.source)
			}
		})
	}
}

// The storage byte is an exponent: the size in bytes is 1 << (byte >> 1).
//
// nfc-tools/nfcutils prints it as 1 << ((version[5] >> 1) - 10) kilobytes,
// which is the same figure divided by 1024, and reads the byte at the same
// offset this parser does.
func TestDESFireStorageSizeVectors(t *testing.T) {
	tests := []struct {
		storage byte
		want    int
	}{
		{0x16, 2 * 1024},
		{0x18, 4 * 1024},
		{0x1A, 8 * 1024},
		{0x1C, 16 * 1024},
		{0x1E, 32 * 1024},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%#02x", tt.storage), func(t *testing.T) {
			version := WrappedVersion{StorageSize: tt.storage}
			if got := version.MemorySize(); got != tt.want {
				t.Errorf("MemorySize() = %d, want %d (lsnfc.c mifare_desfire_identification)", got, tt.want)
			}
			// The same arithmetic the reference does, in kilobytes.
			if kb := 1 << ((int(tt.storage) >> 1) - 10); kb*1024 != tt.want {
				t.Errorf("reference formula gives %dk, want %d bytes", kb, tt.want)
			}
		})
	}
}

// A storage byte encoding no size at all leaves the memory unreported rather
// than producing a number from nothing.
func TestDESFireStorageSizeRefusesNonsense(t *testing.T) {
	for _, storage := range []byte{0x00, 0x01} {
		if got := (WrappedVersion{StorageSize: storage}).MemorySize(); got != 0 {
			t.Errorf("MemorySize(%#02x) = %d, want 0", storage, got)
		}
	}
}
