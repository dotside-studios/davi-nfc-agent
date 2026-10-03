package clientserver

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/dotside-studios/davi-nfc-agent/protocol"
	"github.com/dotside-studios/davi-nfc-agent/server"
)

// The Control Center lays a planSDM answer out for display in TypeScript. Its
// tests run against this fixture, which is what planSDM answers now, so a
// change to the planner or the wire shape reaches them.
const planFixturePath = "testdata/ntag424_plan_cases.json"

type planCase struct {
	Name     string                           `json:"name"`
	Request  protocol.NTAG424RequestPayload   `json:"request"`
	Response *protocol.NTAG424ResponsePayload `json:"response"`
}

func planCases() []planCase {
	right := func(n int) *int { return &n }
	return []planCase{
		{Name: "picc and mac", Request: protocol.NTAG424RequestPayload{
			Op: "planSDM", URLTemplate: "https://example.com/t?p={picc}&m={mac}",
		}},
		{Name: "uid and counter in the clear", Request: protocol.NTAG424RequestPayload{
			Op: "planSDM", URLTemplate: "https://example.com/t?uid={uid}&ctr={ctr}&m={mac}",
		}},
		{Name: "encrypted file data", Request: protocol.NTAG424RequestPayload{
			Op: "planSDM", URLTemplate: "https://example.com/t?p={picc}&e={enc}&m={mac}",
			SDM: &protocol.NTAG424SDMOptions{EncLength: 64, FileRead: right(1), MetaRead: right(1)},
		}},
		{Name: "http with www prefix", Request: protocol.NTAG424RequestPayload{
			Op: "planSDM", URLTemplate: "http://www.example.com/{picc}{mac}",
		}},
		{Name: "no prefix abbreviation", Request: protocol.NTAG424RequestPayload{
			Op: "planSDM", URLTemplate: "ftp://example.com/{picc}{mac}",
		}},
	}
}

func TestNTAG424PlanContract(t *testing.T) {
	s := newTagOps(Config{})

	got := planCases()
	for i := range got {
		res, err := s.NTAG424(context.Background(), server.NTAG424Op{Request: got[i].Request})
		if err != nil {
			t.Fatalf("%s: %v", got[i].Name, err)
		}
		got[i].Response = res
	}

	encoded, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	encoded = append(encoded, '\n')

	if os.Getenv("UPDATE_NTAG424_PLAN_FIXTURES") == "1" {
		if err := os.MkdirAll(filepath.Dir(planFixturePath), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(planFixturePath, encoded, 0o644); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
		t.Logf("wrote %s (%d cases)", planFixturePath, len(got))
		return
	}

	raw, err := os.ReadFile(planFixturePath)
	if err != nil {
		t.Fatalf("read fixture (regenerate with UPDATE_NTAG424_PLAN_FIXTURES=1): %v", err)
	}
	var want []planCase
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("planSDM no longer matches %s. If the change is intended, regenerate with\n"+
			"  UPDATE_NTAG424_PLAN_FIXTURES=1 go test ./server/clientserver -run TestNTAG424PlanContract\n"+
			"and check the console's tests (agent/console/frontend, npm test) against it.", planFixturePath)
	}
}
