package nfc

import (
	"fmt"
	"strings"
)

// APDU explanation. This decodes a command back into what it does, so a human
// can check an exchange without reading the bytes and a caller can gate or warn
// on it without a lookup table of its own.
//
// It is deterministic and self-contained: the commands in this package were
// written to be exact, and this is the mechanical check that they still are,
// with nothing to consult but the bytes. A raw exchange the operator types, a
// builder in apdu.go, and a literal pinned in a test all decode through the same
// path, so what the console shows and what a test asserts cannot drift apart.

// APDUClass is what an exchange does, coarse enough to gate on. The dangerous
// ones (Write, Lock) are separated from the harmless ones precisely so a caller
// can refuse or warn without understanding the command.
type APDUClass string

const (
	// ClassRead reads from the tag without changing it.
	ClassRead APDUClass = "read"
	// ClassWrite changes the tag's memory. Not reversible in general: the same
	// command that writes a data page writes a configuration or lock page.
	ClassWrite APDUClass = "write"
	// ClassLock makes some part of the tag permanently read-only. Never
	// reversible.
	ClassLock APDUClass = "lock"
	// ClassAuth authenticates to the tag or the reader. Changes session state,
	// not tag memory.
	ClassAuth APDUClass = "auth"
	// ClassSelect selects an application or file. Changes what the next command
	// addresses, not the tag.
	ClassSelect APDUClass = "select"
	// ClassInfo reads identity or version data (UID, GET_VERSION). Harmless.
	ClassInfo APDUClass = "info"
	// ClassReaderControl targets the reader, not the tag: load a key into reader
	// memory, drive the LED or buzzer. Never reaches the card as a command.
	ClassReaderControl APDUClass = "reader-control"
	// ClassUnknown is a command this decoder does not recognise. Treated as
	// possibly mutating, because an unknown command cannot be assumed harmless.
	ClassUnknown APDUClass = "unknown"
)

// APDUFields are the parsed structural parts of a command APDU. They are what a
// structural check reads: a declared length that disagrees with the data is a
// malformed command whatever it means.
type APDUFields struct {
	CLA, INS, P1, P2 byte
	HasLc            bool
	Lc               int
	Data             []byte
	HasLe            bool
	Le               int
	Extended         bool
}

// APDUExplanation is what a command decodes to.
type APDUExplanation struct {
	// Summary is a one-line, human-readable account: "READ BINARY — 16 bytes at
	// offset 0", "UPDATE BINARY — write 4 bytes to page 4".
	Summary string

	// Class is what the command does, coarse enough to gate on.
	Class APDUClass

	// Mutating reports whether the command can change or permanently alter the
	// tag. It is the single bit a safety net cares about, and it errs toward
	// true: a write, a lock, and an unrecognised command are all mutating.
	Mutating bool

	// Warnings are the reasons to look twice: a malformed length, an
	// irreversible effect, an unrecognised command. Empty is a clean bill.
	Warnings []string

	// Dialect names the command family: "ISO7816", "PC/SC", "DESFire", or
	// "framing" for a native framing-level command.
	Dialect string

	// Recognized reports whether the decoder knew the command. A false here is
	// itself a warning, surfaced in Warnings too.
	Recognized bool

	// Fields are the parsed structural parts, for a caller that wants them.
	Fields APDUFields
}

// String renders the explanation for a log line or a test failure.
func (e APDUExplanation) String() string {
	b := &strings.Builder{}
	fmt.Fprintf(b, "%s [%s]", e.Summary, e.Class)
	for _, w := range e.Warnings {
		fmt.Fprintf(b, "\n  ⚠ %s", w)
	}
	return b.String()
}

// Explain decodes a command into what it does. raw selects framing-level
// (native, NfcA.transceive / InCommunicateThru) over APDU-level (ISO 7816-4,
// IsoDep / sendCommand), matching the flag on a transceive: the same bytes mean
// different things at the two levels, so the caller says which it sent.
//
// It never errors: an unparseable or unknown command is reported as such, with
// Recognized false and Class Unknown, because "I do not know what this is" is
// the answer a safety net needs, not a failure.
func Explain(cmd []byte, raw bool) APDUExplanation {
	if len(cmd) == 0 {
		return APDUExplanation{
			Summary:  "empty command",
			Class:    ClassUnknown,
			Mutating: false,
			Warnings: []string{"no bytes to send"},
			Dialect:  dialectOf(raw),
		}
	}
	if raw {
		return explainFraming(cmd)
	}
	return explainAPDU(cmd)
}

func dialectOf(raw bool) string {
	if raw {
		return "framing"
	}
	return "ISO7816"
}

// explainAPDU decodes an ISO 7816-4 command APDU, including the PC/SC (CLA=0xFF)
// and DESFire-wrapped (CLA=0x90) families layered on top of it.
func explainAPDU(cmd []byte) APDUExplanation {
	e := APDUExplanation{Dialect: "ISO7816"}
	if len(cmd) < 4 {
		e.Summary = fmt.Sprintf("truncated APDU (% X)", cmd)
		e.Class = ClassUnknown
		e.Mutating = true
		e.Warnings = []string{"an APDU has at least CLA INS P1 P2 (4 bytes)"}
		return e
	}

	f, lenWarn := parseAPDUBody(cmd)
	e.Fields = f
	if lenWarn != "" {
		e.Warnings = append(e.Warnings, lenWarn)
	}

	switch f.CLA {
	case CLAPCSC:
		describePCSC(&e, f)
	case CLADESFire:
		describeDESFire(&e, f)
	case CLAStandard:
		describeISO(&e, f)
	case CLAProprietry:
		e.Summary = fmt.Sprintf("proprietary command (CLA 80, INS %02X)", f.INS)
		e.Class = ClassUnknown
		e.Recognized = false
	default:
		e.Summary = fmt.Sprintf("unrecognised command (CLA %02X INS %02X)", f.CLA, f.INS)
		e.Class = ClassUnknown
		e.Recognized = false
	}

	finalize(&e)
	return e
}

// parseAPDUBody splits an APDU into its fields across the ISO 7816-4 short cases
// (1–4) and detects the extended form. It returns a warning string when the
// declared length does not match the bytes present, which is the malformed
// command a structural check exists to catch.
func parseAPDUBody(cmd []byte) (APDUFields, string) {
	f := APDUFields{CLA: cmd[0], INS: cmd[1], P1: cmd[2], P2: cmd[3]}
	body := cmd[4:]

	switch {
	case len(body) == 0:
		// Case 1: header only.
		return f, ""
	case len(body) == 1:
		// Case 2S: a single Le byte, no data.
		f.HasLe = true
		f.Le = leValue(body[0])
		return f, ""
	}

	// Extended APDUs begin the body with a 0x00 marker (and are longer than a
	// short case-3 with Lc=0 could be). Report them rather than mis-splitting.
	if body[0] == 0x00 && len(body) >= 3 {
		f.Extended = true
		return f, "extended-length APDU; structural fields not fully decoded"
	}

	lc := int(body[0])
	rest := body[1:]
	f.HasLc = true
	f.Lc = lc

	switch {
	case len(rest) == lc:
		// Case 3S: Lc + data, no Le.
		f.Data = rest
		return f, ""
	case len(rest) == lc+1:
		// Case 4S: Lc + data + Le.
		f.Data = rest[:lc]
		f.HasLe = true
		f.Le = leValue(rest[lc])
		return f, ""
	default:
		// The declared Lc does not fit the bytes present. Keep as much data as
		// there is, so the rest of the decode is still useful, and flag it.
		if len(rest) < lc {
			f.Data = rest
		} else {
			f.Data = rest[:lc]
		}
		return f, fmt.Sprintf("declared length Lc=%d does not match %d byte(s) of data present", lc, len(rest))
	}
}

// leValue reads an Le byte, where 0x00 means the maximum (256) rather than zero.
func leValue(b byte) int {
	if b == 0x00 {
		return 256
	}
	return int(b)
}

// describePCSC decodes the PC/SC pseudo-APDUs (CLA=0xFF). These target the
// reader as much as the card: GET UID and READ/UPDATE BINARY reach the tag,
// while LOAD KEY and the LED/buzzer escape are reader instructions.
func describePCSC(e *APDUExplanation, f APDUFields) {
	e.Dialect = "PC/SC"
	e.Recognized = true
	switch f.INS {
	case INSGetUID: // 0xCA
		e.Summary = "GET UID — read the card serial number"
		e.Class = ClassInfo
	case INSReadBinary: // 0xB0
		e.Summary = fmt.Sprintf("READ BINARY — read %d byte(s) from page/block %d", bytesRead(f), f.P2)
		e.Class = ClassRead
	case INSUpdateBin: // 0xD6
		e.Summary = fmt.Sprintf("UPDATE BINARY — write %d byte(s) to page/block %d", len(f.Data), f.P2)
		e.Class = ClassWrite
		flagPageWrite(e, f.P2)
	case INSLoadKey: // 0x82
		e.Summary = fmt.Sprintf("LOAD KEY — store a key in reader slot %d", f.P2)
		e.Class = ClassReaderControl
	case INSAuth: // 0x86
		e.Summary = "GENERAL AUTHENTICATE — reader-side MIFARE authentication"
		e.Class = ClassAuth
	case INSDirectCmd: // 0x00
		describePCSCDirect(e, f)
	default:
		e.Summary = fmt.Sprintf("unrecognised PC/SC command (INS %02X)", f.INS)
		e.Class = ClassUnknown
		e.Recognized = false
	}
}

// describePCSCDirect decodes the CLA=FF INS=00 family: the ACR122-style LED and
// buzzer escape (P1=0x40) and the direct-transmit envelope (P1=0x00) that wraps
// a native command, which is decoded in turn.
func describePCSCDirect(e *APDUExplanation, f APDUFields) {
	switch f.P1 {
	case 0x40:
		e.Summary = "reader LED/buzzer control (ACR122 escape)"
		e.Class = ClassReaderControl
	case 0x00:
		if len(f.Data) == 0 {
			e.Summary = "direct transmit — empty payload"
			e.Class = ClassUnknown
			e.Recognized = false
			return
		}
		inner := Explain(f.Data, true)
		e.Summary = "direct transmit → " + inner.Summary
		e.Class = inner.Class
		e.Recognized = inner.Recognized
		e.Warnings = append(e.Warnings, inner.Warnings...)
	default:
		e.Summary = fmt.Sprintf("PC/SC direct command (P1 %02X)", f.P1)
		e.Class = ClassReaderControl
	}
}

// describeDESFire decodes the DESFire-wrapped family (CLA=0x90), where the INS
// byte is the native DESFire command carried inside an ISO 7816 envelope.
func describeDESFire(e *APDUExplanation, f APDUFields) {
	e.Dialect = "DESFire"
	e.Recognized = true
	switch f.INS {
	case DFCmdGetVersion: // 0x60
		e.Summary = "DESFire GetVersion — read chip version (continues with 91 AF)"
		e.Class = ClassInfo
	case DFCmdGetApplicationIDs: // 0x6A
		e.Summary = "DESFire GetApplicationIDs — list applications"
		e.Class = ClassRead
	case DFCmdGetFileIDs: // 0x6F
		e.Summary = "DESFire GetFileIDs — list files in the selected application"
		e.Class = ClassRead
	case DFCmdSelectApplication: // 0x5A
		e.Summary = fmt.Sprintf("DESFire SelectApplication — select AID %X", f.Data)
		e.Class = ClassSelect
	case DFCmdReadData: // 0xBD
		e.Summary = "DESFire ReadData — read from a file"
		e.Class = ClassRead
	case DFCmdWriteData: // 0x3D
		e.Summary = "DESFire WriteData — write to a file"
		e.Class = ClassWrite
	case DFCmdAuthenticate, DFCmdAuthenticateISO, DFCmdAuthenticateAES: // 0x0A / 0x1A / 0xAA
		e.Summary = fmt.Sprintf("DESFire Authenticate (0x%02X)", f.INS)
		e.Class = ClassAuth
	case DFCmdAdditionalFrame: // 0xAF
		e.Summary = "DESFire AdditionalFrame — continue a chained command"
		e.Class = ClassRead
	default:
		e.Summary = fmt.Sprintf("unrecognised DESFire command (0x%02X)", f.INS)
		e.Class = ClassUnknown
		e.Recognized = false
	}
}

// describeISO decodes the standard ISO 7816-4 commands (CLA=0x00) the Type 4
// path uses: SELECT, READ BINARY and UPDATE BINARY.
func describeISO(e *APDUExplanation, f APDUFields) {
	e.Dialect = "ISO7816"
	e.Recognized = true
	switch f.INS {
	case INSSelectFile: // 0xA4
		e.Summary = "SELECT — " + describeSelect(f)
		e.Class = ClassSelect
	case INSReadBinary: // 0xB0
		e.Summary = fmt.Sprintf("READ BINARY — read %d byte(s) at offset %d", bytesRead(f), isoOffset(f))
		e.Class = ClassRead
	case INSUpdateBin: // 0xD6
		e.Summary = fmt.Sprintf("UPDATE BINARY — write %d byte(s) at offset %d", len(f.Data), isoOffset(f))
		e.Class = ClassWrite
	default:
		e.Summary = fmt.Sprintf("ISO 7816 command (INS %02X)", f.INS)
		e.Class = ClassUnknown
		e.Recognized = false
	}
}

// describeSelect renders the SELECT operand: what P1 selects by and, where the
// data names a known file or application, what it is.
func describeSelect(f APDUFields) string {
	var by string
	switch f.P1 {
	case 0x04:
		by = "by name/AID"
	case 0x00:
		by = "by file identifier"
	case 0x01:
		by = "child DF"
	case 0x02:
		by = "child EF"
	case 0x08:
		by = "by path from MF"
	case 0x09:
		by = "by path from current DF"
	default:
		by = fmt.Sprintf("P1=%02X", f.P1)
	}
	if named := knownSelectTarget(f.Data); named != "" {
		return by + ", " + named
	}
	if len(f.Data) > 0 {
		return fmt.Sprintf("%s (%X)", by, f.Data)
	}
	return by
}

// knownSelectTarget names the well-known NDEF Type 4 identifiers, so a SELECT of
// them reads as what it is rather than a bare hex string.
func knownSelectTarget(data []byte) string {
	switch strings.ToUpper(BytesToHex(data)) {
	case "D2760000850101":
		return "the NDEF Type 4 application"
	case "E103":
		return "the Capability Container file"
	case "E104":
		return "the NDEF file"
	}
	return ""
}

// isoOffset reads the 15-bit offset a standard READ/UPDATE BINARY carries in
// P1P2 (the high bit of P1 is a flag, not part of the offset).
func isoOffset(f APDUFields) int {
	return int(f.P1&0x7F)<<8 | int(f.P2)
}

// bytesRead reports how many bytes a read returns: the Le when present, else 0
// for a command that does not say.
func bytesRead(f APDUFields) int {
	if f.HasLe {
		return f.Le
	}
	return 0
}

// flagPageWrite adds a lock warning for a write to the static lock pages of an
// NTAG/Ultralight tag (pages 2 and 3 hold the lock bytes and, on page 3, the
// one-time-programmable bits). It is a heuristic — the decoder does not know the
// tag — worded as a possibility, not a certainty.
func flagPageWrite(e *APDUExplanation, page byte) {
	if page == 2 || page == 3 {
		e.Warnings = append(e.Warnings,
			fmt.Sprintf("page %d holds lock/OTP bytes on NTAG/Ultralight; a write here can set permanent lock bits", page))
	}
}

// explainFraming decodes a native framing-level command (raw exchange). These
// opcodes are shared across tag families, so where one byte means different
// things on Ultralight/NTAG and on MIFARE Classic, the summary says so rather
// than guessing the tag.
func explainFraming(cmd []byte) APDUExplanation {
	e := APDUExplanation{Dialect: "framing", Recognized: true}
	op := cmd[0]
	switch op {
	case 0x30:
		e.Summary = fmt.Sprintf("native READ — Ultralight/NTAG page %s (16 bytes), or MIFARE Classic block read", framingArg(cmd))
		e.Class = ClassRead
	case 0x3A:
		e.Summary = "native FAST_READ — NTAG page range"
		e.Class = ClassRead
	case 0xA2:
		e.Summary = fmt.Sprintf("native WRITE — Ultralight/NTAG page %s (4 bytes)", framingArg(cmd))
		e.Class = ClassWrite
		if len(cmd) >= 2 {
			flagPageWrite(&e, cmd[1])
		}
	case 0xA0:
		e.Summary = "native WRITE — MIFARE Classic block, or Ultralight compatibility write"
		e.Class = ClassWrite
	case 0x60:
		e.Summary = "native GET_VERSION (NTAG/Ultralight), or MIFARE Classic AUTH key A — ambiguous without the tag type"
		e.Class = ClassInfo
	case 0x61:
		e.Summary = "MIFARE Classic AUTH key B"
		e.Class = ClassAuth
	case 0x1B:
		e.Summary = "native PWD_AUTH — NTAG password authentication"
		e.Class = ClassAuth
	case 0x3C:
		e.Summary = "native READ_SIG — NTAG originality signature"
		e.Class = ClassInfo
	case 0x39:
		e.Summary = "native READ_CNT — NTAG counter"
		e.Class = ClassRead
	case 0x50:
		e.Summary = "native HALT"
		e.Class = ClassInfo
	default:
		e.Summary = fmt.Sprintf("unrecognised framing command (opcode %02X)", op)
		e.Class = ClassUnknown
		e.Recognized = false
	}
	finalize(&e)
	return e
}

// framingArg renders the page/block argument of a native command, or a
// placeholder when the command carries none.
func framingArg(cmd []byte) string {
	if len(cmd) >= 2 {
		return fmt.Sprintf("%d", cmd[1])
	}
	return "?"
}

// finalize settles the derived fields once the class is known: what mutates, and
// the warnings a caller should see for a mutating or unrecognised command. It is
// idempotent — a nested decode (the direct-transmit envelope) runs it on the
// inner command first — so warnings are deduplicated rather than doubled.
func finalize(e *APDUExplanation) {
	switch e.Class {
	case ClassWrite:
		e.Mutating = true
		e.Warnings = append(e.Warnings, "writes to the tag; a write to a configuration or OTP page is not reversible")
	case ClassLock:
		e.Mutating = true
		e.Warnings = append(e.Warnings, "makes part of the tag permanently read-only; not reversible")
	case ClassUnknown:
		// An unrecognised command cannot be assumed harmless.
		e.Mutating = true
		if !e.Recognized {
			e.Warnings = append(e.Warnings, "unrecognised command; the agent cannot tell whether it writes or locks")
		}
	}
	e.Warnings = dedupe(e.Warnings)
}

// dedupe drops repeated warnings while keeping their order, so a nested decode
// does not report the same caution twice.
func dedupe(in []string) []string {
	if len(in) < 2 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := in[:0]
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
