package protocol

import "github.com/dotside-studios/davi-nfc-agent/nfc"

// TagCapabilities describes what operations a tag supports. It crosses the wire
// in both the device and client protocols, and is defined in the nfc package
// alongside the tags that report it.
type TagCapabilities = nfc.TagCapabilities
