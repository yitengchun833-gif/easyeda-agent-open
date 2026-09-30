package app

import "testing"

func TestPowerLayoutApplyGatesBeforeWiresAndSavesAfterTopology(t *testing.T) {
	f := powerLayoutFixture(t, 0, 0, 20, 0)
	f["result"].(map[string]any)["wires"] = []any{}
	f["result"].(map[string]any)["connectivitySummary"] = map[string]any{"wires": 0}
	raw := powerLayoutBytes(t, f)
	plan, err := planPowerLayout(raw, powerLayoutTestOptions())
	if err != nil {
		t.Fatal(err)
	}
	pb, err := powerLayoutPlaybook(plan, raw)
	if err != nil {
		t.Fatal(err)
	}
	positionChecked, topologyChecked := false, false
	for _, s := range pb.Steps {
		if s.ID == "verify-position-before-wiring" {
			positionChecked = s.ExpectSchematic != nil
		}
		if s.Action == "schematic.wire.create" && !positionChecked {
			t.Fatal("wire precedes measured pin verification")
		}
		if s.Action == "schematic.wire.create" {
			if _, named := s.Payload["net"]; named {
				t.Fatal("do not duplicate wire net-name attributes; local symbols name the connected trees")
			}
		}
		if s.ID == "verify-all-pin-nets" {
			topologyChecked = true
			for _, c := range plan.Placements {
				for _, p := range c.Pins {
					e := s.ExpectSchematic.Parts[c.Designator].Pins[p.Number]
					if e.Net == nil || *e.Net != p.Net {
						t.Fatalf("missing golden net for %s.%s", c.Designator, p.Number)
					}
				}
			}
		}
		if s.Action == "schematic.save" && !topologyChecked {
			t.Fatal("verified save precedes golden net check")
		}
	}
	if !positionChecked || !topologyChecked {
		t.Fatal("generated Apply lacks data gates")
	}
}

func TestPowerLayoutApplyNeverDeletesPartsAndRejectsAnonymousWires(t *testing.T) {
	f := powerLayoutFixture(t, 0, 0, 20, 0)
	r := f["result"].(map[string]any)
	r["wires"] = []any{map[string]any{"primitiveId": "old-wire"}}
	r["connectivitySummary"] = map[string]any{"wires": 1}
	r["components"] = append(r["components"].([]any), map[string]any{"componentType": "netflag", "primitiveId": "old-flag"})
	raw := powerLayoutBytes(t, f)
	plan, err := planPowerLayout(raw, powerLayoutTestOptions())
	if err != nil {
		t.Fatal(err)
	}
	pb, err := powerLayoutPlaybook(plan, raw)
	if err != nil {
		t.Fatal(err)
	}
	deleted := map[string]bool{}
	for _, s := range pb.Steps {
		if s.Action == "schematic.primitives.delete" {
			for _, id := range s.Payload["primitiveIds"].([]string) {
				deleted[id] = true
			}
		}
	}
	if len(deleted) != 2 || !deleted["old-wire"] || !deleted["old-flag"] {
		t.Fatalf("cleanup must contain only the old connections: %v", deleted)
	}
	r["wires"] = []any{map[string]any{"x0": 1, "x1": 2, "y0": 3, "y1": 3}}
	if _, err = powerLayoutPlaybook(plan, powerLayoutBytes(t, f)); err == nil {
		t.Fatal("wire without fresh primitive ID must not produce destructive Apply")
	}
	delete(r, "connectivitySummary")
	if _, err = powerLayoutPlaybook(plan, powerLayoutBytes(t, f)); err == nil {
		t.Fatal("missing inventory must never imply a clean page")
	}
}
