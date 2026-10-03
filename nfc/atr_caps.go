package nfc

// ATRExtendedLength reads whether the card declares support for extended Lc and
// Le fields in the historical bytes of its ATR. The card capabilities data
// object (compact TLV tag 7, length 3) ends in the third software function
// table byte, whose bit b7 is set for extended Lc and Le.
//
// known is false when the ATR carries no card capabilities, in which case
// supported is false and says nothing about the card: many cards that take
// extended APDUs never say so. The reader's own support is not in the ATR and
// is not reported here.
func ATRExtendedLength(atr []byte) (supported, known bool) {
	start := findHistoricalBytesStart(atr)
	if start < 0 || start >= len(atr) {
		return false, false
	}
	hist := atr[start:min(len(atr), start+int(atr[1]&0x0F))]
	if len(hist) < 2 {
		return false, false
	}

	var tlv []byte
	switch hist[0] {
	case 0x00:
		if len(hist) < 4 {
			return false, false
		}
		tlv = hist[1 : len(hist)-3]
	case 0x80:
		tlv = hist[1:]
	default:
		return false, false
	}

	for len(tlv) > 0 {
		tag, length := tlv[0]>>4, int(tlv[0]&0x0F)
		if 1+length > len(tlv) {
			return false, false
		}
		if tag == 0x7 && length == 3 {
			return tlv[3]&0x40 != 0, true
		}
		tlv = tlv[1+length:]
	}
	return false, false
}
