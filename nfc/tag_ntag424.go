package nfc

// pcscNTAG424Tag is an NTAG 424 DNA.
//
// Its NDEF file is an ordinary NFC Forum Type 4 file, so the NDEF path is the
// Type 4 driver's. This type supplies the identity: the name, the 416-byte
// layout, and the 254-byte NDEF ceiling that lets an oversized write be refused
// before it reaches the card.
//
// The card's AES commands (authentication, file settings, SDM) are not
// implemented. The profile's supportsAuthentication describes the card, not
// this driver; callers that need those commands send them over the raw APDU
// channel.
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
