package nfc

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

// Selecting an elementary file (the CC or NDEF file on a Type 4 tag) is by file
// identifier — P1=0x00, P2=0x0C, no Le — not by DF name. The two SELECT builders
// must not collapse into the same P1: selecting an EF with the by-name P1 is
// what a compliant tag answers with 6A82.
func TestSelectFileAPDUUsesEFSelectForm(t *testing.T) {
	got := SelectFileAPDU([]byte{0xE1, 0x03})
	want := []byte{0x00, 0xA4, 0x00, 0x0C, 0x02, 0xE1, 0x03}
	if !bytes.Equal(got, want) {
		t.Errorf("SelectFileAPDU(E103) = % X, want % X (P1=00 select-by-EF-id, P2=0C no FCI, no Le)", got, want)
	}

	// The application select stays by DF name (P1=04), which is correct for an AID.
	app := SelectFileByAIDAPDU(ndefAppAID)
	if app[2] != 0x04 {
		t.Errorf("SelectFileByAIDAPDU P1 = %02X, want 04 (select by DF name)", app[2])
	}
	if got[2] == app[2] {
		t.Error("the EF select and the AID select use the same P1; they must differ")
	}
}

// ExampleExplain shows what a raw exchange decodes to: the command, its class in
// brackets, and the cautions a safety net should surface. Here a write lands on
// a page that holds an NTAG's lock and OTP bytes.
func ExampleExplain() {
	cmd := []byte{0xFF, 0xD6, 0x00, 0x03, 0x04, 0xDE, 0xAD, 0xBE, 0xEF}
	fmt.Println(Explain(cmd, false))
	// Output:
	// UPDATE BINARY — write 4 byte(s) to page/block 3 [write]
	//   ⚠ page 3 holds lock/OTP bytes on NTAG/Ultralight; a write here can set permanent lock bits
	//   ⚠ writes to the tag; a write to a configuration or OTP page is not reversible
}

// The commands in apdu.go were generated, and this is the mechanical proof that
// each one is still structurally sound and means what its name says, with no AI
// in the loop: a builder is called, its output is decoded, and the decode is
// checked against what the builder claims to be. A builder that miscomputes a
// length or lands in the wrong class fails here.

// builderCase is one apdu.go builder, its output, and what it must decode to.
type builderCase struct {
	name    string
	cmd     []byte
	raw     bool
	class   APDUClass
	mutates bool
	// summarySub, when set, must appear in the decoded summary. It pins the
	// human-readable half so a reworded builder cannot silently change meaning.
	summarySub string
}

func builderCases() []builderCase {
	key := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	four := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	aid := []byte{0xD2, 0x76, 0x00, 0x00, 0x85, 0x01, 0x01}
	dfAID := []byte{0x01, 0x02, 0x03}

	return []builderCase{
		{name: "GetUIDAPDU", cmd: GetUIDAPDU(), class: ClassInfo, summarySub: "GET UID"},
		{name: "LoadKeyAPDU", cmd: LoadKeyAPDU(0x00, key), class: ClassReaderControl, summarySub: "LOAD KEY"},
		{name: "MIFAREAuthAPDU", cmd: MIFAREAuthAPDU(4, MIFAREKeyA, 0x00), class: ClassAuth, summarySub: "AUTHENTICATE"},
		{name: "ReadBinaryAPDU", cmd: ReadBinaryAPDU(4, 4), class: ClassRead, summarySub: "READ BINARY"},
		{name: "UpdateBinaryAPDU", cmd: UpdateBinaryAPDU(4, four), class: ClassWrite, mutates: true, summarySub: "UPDATE BINARY"},
		{name: "ReadBinaryExtAPDU", cmd: ReadBinaryExtAPDU(0x0000, 16), class: ClassRead, summarySub: "READ BINARY"},
		{name: "UpdateBinaryExtAPDU", cmd: UpdateBinaryExtAPDU(0x0000, four), class: ClassWrite, mutates: true, summarySub: "UPDATE BINARY"},
		{name: "SelectFileAPDU", cmd: SelectFileAPDU([]byte{0xE1, 0x03}), class: ClassSelect, summarySub: "SELECT"},
		{name: "SelectFileByAIDAPDU", cmd: SelectFileByAIDAPDU(aid), class: ClassSelect, summarySub: "NDEF Type 4 application"},
		{name: "GetVersionAPDU", cmd: GetVersionAPDU(), class: ClassInfo, summarySub: "GET_VERSION"},
		{name: "UltralightReadAPDU", cmd: UltralightReadAPDU(4), class: ClassRead, summarySub: "READ"},
		{name: "UltralightWriteAPDU", cmd: UltralightWriteAPDU(4, four), class: ClassWrite, mutates: true, summarySub: "WRITE"},
		{name: "DESFireSelectAppAPDU", cmd: DESFireSelectAppAPDU(dfAID), class: ClassSelect, summarySub: "SelectApplication"},
		{name: "DESFireGetAppIDsAPDU", cmd: DESFireGetAppIDsAPDU(), class: ClassRead, summarySub: "GetApplicationIDs"},
		{name: "DESFireGetFileIDsAPDU", cmd: DESFireGetFileIDsAPDU(), class: ClassRead, summarySub: "GetFileIDs"},
		{name: "DESFireReadDataAPDU", cmd: DESFireReadDataAPDU(1, 0, 16), class: ClassRead, summarySub: "ReadData"},
		{name: "DESFireWriteDataAPDU", cmd: DESFireWriteDataAPDU(1, 0, four), class: ClassWrite, mutates: true, summarySub: "WriteData"},
		{name: "DESFireAuthAPDU", cmd: DESFireAuthAPDU(0, DFCmdAuthenticateAES), class: ClassAuth, summarySub: "Authenticate"},
		{name: "DESFireAdditionalFrameAPDU", cmd: DESFireAdditionalFrameAPDU(four), class: ClassRead, summarySub: "AdditionalFrame"},
	}
}

// Every builder decodes to the class and meaning it claims. This is the round
// trip: build -> decode -> the decode agrees with the builder.
func TestExplainRoundTripsEveryBuilder(t *testing.T) {
	for _, tc := range builderCases() {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.cmd) == 0 {
				t.Fatalf("%s produced no bytes", tc.name)
			}
			e := Explain(tc.cmd, tc.raw)

			if e.Class != tc.class {
				t.Errorf("class = %q, want %q\n  decoded: %s", e.Class, tc.class, e)
			}
			if e.Mutating != tc.mutates {
				t.Errorf("mutating = %v, want %v\n  decoded: %s", e.Mutating, tc.mutates, e)
			}
			if !e.Recognized {
				t.Errorf("builder output was not recognised by the decoder\n  bytes: % X", tc.cmd)
			}
			if tc.summarySub != "" && !strings.Contains(e.Summary, tc.summarySub) {
				t.Errorf("summary %q does not mention %q", e.Summary, tc.summarySub)
			}
		})
	}
}

// A builder must not emit a malformed command: whenever it declares a length,
// that length has to match the data it carries. A miscomputed Lc is the classic
// silent APDU bug, and it is caught here for every builder at once.
func TestBuildersAreStructurallyConsistent(t *testing.T) {
	for _, tc := range builderCases() {
		t.Run(tc.name, func(t *testing.T) {
			e := Explain(tc.cmd, tc.raw)
			if e.Fields.HasLc && e.Fields.Lc != len(e.Fields.Data) {
				t.Errorf("declared Lc=%d but carries %d data byte(s): % X", e.Fields.Lc, len(e.Fields.Data), tc.cmd)
			}
			for _, w := range e.Warnings {
				if strings.Contains(w, "declared length") {
					t.Errorf("builder emits a malformed length: %s\n  bytes: % X", w, tc.cmd)
				}
				if strings.Contains(w, "unrecognised") {
					t.Errorf("builder emits a command the decoder cannot classify: % X", tc.cmd)
				}
			}
		})
	}
}

// The literals hard-coded in the emulator and console are decoded and pinned, so
// a reader who cannot write APDU can see what each one is, and a change to any of
// them has to be a deliberate edit to this table.
func TestExplainKnownLiterals(t *testing.T) {
	cases := []struct {
		name    string
		cmd     []byte
		raw     bool
		class   APDUClass
		mutates bool
		sub     string
	}{
		// nfctest/emulator_test.go:27 — the READ literal a test asserts by hand.
		{"emulator READ page 4", []byte{0xFF, 0xB0, 0x00, 0x04, 0x04}, false, ClassRead, false, "page/block 4"},
		// nfctest/emulator_test.go:31 — the WRITE literal, and the danger flag.
		{"emulator WRITE page 4", []byte{0xFF, 0xD6, 0x00, 0x04, 0x04, 0xDE, 0xAD, 0xBE, 0xEF}, false, ClassWrite, true, "UPDATE BINARY"},
		// supervisor_test.go:230 — a raw framing exchange.
		{"framing READ page 0", []byte{0x30, 0x00}, true, ClassRead, false, "native READ"},
		// apdu.ts presets.
		{"preset Select NDEF app", []byte{0x00, 0xA4, 0x04, 0x00, 0x07, 0xD2, 0x76, 0x00, 0x00, 0x85, 0x01, 0x01, 0x00}, false, ClassSelect, false, "NDEF Type 4 application"},
		{"preset Read binary 16B", []byte{0x00, 0xB0, 0x00, 0x00, 0x10}, false, ClassRead, false, "16 byte"},
		{"preset DESFire GetVersion", []byte{0x90, 0x60, 0x00, 0x00, 0x00}, false, ClassInfo, false, "GetVersion"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := Explain(tc.cmd, tc.raw)
			if e.Class != tc.class {
				t.Errorf("class = %q, want %q\n  decoded: %s", e.Class, tc.class, e)
			}
			if e.Mutating != tc.mutates {
				t.Errorf("mutating = %v, want %v", e.Mutating, tc.mutates)
			}
			if !strings.Contains(e.Summary, tc.sub) {
				t.Errorf("summary %q does not mention %q", e.Summary, tc.sub)
			}
		})
	}
}

// A write to a static lock/OTP page is the one write worth a louder warning, and
// the decoder flags it whether the write arrives as a PC/SC UPDATE BINARY or a
// native page write.
func TestExplainFlagsLockPageWrites(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  []byte
		raw  bool
	}{
		{"UPDATE BINARY page 2", []byte{0xFF, 0xD6, 0x00, 0x02, 0x04, 0x00, 0x00, 0x00, 0x00}, false},
		{"native WRITE page 3", []byte{0xA2, 0x03, 0x00, 0x00, 0x00, 0x00}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := Explain(tc.cmd, tc.raw)
			if !hasWarning(e, "lock/OTP") {
				t.Errorf("a write to a lock page was not flagged\n  decoded: %s", e)
			}
		})
	}
}

// A malformed command is reported, not silently mis-split: the decoder is a
// safety net, so a length that does not add up has to surface as a warning.
func TestExplainFlagsMalformedLength(t *testing.T) {
	// Lc says 5 data bytes, only 3 follow.
	e := Explain([]byte{0x00, 0xA4, 0x04, 0x00, 0x05, 0xE1, 0x03, 0x00}, false)
	if !hasWarning(e, "declared length") {
		t.Errorf("a mismatched Lc was not flagged\n  decoded: %s", e)
	}
}

// An unknown command is never called harmless: a raw channel that cannot tell
// what a command does must treat it as if it could write.
func TestExplainTreatsUnknownAsMutating(t *testing.T) {
	e := Explain([]byte{0x00, 0xEE, 0x00, 0x00}, false)
	if e.Recognized {
		t.Error("a made-up INS was reported as recognised")
	}
	if !e.Mutating {
		t.Error("an unrecognised command was treated as harmless")
	}
	if e.Class != ClassUnknown {
		t.Errorf("class = %q, want %q", e.Class, ClassUnknown)
	}
}

// Empty and truncated input are answered, not panicked on: the decoder takes
// whatever the operator typed.
func TestExplainHandlesShortInput(t *testing.T) {
	if e := Explain(nil, false); e.Class != ClassUnknown {
		t.Errorf("empty command class = %q, want %q", e.Class, ClassUnknown)
	}
	if e := Explain([]byte{0xFF, 0xCA}, false); e.Recognized {
		t.Error("a 2-byte fragment was reported as a recognised APDU")
	}
}

// The direct-transmit envelope is unwrapped: FF 00 00 00 Lc <native> Le carries
// a framing command, and the decode reflects the inner command, once.
func TestExplainUnwrapsDirectTransmit(t *testing.T) {
	e := Explain(DirectTransmitAPDU([]byte{0xA2, 0x05, 0x01, 0x02, 0x03, 0x04}), false)
	if e.Class != ClassWrite {
		t.Errorf("class = %q, want %q (the wrapped native WRITE)", e.Class, ClassWrite)
	}
	if !strings.Contains(e.Summary, "direct transmit") || !strings.Contains(e.Summary, "WRITE") {
		t.Errorf("summary %q does not show the unwrapped command", e.Summary)
	}
	if n := countWarning(e, "writes to the tag"); n != 1 {
		t.Errorf("write warning appeared %d times, want 1 (nested decode should dedupe)", n)
	}
}

func hasWarning(e APDUExplanation, sub string) bool {
	return countWarning(e, sub) > 0
}

func countWarning(e APDUExplanation, sub string) int {
	n := 0
	for _, w := range e.Warnings {
		if strings.Contains(w, sub) {
			n++
		}
	}
	return n
}
