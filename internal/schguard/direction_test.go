package schguard

import (
	"encoding/json"
	"os"
	"testing"
)

func TestLiveHaloExceptionOnlyOwnOutwardPin(t *testing.T) {
	s := scene(0, wire(20, 0, 30, 0))
	c := s["components"].([]any)[0].(map[string]any)
	c["bbox"] = map[string]any{"minX": -10.5, "minY": -10.5, "maxX": 20.5, "maxY": 10.5}
	if f := AnalyzeWireGeometry(s); len(f) != 0 {
		t.Fatalf("own outward stroke-halo exit rejected: %+v", f)
	}
	s["wires"] = []any{wire(20, 5, 30, 5)}
	if f := AnalyzeWireGeometry(s); count(f, "wire-through-body") != 1 {
		t.Fatal("unrelated wire got halo exemption", f)
	}
	s["wires"] = []any{wire(20, 0, 10, 0)}
	if f := AnalyzeWireGeometry(s); count(f, "wire-through-body") != 1 || count(f, "pin-exit-direction") != 1 {
		t.Fatal("inward pin got halo exemption", f)
	}
}

func TestP1D1Pin3ReversedFixture(t *testing.T) {
	raw, err := os.ReadFile("testdata/d1-pin3-reversed.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Result map[string]any `json:"result"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	findings := AnalyzeWireGeometry(fixture.Result)
	if count(findings, "pin-exit-direction") != 1 || count(findings, "wire-through-body") != 1 {
		t.Fatalf("D1.3 regression fixture no longer detects both data errors: %+v", findings)
	}
}
