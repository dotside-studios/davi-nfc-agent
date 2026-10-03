package nfctest

import (
	"errors"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// Framing-level exchange on the NTAG/Ultralight emulator.
//
// A Type 2 tag answers its own frames (READ 30, FAST_READ 3A, WRITE A2, READ_SIG
// 3C, READ_CNT 39), which a PC/SC reader carries only when asked to put bytes on
// the air as they are. The emulator offers both ways a reader does: TransceiveRaw
// takes the frame directly, as a reader that carries one would, and the
// pseudo-APDU Direct Transmit (FF 00 00 00 Lc) carrying a PN532 InCommunicateThru
// (D4 42) answers in the PN532 framing (D5 43, status, reply) an ACR122 uses, so
// the wrap and unwrap a PC/SC driver does can be run against it.
//
// The command set and the reply layouts follow the NTAG21x and MIFARE
// Ultralight datasheets. The signature and the counter are placeholders that
// only have to round-trip: the signature is not a valid ECDSA signature for any
// key. A frame the tag would NAK is answered here with the PN532's timeout
// status (01), a modelling choice rather than something observed on a reader.

// errEmuNAK is what a frame the emulated tag refuses comes back as.
var errEmuNAK = errors.New("nfctest: tag did not acknowledge the frame")

const (
	frameRead     = 0x30
	frameFastRead = 0x3A
	frameWrite    = 0xA2
	frameReadSig  = 0x3C
	frameReadCnt  = 0x39

	// The PN532 framing as an ACR122 carries it.
	directTransmitINS = 0x00
	pn532Host         = 0xD4
	pn532Thru         = 0x42
	pn532Device       = 0xD5
	pn532ThruReply    = 0x43
	pn532StatusOK     = 0x00
	pn532StatusNoAns  = 0x01

	// frameACK is the 4-bit acknowledge a Type 2 tag answers a good WRITE with.
	frameACK = 0x0A
)

// emuSignature is the placeholder originality signature the emulated NTAG
// answers READ_SIG with.
func emuSignature() []byte {
	sig := make([]byte, 32)
	for i := range sig {
		sig[i] = byte(0xA0 + i)
	}
	return sig
}

// SupportsTransceiveRaw reports that the emulator carries framing-level frames
// (implements nfc.RawCardTransport).
func (e *memEmulator) SupportsTransceiveRaw() bool { return true }

// TransceiveRaw answers a Type 2 frame directly.
func (e *memEmulator) TransceiveRaw(frame []byte) ([]byte, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.shouldRemove() {
		e.present = false
		return nil, emuRemoved(e.removeAfterOp)
	}
	reply, ok := e.nativeFrame(frame)
	if !ok {
		return nil, errEmuNAK
	}
	return reply, nil
}

// nativeFrame answers one Type 2 frame, and reports false for one the tag NAKs.
// Caller holds e.mu.
func (e *memEmulator) nativeFrame(frame []byte) ([]byte, bool) {
	if len(frame) == 0 {
		return nil, false
	}
	switch frame[0] {
	case frameRead:
		if len(frame) != 2 {
			return nil, false
		}
		return e.readMem(int(frame[1]), 16)
	case frameFastRead:
		if len(frame) != 3 || frame[2] < frame[1] || e.signature == nil {
			return nil, false
		}
		return e.readMem(int(frame[1]), (int(frame[2])-int(frame[1])+1)*4)
	case frameWrite:
		if len(frame) != 6 {
			return nil, false
		}
		if e.failWrites > 0 {
			e.failWrites--
			return nil, false
		}
		if !e.writeMem(int(frame[1]), frame[2:]) {
			return nil, false
		}
		return []byte{frameACK}, true
	case frameReadSig:
		if len(frame) != 2 || frame[1] != 0x00 || e.signature == nil {
			return nil, false
		}
		return append([]byte(nil), e.signature...), true
	case frameReadCnt:
		if len(frame) != 2 || frame[1] != 0x02 || e.signature == nil {
			return nil, false
		}
		return []byte{0x00, 0x00, 0x00}, true
	}
	return nil, false
}

// directTransmit answers the Direct Transmit pseudo-APDU an ACR122 takes. Caller
// holds e.mu.
func (e *memEmulator) directTransmit(cmd []byte) []byte {
	lc := int(cmd[4])
	if len(cmd) != 5+lc || lc < 3 || cmd[5] != pn532Host || cmd[6] != pn532Thru {
		return emuFail()
	}
	reply, ok := e.nativeFrame(cmd[7:])
	status := byte(pn532StatusOK)
	if !ok {
		status = pn532StatusNoAns
		reply = nil
	}
	out := append([]byte{pn532Device, pn532ThruReply, status}, reply...)
	return append(out, nfc.SW1Success, nfc.SW2Success)
}

// SupportsTransceiveRaw and TransceiveRaw pass through to an emulator that
// carries frames, and refuse on one that does not, so the strict wrapper claims
// no more than the silicon behind it.
func (s strictTransport) SupportsTransceiveRaw() bool {
	raw, ok := s.inner.(nfc.RawCardTransport)
	return ok && raw.SupportsTransceiveRaw()
}

func (s strictTransport) TransceiveRaw(frame []byte) ([]byte, error) {
	raw, ok := s.inner.(nfc.RawCardTransport)
	if !ok {
		return nil, nfc.NewNotSupportedError("TransceiveRaw")
	}
	return raw.TransceiveRaw(frame)
}
