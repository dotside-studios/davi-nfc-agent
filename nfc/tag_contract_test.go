package nfc

import "testing"

// pcscTagKinds is every tag kind the reader path can build a driver for.
var pcscTagKinds = []DetectedTagType{
	DetectedClassic1K,
	DetectedClassic4K,
	DetectedUltralight,
	DetectedUltralightC,
	DetectedUltralightEV1,
	DetectedUltralightEV1_128,
	DetectedNTAG213,
	DetectedNTAG215,
	DetectedNTAG216,
	DetectedDESFire,
	DetectedISO14443_4,
}

// TestEveryDrivenKindHasAProfile keeps the profile table and the tag factory in
// step. A kind the factory can build but the table does not describe would fall
// back to guessing capabilities from a display name, which is what the profiles
// exist to stop.
func TestEveryDrivenKindHasAProfile(t *testing.T) {
	for _, kind := range pcscTagKinds {
		if _, ok := profileFor(kind); !ok {
			t.Errorf("kind %v has a driver but no profile", kind)
		}
	}
}

// TestProfilesAreComplete checks the fields every tag reports upward: a name to
// show, a numeric type, and a technology, which reaches clients with each scan.
func TestProfilesAreComplete(t *testing.T) {
	for kind, profile := range tagProfiles {
		if profile.name == "" {
			t.Errorf("kind %v has no display name", kind)
		}
		if profile.technology == "" {
			t.Errorf("%s has no technology", profile.name)
		}
		if profile.numericType < 0 {
			t.Errorf("%s has no numeric type", profile.name)
		}
		if profile.family == "" {
			t.Errorf("%s has no family", profile.name)
		}
	}
}

// TestDeclaredCapacityFitsTheDriver is the invariant behind MaxNDEFSize: a tag
// must never advertise more room than its driver is willing to write, or
// Reader.WriteMessage lets through a message the write then fails on.
func TestDeclaredCapacityFitsTheDriver(t *testing.T) {
	ultralightKinds := []DetectedTagType{
		DetectedUltralight, DetectedUltralightC, DetectedUltralightEV1, DetectedUltralightEV1_128,
	}
	for _, kind := range ultralightKinds {
		profile := tagProfiles[kind]
		layout := ultralightLayoutFor(kind)

		if writable := layout.writablePages*4 - 2; profile.maxNDEFSize > writable {
			t.Errorf("%v declares MaxNDEFSize=%d but its driver writes at most %d bytes",
				kind, profile.maxNDEFSize, writable)
		}
		if profile.canLock != layout.lockable {
			t.Errorf("%v declares CanLock=%v but its layout says %v", kind, profile.canLock, layout.lockable)
		}
	}
}

// TestCardTakesTechnologyFromTheTag guards the technology a scan carries to
// clients. It used to be re-derived from the tag's display name, which had no
// case for NTAG, so every NTAG scan reported "Unknown".
func TestCardTakesTechnologyFromTheTag(t *testing.T) {
	for _, kind := range pcscTagKinds {
		tag := NewTagForType(kind, &stubTransport{}, "04112233445566")

		t.Run(tag.Type(), func(t *testing.T) {
			card := NewCard(tag)
			want := GetTagCapabilities(tag).Technology
			if card.Technology != want {
				t.Errorf("Card.Technology = %q, want the tag's own %q", card.Technology, want)
			}
			if card.Technology == "" || card.Technology == "Unknown" {
				t.Errorf("Card.Technology = %q", card.Technology)
			}
		})
	}
}
