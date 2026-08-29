package nfc

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// The console decodes a command client-side, in TypeScript, so its explainer is
// a hand-written mirror of Explain. This is the contract that keeps the two from
// drifting: a shared fixture of commands and their decode, generated from Explain
// here and asserted against the TypeScript explainer in
// agent/console/frontend/src/apdu.explain.test.ts. Either side changing the
// decode without the other fails a test.
//
// Regenerate after a deliberate change to Explain:
//
//	UPDATE_APDU_FIXTURES=1 go test ./nfc/ -run TestExplainContract
//
// then run the console tests (npm test) to see whether the mirror needs the same
// change.

const explainFixturePath = "testdata/apdu_explain_cases.json"

// explainRecord is one line of the contract: an input and the decode both
// languages must agree on. It carries only the fields the TypeScript explainer
// also produces (Dialect and Fields are Go-only), so the fixture is exactly the
// shared surface.
type explainRecord struct {
	Hex        string   `json:"hex"`
	Raw        bool     `json:"raw"`
	Summary    string   `json:"summary"`
	Class      string   `json:"cls"`
	Mutating   bool     `json:"mutating"`
	Recognized bool     `json:"recognized"`
	Warnings   []string `json:"warnings"`
}

// explainContractInputs is the curated set of commands, chosen to reach every
// branch of the decoder: each dialect, each class, the direct-transmit unwrap,
// a lock-page write, a malformed length, an unknown command, and the short
// inputs.
var explainContractInputs = []struct {
	hex string
	raw bool
}{
	{"FFCA00000000", false},               // PC/SC GET UID
	{"FFB0000404", false},                 // PC/SC READ BINARY, page 4
	{"FFD6000404DEADBEEF", false},         // PC/SC UPDATE BINARY, page 4 (no lock page)
	{"FFD600030400000000", false},         // PC/SC UPDATE BINARY, page 3 (lock/OTP)
	{"FF82000006FFFFFFFFFFFF", false},     // PC/SC LOAD KEY
	{"FF860000050100046000", false},       // PC/SC GENERAL AUTHENTICATE
	{"FF0040A00401010101", false},         // ACR122 LED/buzzer escape
	{"FF000000016000", false},             // direct transmit -> GET_VERSION
	{"00A4040007D276000085010100", false}, // SELECT NDEF application
	{"00A4000C02E103", false},             // SELECT the CC file by identifier
	{"00B0000010", false},                 // ISO READ BINARY, 16 bytes
	{"00D6000504DEADBEEF", false},         // ISO UPDATE BINARY at offset 5
	{"9060000000", false},                 // DESFire GetVersion
	{"905A00000301020300", false},         // DESFire SelectApplication
	{"906A000000", false},                 // DESFire GetApplicationIDs
	{"00EE0000", false},                   // unknown ISO command
	{"00A4040005E10300", false},           // malformed: Lc says 5, 3 bytes follow
	{"3000", true},                        // native READ (framing)
	{"A20300000000", true},                // native WRITE to page 3 (framing, lock/OTP)
	{"60", true},                          // native GET_VERSION (framing, ambiguous)
	{"1BFFFFFFFF", true},                  // native PWD_AUTH (framing)
	{"FF", true},                          // unknown framing opcode
	{"", false},                           // empty command
	{"FFCA", false},                       // truncated APDU
}

// buildExplainRecords decodes every contract input through Explain and shapes it
// into the shared record. Warnings is never nil, so the JSON always has a list
// and the TypeScript side compares against [] rather than null.
func buildExplainRecords(t *testing.T) []explainRecord {
	t.Helper()
	out := make([]explainRecord, 0, len(explainContractInputs))
	for _, in := range explainContractInputs {
		cmd, err := HexToBytes(in.hex)
		if err != nil {
			t.Fatalf("bad fixture input %q: %v", in.hex, err)
		}
		e := Explain(cmd, in.raw)
		warnings := e.Warnings
		if warnings == nil {
			warnings = []string{}
		}
		out = append(out, explainRecord{
			Hex:        in.hex,
			Raw:        in.raw,
			Summary:    e.Summary,
			Class:      string(e.Class),
			Mutating:   e.Mutating,
			Recognized: e.Recognized,
			Warnings:   warnings,
		})
	}
	return out
}

// TestExplainContract keeps the committed fixture equal to what Explain produces
// now. With UPDATE_APDU_FIXTURES=1 it rewrites the fixture instead of asserting,
// which is how a deliberate change to Explain is rolled forward.
func TestExplainContract(t *testing.T) {
	got := buildExplainRecords(t)

	encoded, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	encoded = append(encoded, '\n')

	if os.Getenv("UPDATE_APDU_FIXTURES") == "1" {
		if err := os.MkdirAll(filepath.Dir(explainFixturePath), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(explainFixturePath, encoded, 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		t.Logf("wrote %s (%d cases)", explainFixturePath, len(got))
		return
	}

	raw, err := os.ReadFile(explainFixturePath)
	if err != nil {
		t.Fatalf("read fixture (regenerate with UPDATE_APDU_FIXTURES=1): %v", err)
	}
	var want []explainRecord
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("Explain no longer matches %s.\n"+
			"If this change to the decoder is intended, regenerate with\n"+
			"  UPDATE_APDU_FIXTURES=1 go test ./nfc/ -run TestExplainContract\n"+
			"and update the TypeScript mirror in agent/console/frontend/src/apdu.ts so its\n"+
			"test (npm test) passes against the same fixture.", explainFixturePath)
		// Point at the first divergence, so the failure is actionable.
		for i := range got {
			if i >= len(want) || !reflect.DeepEqual(got[i], want[i]) {
				t.Errorf("first divergence at case %d (%q):\n  now:   %+v", i, got[i].Hex, got[i])
				if i < len(want) {
					t.Errorf("  fixture: %+v", want[i])
				}
				break
			}
		}
	}
}
