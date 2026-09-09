package nfc

// pcscNTAG424Tag is an NTAG 424 DNA.
//
// Its NDEF file is an ordinary NFC Forum Type 4 file — selected by identifier,
// read and written with READ BINARY and UPDATE BINARY — so the whole NDEF path
// is the Type 4 driver's, unchanged. What this type adds is knowing which card
// it is: the name it reports, the 416-byte layout, and the 254-byte NDEF
// ceiling that lets a write be refused before it reaches the card rather than
// failing partway through with 6A84.
//
// The card's own command set — AES authentication, file settings, SDM — is not
// implemented. The profile advertises that the card supports authentication,
// which is true of the card and not a claim about this driver; a caller that
// needs those commands sends them over the raw APDU channel.
type pcscNTAG424Tag struct {
	pcscISO14443Tag
}

func newPCSCNTAG424Tag(dev CardTransport, uid string) *pcscNTAG424Tag {
	return &pcscNTAG424Tag{
		pcscISO14443Tag: pcscISO14443Tag{
			pcscBaseTag: pcscBaseTag{
				device:       dev,
				uid:          uid,
				detectedType: DetectedNTAG424,
			},
		},
	}
}
