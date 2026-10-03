package clientserver

import (
	"fmt"
	"strings"

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

// auditRawSession records the start or end of a raw session in the client log.
// A session holds the reader to one client, so who held it and for which tag is
// part of the trail.
func auditRawSession(event, device, uid, sessionID string) {
	where := "on the reader"
	if device != "" {
		where = "on " + device
	}
	if uid != "" {
		where += " (tag " + uid + ")"
	}
	if sessionID != "" {
		where += " (session " + sessionID + ")"
	}
	clientLog.Printf("Raw session %s %s", event, where)
}

// auditRawSequence records a raw sequence in the client log: one line for the
// run, then each step as an exchange. Steps are recorded before the run, so one
// that is never sent because an earlier reply stopped the run is listed too.
func auditRawSequence(steps []nfc.SequenceStep, device, uid, sessionID string) {
	where := "on the reader"
	if device != "" {
		where = "on " + device
	}
	if uid != "" {
		where += " (tag " + uid + ")"
	}
	if sessionID != "" {
		where += " (session " + sessionID + ")"
	}
	clientLog.Printf("Raw sequence of %d steps %s", len(steps), where)
	for _, step := range steps {
		auditRawExchange(step.Data, false, device, uid)
	}
}

// auditRawFollowups records the commands autoGetResponse sent after the ones the
// client asked for, a GET RESPONSE or a command re-sent with a corrected Le,
// each as part of the exchange it followed. One list per step; a step that
// needed none has an empty one. They are recorded after the run, as it is the
// card's replies that decide whether there are any.
func auditRawFollowups(followups [][][]byte, device, uid, sessionID string) {
	for _, follow := range followups {
		for _, cmd := range follow {
			level, msg := rawExchangeAudit(cmd, false, device, uid)
			msg = "Raw exchange follow-up (autoGetResponse)" + strings.TrimPrefix(msg, "Raw exchange")
			if sessionID != "" {
				msg += " (session " + sessionID + ")"
			}
			if level == auditWarn {
				clientWarn.Printf("%s", msg)
			} else {
				clientLog.Printf("%s", msg)
			}
		}
	}
}

// auditNTAG424 records an NTAG 424 operation in the client log. It takes the
// operation's name and its non-secret arguments only, never key material.
func auditNTAG424(op string, mutating bool, device, uid, detail string) {
	where := "on the reader"
	if device != "" {
		where = "on " + device
	}
	if uid != "" {
		where += " (tag " + uid + ")"
	}
	msg := fmt.Sprintf("NTAG 424 %s %s", op, where)
	if detail != "" {
		msg += ": " + detail
	}
	if mutating {
		clientWarn.Printf("%s", msg)
	} else {
		clientLog.Printf("%s", msg)
	}
}
