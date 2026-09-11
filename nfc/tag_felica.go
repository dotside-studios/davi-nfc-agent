package nfc

// FeliCa, read for its identifier alone.
//
// A FeliCa is ISO 18092 rather than ISO 14443, and speaks its own command set:
// Polling, Read Without Encryption, Write Without Encryption, addressed by
// service and block. None of that travels as an ISO 7816 APDU, and a PC/SC
// reader carries it only through a vendor escape that differs per reader, so
// this driver sends no FeliCa command at all.
//
// What it does have is the IDm, which a PC/SC reader returns for the standard
// Get Data command every other tag here answers, so a FeliCa scans with its
// identity, its type and its technology, and nothing else. That is the whole of
// a transit card or an access badge as far as this agent is concerned: their
// contents sit behind issuer keys and would not be readable even with the
// commands implemented.
//
// A FeliCa carrying an NDEF message is an NFC Forum Type 3 tag, whose contents
// this cannot reach either. Reading one needs the command set above.

type pcscFeliCaTag struct {
	BaseTag

	uid string
}

func newPCSCFeliCaTag(uid string) *pcscFeliCaTag {
	return &pcscFeliCaTag{uid: uid}
}

func (t *pcscFeliCaTag) UID() string {
	return t.uid
}

func (t *pcscFeliCaTag) Type() string {
	return tagProfiles[DetectedFeliCa].name
}

func (t *pcscFeliCaTag) NumericType() int {
	return tagProfiles[DetectedFeliCa].numericType
}

func (t *pcscFeliCaTag) Capabilities() TagCapabilities {
	return tagProfiles[DetectedFeliCa].capabilities()
}

// ReadData declines, because this driver holds no way to read a FeliCa's
// memory. The card is present and answering; there is simply nothing here that
// can ask it for anything.
func (t *pcscFeliCaTag) ReadData() ([]byte, error) {
	return nil, NewNoPayloadError("ReadData (FeliCa)", t.uid, nil)
}
