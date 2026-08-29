package virtualnfc

import "github.com/dotside-studios/davi-nfc-agent/nfc"

// These helpers turn declared content into an *nfc.NDEFMessage, wrapping
// nfc.NDEFMessageBuilder so a virtual card's content can be built in one call.

// Message builds an NDEF message from the given records.
func Message(records ...nfc.NDEFRecordBuilder) (*nfc.NDEFMessage, error) {
	return (&nfc.NDEFMessageBuilder{Records: records}).Build()
}

// TextMessage builds a single-record text message. An empty language defaults to
// "en", matching the tag drivers.
func TextMessage(text, language string) (*nfc.NDEFMessage, error) {
	if language == "" {
		language = "en"
	}
	return Message(&nfc.NDEFText{Content: text, Language: language})
}

// URIMessage builds a single-record URI message.
func URIMessage(uri string) (*nfc.NDEFMessage, error) {
	return Message(&nfc.NDEFURI{Content: uri})
}
