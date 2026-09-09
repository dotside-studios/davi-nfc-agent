package ntag424_test

import (
	"errors"
	"fmt"

	"github.com/dotside-studios/davi-nfc-agent/nfc/ntag424"
)

// A backend receives the URL a tag served and checks that it came from one of
// its tags. The keys here are the factory default; a deployed tag has its own.
func ExampleVerifyURL() {
	keys := ntag424.Keys{
		MetaRead: make([]byte, ntag424.KeySize),
		FileRead: make([]byte, ntag424.KeySize),
	}

	tap, err := ntag424.VerifyURL(
		"https://ntag.nxp.com/424?e=EF963FF7828658A599F3041510671E88&c=94EED9EE65337086",
		keys,
	)
	if err != nil {
		if errors.Is(err, ntag424.ErrMACMismatch) {
			fmt.Println("not one of our tags")
			return
		}
		fmt.Println("not a tap:", err)
		return
	}

	// The MAC proves the tag wrote this URL. It does not prove the URL is
	// fresh, so the counter is compared against the last tap recorded for this
	// tag; anything that does not advance is a replay.
	fmt.Printf("tag %s, read %d\n", tap.UIDString(), tap.ReadCounter)

	// Output:
	// tag 04DE5F1EACC040, read 61
}
