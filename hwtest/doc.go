// Package hwtest holds the tests that need a real reader and a real tag.
//
// They are behind the "hardware" build tag, so go test ./... and CI never run
// them:
//
//	go test -tags hardware -v -count=1 -run TestRawFraming ./hwtest
//	go test -tags hardware -v -count=1 -run TestLRP ./hwtest
//
// They drive the reader through the same code the agent uses (nfc/pcsc under an
// nfc.Supervisor), record every byte the reader exchanges with the tag, and
// write the recording as a fixture the unit tests can replay. The procedures
// themselves are in hwtest/scenario, which is also run against emulators in the
// normal test suite; the fixture format and replays are in hwtest/fixture.
//
// See README.md in this directory, and docs/hardware-tests.md, for the
// environment variables and what to send back.
package hwtest
