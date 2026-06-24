package nfctest

import (
	"bytes"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/nfc"
)

// allFamilies builds a fresh blank tag for every supported tag family.
func allFamilies() []struct {
	name string
	make func() nfc.Tag
} {
	const uid = "04A1B2C3D4E5F6"
	return []struct {
		name string
		make func() nfc.Tag
	}{
		{"NTAG213", func() nfc.Tag {
			return nfc.NewEmulatedTag(newNTAGEmulator(nfc.DetectedNTAG213), uid, nfc.DetectedNTAG213)
		}},
		{"NTAG215", func() nfc.Tag {
			return nfc.NewEmulatedTag(newNTAGEmulator(nfc.DetectedNTAG215), uid, nfc.DetectedNTAG215)
		}},
		{"NTAG216", func() nfc.Tag {
			return nfc.NewEmulatedTag(newNTAGEmulator(nfc.DetectedNTAG216), uid, nfc.DetectedNTAG216)
		}},
		{"Ultralight", func() nfc.Tag { return nfc.NewEmulatedTag(newUltralightEmulator(), uid, nfc.DetectedUltralight) }},
		{"UltralightC", func() nfc.Tag { return nfc.NewEmulatedTag(newUltralightCEmulator(), uid, nfc.DetectedUltralightC) }},
		{"Classic1K", func() nfc.Tag { return nfc.NewEmulatedTag(newClassicEmulator(), uid, nfc.DetectedClassic1K) }},
		{"DESFire", func() nfc.Tag { return nfc.NewEmulatedTag(newDESFireEmulator(), uid, nfc.DetectedDESFire) }},
	}
}

func buildMessage(t *testing.T, recs ...nfc.NDEFRecordBuilder) []byte {
	t.Helper()
	msg, err := (&nfc.NDEFMessageBuilder{Records: recs}).Build()
	if err != nil {
		t.Fatalf("build NDEF: %v", err)
	}
	data, err := msg.Encode()
	if err != nil {
		t.Fatalf("encode NDEF: %v", err)
	}
	return data
}

// roundTrip writes the encoded NDEF to the tag and reads it back, asserting the
// bytes survive exactly.
func roundTrip(t *testing.T, tag nfc.Tag, want []byte) {
	t.Helper()
	if err := tag.WriteData(want); err != nil {
		t.Fatalf("WriteData: %v", err)
	}
	got, err := tag.ReadData()
	if err != nil {
		t.Fatalf("ReadData: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("round-trip mismatch:\n want % X\n got  % X", want, got)
	}
}

// TestRecordMatrix_AllRecordTypesAllFamilies round-trips each NDEF record type
// through every tag family. Payloads are kept small so they fit even the
// smallest tag (original Ultralight).
func TestRecordMatrix_AllRecordTypesAllFamilies(t *testing.T) {
	records := []struct {
		name string
		rec  nfc.NDEFRecordBuilder
	}{
		{"Text", &nfc.NDEFText{Content: "hi", Language: "en"}},
		{"URI", &nfc.NDEFURI{Content: "https://x.co"}},
		{"MIME", &nfc.NDEFMIME{Type: "a/b", Data: []byte("xy")}},
		{"External", &nfc.NDEFExternal{Domain: "ex.co:a", Data: []byte("z")}},
	}
	for _, fam := range allFamilies() {
		for _, rc := range records {
			t.Run(fam.name+"/"+rc.name, func(t *testing.T) {
				roundTrip(t, fam.make(), buildMessage(t, rc.rec))
			})
		}
	}
}

// TestRecordMatrix_SmartPosterAndMultiRecord exercises a nested Smart Poster and
// a multi-record message on the larger-capacity families.
func TestRecordMatrix_SmartPosterAndMultiRecord(t *testing.T) {
	messages := []struct {
		name string
		recs []nfc.NDEFRecordBuilder
	}{
		{"SmartPoster", []nfc.NDEFRecordBuilder{&nfc.NDEFSmartPoster{URI: "https://example.com", Title: "Example"}}},
		{"MultiRecord", []nfc.NDEFRecordBuilder{
			&nfc.NDEFText{Content: "one", Language: "en"},
			&nfc.NDEFURI{Content: "https://two.example"},
		}},
	}
	larger := map[string]bool{"NTAG215": true, "NTAG216": true, "Classic1K": true, "DESFire": true}
	for _, fam := range allFamilies() {
		if !larger[fam.name] {
			continue
		}
		for _, m := range messages {
			t.Run(fam.name+"/"+m.name, func(t *testing.T) {
				roundTrip(t, fam.make(), buildMessage(t, m.recs...))
			})
		}
	}
}

// TestRecordMatrix_URIPrefixes round-trips URIs across several NFC Forum prefix
// abbreviations to exercise the prefix-optimization codec.
func TestRecordMatrix_URIPrefixes(t *testing.T) {
	uris := []string{
		"http://www.example.com",
		"https://www.example.com",
		"http://example.com",
		"https://example.com",
		"tel:+15551234567",
		"mailto:a@example.com",
		"ftp://ftp.example.com",
		"urn:nfc:ext:example.com:x",
	}
	for _, uri := range uris {
		t.Run(uri, func(t *testing.T) {
			tag := nfc.NewEmulatedTag(newNTAGEmulator(nfc.DetectedNTAG215), "04A1B2C3D4E5F6", nfc.DetectedNTAG215)
			roundTrip(t, tag, buildMessage(t, &nfc.NDEFURI{Content: uri}))
		})
	}
}
