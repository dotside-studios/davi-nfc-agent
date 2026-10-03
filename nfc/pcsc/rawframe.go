// Framing-level exchange over PC/SC.
//
// A PC/SC reader normally speaks ISO 7816 APDUs to the tag, which a Type 2 tag
// (NTAG21x, Ultralight) and a MIFARE Classic do not understand. Sending one of
// their own frames, such as READ_SIG (3C 00) or PWD_AUTH (1B ...), means asking
// the reader to put the bytes on the air as they are. PC/SC has no standard
// call for that on every reader, so this file carries the two methods readers
// offer:
//
//   - ACR122 class (PN532/PN533 based): the frame goes inside a Direct Transmit
//     pseudo-APDU (FF 00 00 00 Lc) as a PN532 InCommunicateThru command (D4 42),
//     which makes the chip add and check the CRC and hand back the tag's reply.
//   - PC/SC Part 3 transparent exchange: a session is started with FF C2, the
//     frame is sent in a transceive data object, and the session is ended.
//
// The wrap and unwrap functions are pure so they can be tested without a
// reader.

package pcsc

import (
	"errors"
	"fmt"
)

// PN532 InCommunicateThru (PN532 User Manual, section 7.3.9): the host sends
// D4 42 followed by the frame, and the chip answers D5 43, a status byte and the
// tag's reply. The chip adds the CRC to what it sends and strips it from what it
// receives, so the frame carries neither.
const (
	pn532Host                byte = 0xD4
	pn532InCommunicateThru   byte = 0x42
	pn532Device              byte = 0xD5
	pn532InCommunicateThruRs byte = 0x43

	// pn532StatusErrorMask selects the error code of a status byte. Bit 6 is
	// "more information" and bit 7 "NAD present", neither of which is an error.
	pn532StatusErrorMask byte = 0x3F
)

// maxThruFrame is the longest frame a Direct Transmit carries: Lc is one byte
// and two of its bytes are the PN532 header.
const maxThruFrame = 255 - 2

// pn532StatusNames names the error codes of the PN532 status byte (PN532 User
// Manual, section 7.1) that a framing-level exchange meets. A code not listed
// is still an error, reported by number.
var pn532StatusNames = map[byte]string{
	0x01: "timeout, the tag did not answer",
	0x02: "CRC error",
	0x03: "parity error",
	0x04: "bit count anomaly",
	0x05: "framing error",
	0x06: "abnormal bit collision",
	0x07: "communication buffer too small",
	0x09: "RF buffer overflow",
	0x0A: "RF field not switched on in time",
	0x0B: "RF protocol error",
	0x0D: "overheating",
	0x0E: "internal buffer overflow",
	0x10: "invalid parameter",
	0x13: "data format does not match the specification",
	0x14: "authentication error",
	0x23: "UID check byte is wrong",
	0x25: "invalid device state",
	0x26: "operation not allowed in this configuration",
	0x27: "command not acceptable in the current context",
	0x29: "target released by the initiator",
	0x2A: "card ID does not match",
	0x2B: "card disappeared",
	0x2C: "NFCID3 mismatch",
}

// acr122Thru wraps a frame as the Direct Transmit pseudo-APDU an ACR122 passes
// to its PN532 (ACS ACR122U API, section 5.0): FF 00 00 00 Lc D4 42 frame.
func acr122Thru(frame []byte) ([]byte, error) {
	if len(frame) == 0 {
		return nil, errors.New("no frame to send")
	}
	if len(frame) > maxThruFrame {
		return nil, fmt.Errorf("a frame carries at most %d bytes through Direct Transmit, got %d", maxThruFrame, len(frame))
	}
	cmd := make([]byte, 0, 7+len(frame))
	cmd = append(cmd, acr122CLA, 0x00, 0x00, 0x00, byte(2+len(frame)), pn532Host, pn532InCommunicateThru)
	return append(cmd, frame...), nil
}

// acr122Unthru reads the reader's answer to acr122Thru and returns the tag's
// reply. The answer is D5 43, the PN532 status byte, the reply, then SW 90 00;
// a nonzero error code in the status byte, such as 01 for a tag that stayed
// silent, is an error. The reader itself answers 63 00 when the chip rejected
// the command.
func acr122Unthru(resp []byte) ([]byte, error) {
	if len(resp) < 2 {
		return nil, fmt.Errorf("reader answered %d bytes, want at least a status word", len(resp))
	}
	sw1, sw2 := resp[len(resp)-2], resp[len(resp)-1]
	if sw1 != 0x90 || sw2 != 0x00 {
		return nil, fmt.Errorf("reader refused the frame: SW=%02X%02X", sw1, sw2)
	}
	body := resp[:len(resp)-2]
	if len(body) < 3 || body[0] != pn532Device || body[1] != pn532InCommunicateThruRs {
		return nil, fmt.Errorf("reader answer is not an InCommunicateThru response: % X", body)
	}
	if code := body[2] & pn532StatusErrorMask; code != 0 {
		return nil, pn532Status{code: code}
	}
	return append([]byte(nil), body[3:]...), nil
}

// pn532Status is the error a nonzero PN532 status byte reports.
type pn532Status struct {
	code byte
}

func (e pn532Status) Error() string {
	if name, ok := pn532StatusNames[e.code]; ok {
		return fmt.Sprintf("PN532 status %02X: %s", e.code, name)
	}
	return fmt.Sprintf("PN532 status %02X", e.code)
}

// PC/SC Part 3 transparent exchange (PC/SC Workgroup, Part 3 Supplemental
// Document for contactless, "Transparent Exchange"). The pseudo-APDU is
// FF C2 00 P2 Lc followed by BER-TLV data objects; P2 selects the operation.
//
// The numbers below are from memory of the supplement and of the vendor manuals
// that reproduce it, not from a copy of the document, and no reader was
// available to check them against. The data object tags (81, 82, 95, 5F46 and
// the response tags C0, 96, 97) are the ones best known; the P2 values and the
// layout of C0 are the least certain. Support is therefore only claimed for a
// reader that accepts the start-session command, see probeRawMode.
const (
	pcscTransparentINS      byte = 0xC2
	pcscP2ManageSession     byte = 0x00
	pcscP2TransparentExch   byte = 0x01
	objStartSession         byte = 0x81
	objEndSession           byte = 0x82
	objTransceive           byte = 0x95
	objResponseData         byte = 0x97
	objGenericError         byte = 0xC0
	objTimerHigh            byte = 0x5F
	objTimerLow             byte = 0x46
	transparentTimerMicros       = 250_000
	transparentMaxObjectLen      = 255
)

// transparentCommand builds a Part 3 pseudo-APDU around data objects.
func transparentCommand(p2 byte, objects []byte) ([]byte, error) {
	if len(objects) > 255 {
		return nil, fmt.Errorf("transparent exchange carries at most 255 bytes of data objects, got %d", len(objects))
	}
	cmd := []byte{acr122CLA, pcscTransparentINS, 0x00, p2, byte(len(objects))}
	return append(cmd, objects...), nil
}

// transparentStartSession is FF C2 00 00 02 81 00.
func transparentStartSession() []byte {
	cmd, _ := transparentCommand(pcscP2ManageSession, []byte{objStartSession, 0x00})
	return cmd
}

// transparentEndSession is FF C2 00 00 02 82 00.
func transparentEndSession() []byte {
	cmd, _ := transparentCommand(pcscP2ManageSession, []byte{objEndSession, 0x00})
	return cmd
}

// berLength encodes a data object length: one byte below 128, otherwise 81 and
// one byte.
func berLength(n int) []byte {
	if n < 0x80 {
		return []byte{byte(n)}
	}
	return []byte{0x81, byte(n)}
}

// transparentTransceive builds the exchange that sends frame to the tag: the
// timer object (5F46, microseconds, four bytes) then the transceive object
// (95).
func transparentTransceive(frame []byte) ([]byte, error) {
	if len(frame) == 0 {
		return nil, errors.New("no frame to send")
	}
	if len(frame) > transparentMaxObjectLen {
		return nil, fmt.Errorf("a frame carries at most %d bytes, got %d", transparentMaxObjectLen, len(frame))
	}
	micros := uint32(transparentTimerMicros)
	objects := []byte{objTimerHigh, objTimerLow, 0x04, byte(micros >> 24), byte(micros >> 16), byte(micros >> 8), byte(micros)}
	objects = append(objects, objTransceive)
	objects = append(objects, berLength(len(frame))...)
	objects = append(objects, frame...)
	return transparentCommand(pcscP2TransparentExch, objects)
}

// transparentObjects splits the data objects of a Part 3 answer, keyed by tag
// (one byte, or two when the first is 5F). A later object with a tag already
// seen is ignored. It rejects an object that runs past the end.
func transparentObjects(body []byte) (map[uint16][]byte, error) {
	objects := make(map[uint16][]byte)
	for i := 0; i < len(body); {
		tag := uint16(body[i])
		i++
		if tag&0x1F == 0x1F {
			if i >= len(body) {
				return nil, errors.New("data object tag is cut short")
			}
			tag = tag<<8 | uint16(body[i])
			i++
		}
		if i >= len(body) {
			return nil, fmt.Errorf("data object %X has no length", tag)
		}
		n := int(body[i])
		i++
		if n == 0x81 {
			if i >= len(body) {
				return nil, fmt.Errorf("data object %X has a cut-short length", tag)
			}
			n = int(body[i])
			i++
		}
		if i+n > len(body) {
			return nil, fmt.Errorf("data object %X claims %d bytes, %d remain", tag, n, len(body)-i)
		}
		if _, seen := objects[tag]; !seen {
			objects[tag] = body[i : i+n]
		}
		i += n
	}
	return objects, nil
}

// parseTransparent reads a Part 3 answer: SW 90 00 at the end and data objects
// before it. A generic error status object (C0) whose first byte is not zero is
// an error.
func parseTransparent(resp []byte) (map[uint16][]byte, error) {
	if len(resp) < 2 {
		return nil, fmt.Errorf("reader answered %d bytes, want at least a status word", len(resp))
	}
	sw1, sw2 := resp[len(resp)-2], resp[len(resp)-1]
	if sw1 != 0x90 || sw2 != 0x00 {
		return nil, fmt.Errorf("reader refused the command: SW=%02X%02X", sw1, sw2)
	}
	objects, err := transparentObjects(resp[:len(resp)-2])
	if err != nil {
		return nil, err
	}
	if status, ok := objects[uint16(objGenericError)]; ok && len(status) > 0 && status[0] != 0x00 {
		return nil, fmt.Errorf("reader reported error status % X", status)
	}
	return objects, nil
}

// transparentReply returns the tag's reply from the answer to a transceive: the
// response data object (97), empty when the tag sent nothing back.
func transparentReply(resp []byte) ([]byte, error) {
	objects, err := parseTransparent(resp)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), objects[uint16(objResponseData)]...), nil
}
