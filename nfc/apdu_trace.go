package nfc

import (
	"fmt"
	"os"
)

// apduTrace turns on a per-command trace of the driver's tag I/O, decoded into
// what each command does. It is read once at startup and off unless
// DAVI_NFC_APDU_TRACE=1: a full trace is verbose (a single NDEF read is dozens
// of page reads) and belongs to someone debugging a reader, not the default log.
var apduTrace = os.Getenv("DAVI_NFC_APDU_TRACE") == "1"

// traceAPDU records one command the driver sent to a tag and how it answered,
// decoded through Explain so the trace reads in intent rather than hex.
//
// It logs the decoded summary, never the command bytes, and the response status
// word rather than the response data: a command such as LOAD KEY or an
// authenticate carries key material, and a read carries tag contents, neither of
// which a debug trace should spill into the log.
func traceAPDU(uid string, cmd, resp []byte, err error) {
	ex := Explain(cmd, false)
	readerLog.Printf("APDU %s: %s [%s] %s", traceUID(uid), ex.Summary, ex.Class, traceOutcome(resp, err))
}

// traceUID names the tag a command was sent to, or a placeholder before one is
// known.
func traceUID(uid string) string {
	if uid == "" {
		return "(no uid)"
	}
	return uid
}

// traceOutcome summarises a response without echoing its data: the status word
// and how many data bytes came back, or the error.
func traceOutcome(resp []byte, err error) string {
	if err != nil {
		return "failed: " + err.Error()
	}
	if len(resp) >= 2 {
		sw := uint16(resp[len(resp)-2])<<8 | uint16(resp[len(resp)-1])
		return fmt.Sprintf("<- SW %04X (%d data byte(s))", sw, len(resp)-2)
	}
	return fmt.Sprintf("<- %d byte(s)", len(resp))
}
