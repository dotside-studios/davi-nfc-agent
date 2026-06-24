package nfc

// NewEmulatedTag wraps a CardTransport in the production tag driver for the
// given tag kind, so a custom or in-memory transport is driven by the real tag
// I/O (page/block/APDU logic, TLV framing, lock bytes) rather than a stand-in.
//
// This is the bridge that lets the nfctest emulators run production driver code
// without hardware: build an emulator (a CardTransport), wrap it here, and the
// returned Tag behaves as if it were a real card on a reader.
func NewEmulatedTag(transport CardTransport, uid string, kind DetectedTagType) Tag {
	switch kind {
	case DetectedClassic1K, DetectedClassic4K:
		return newPCSCClassicTag(transport, uid, kind)
	case DetectedUltralight, DetectedUltralightC, DetectedUltralightEV1:
		return newPCSCUltralightTag(transport, uid, kind)
	case DetectedNTAG213, DetectedNTAG215, DetectedNTAG216:
		return newPCSCNtagTag(transport, uid, kind)
	case DetectedDESFire:
		return newPCSCDESFireTag(transport, uid)
	default:
		return newPCSCISO14443Tag(transport, uid)
	}
}
