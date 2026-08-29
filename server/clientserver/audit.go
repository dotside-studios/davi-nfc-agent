package clientserver

import (
	"fmt"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// The raw APDU channel does not second-guess a command — the operator opened it
// deliberately, and the decoder's danger cues are heuristic and tag-type
// dependent — but it does record every one. A tag changed or bricked by a raw
// exchange can then be traced to what was sent, which is the accountability the
// mode and channel gates cannot provide on their own.

// auditLevel is the severity a raw exchange is recorded at.
type auditLevel int

const (
	auditInfo auditLevel = iota
	auditWarn
)

// rawExchangeAudit decodes a raw exchange into the line it is logged as, and the
// level to log it at: a command that changes the tag, or one the decoder
// cautions about, is a warning; the rest are informational.
//
// It records the decoded summary, never the command bytes: a command such as
// LOAD KEY or an authenticate carries key material in its data field, which must
// not reach the log.
func rawExchangeAudit(cmd []byte, raw bool, device, uid string) (auditLevel, string) {
	ex := nfc.Explain(cmd, raw)

	where := "on the reader"
	if device != "" {
		where = "on " + device
	}
	if uid != "" {
		where += " (tag " + uid + ")"
	}

	msg := fmt.Sprintf("Raw exchange %s: %s [%s]", where, ex.Summary, ex.Class)
	for _, w := range ex.Warnings {
		msg += "; " + w
	}

	if ex.Mutating || len(ex.Warnings) > 0 {
		return auditWarn, msg
	}
	return auditInfo, msg
}

// auditRawExchange records a raw exchange in the client log. It never refuses:
// the gates are the consent, and this is the trail.
func auditRawExchange(cmd []byte, raw bool, device, uid string) {
	level, msg := rawExchangeAudit(cmd, raw, device, uid)
	if level == auditWarn {
		clientWarn.Printf("%s", msg)
	} else {
		clientLog.Printf("%s", msg)
	}
}
