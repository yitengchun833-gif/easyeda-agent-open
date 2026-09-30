package app

import (
	"encoding/json"
	"strings"
	"testing"
)

func drawingFixture(t *testing.T) (*schematicDrawingExpectation, map[string]any) {
	t.Helper()
	e := &schematicDrawingExpectation{
		Wires: []powerLayoutWire{{Net: "GND", Points: [][2]float64{{0, 20}, {40, 20}}}},
		Flags: []powerLayoutFlag{{Net: "GND", Kind: "ground", PinX: 40, PinY: 20, Direction: "down", Offset: 20}},
	}
	var live map[string]any
	if err := json.Unmarshal([]byte(`{"components":[{"componentType":"sheet"},{"componentType":"part"},{"componentType":"netflag","net":"GND","x":40,"y":0,"rotation":0}],"wires":[{"x0":0,"y0":20,"x1":40,"y1":20,"net":"GND"},{"x0":40,"y0":20,"x1":40,"y1":0,"net":"GND"}]}`), &live); err != nil {
		t.Fatal(err)
	}
	live["connectivitySummary"] = emptyDrawingSummary()
	return e, live
}

func emptyDrawingSummary() map[string]any {
	return map[string]any{"scope": "activePage", "buses": 0.0, "shortSymbols": 0.0}
}

func drawingWire(x0, y0, x1, y1 float64) map[string]any {
	return map[string]any{"x0": x0, "y0": y0, "x1": x1, "y1": y1, "net": "GND"}
}

func TestSchematicDrawingSplitMergedAndReversedPathsEquivalent(t *testing.T) {
	e, live := drawingFixture(t)
	if err := e.check(live); err != nil {
		t.Fatalf("exact measured drawing rejected: %v", err)
	}
	live["wires"] = []any{drawingWire(40, 20, 15, 20), drawingWire(0, 20, 15, 20), drawingWire(40, 0, 40, 5), drawingWire(40, 20, 40, 5)}
	if err := e.check(live); err != nil {
		t.Fatalf("editor splitting/reversing an identical conductor path must pass: %v", err)
	}
	// The authored polyline and an editor's separate segments cover the same
	// edges even when the marker stub merges into the horizontal wire.
	e.Wires = []powerLayoutWire{{Net: "GND", Points: [][2]float64{{0, 20}, {15, 20}, {40, 20}}}}
	live["wires"] = []any{drawingWire(0, 20, 40, 20), drawingWire(40, 0, 40, 20)}
	if err := e.check(live); err != nil {
		t.Fatalf("merged collinear source polyline must pass: %v", err)
	}
}

func TestSchematicDrawingSameNetDetourDoesNotMatch(t *testing.T) {
	e, live := drawingFixture(t)
	// Same terminal coordinates, same GND marker and same net name, but the
	// wire loops over the module instead of using the authored short segment.
	live["wires"] = []any{drawingWire(0, 20, 0, 40), drawingWire(0, 40, 40, 40), drawingWire(40, 40, 40, 20), drawingWire(40, 20, 40, 0)}
	if err := e.check(live); err == nil || !strings.Contains(err.Error(), "wire paths differ") {
		t.Fatalf("equal electrical topology must not hide a detour: %v", err)
	}
}

func TestSchematicDrawingRejectsChangedOrExtraMarkers(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any, map[string]any)
	}{
		{"reversed", func(_ map[string]any, marker map[string]any) { marker["rotation"] = 180.0 }},
		{"moved", func(_ map[string]any, marker map[string]any) { marker["x"] = 45.0 }},
		{"renamed", func(_ map[string]any, marker map[string]any) { marker["net"] = "+3V3" }},
		{"duplicate-same-net", func(live map[string]any, marker map[string]any) {
			live["components"] = append(live["components"].([]any), marker)
		}},
		{"unavailable-rotation", func(_ map[string]any, marker map[string]any) { delete(marker, "rotation") }},
		{"label-instead-of-flag", func(_ map[string]any, marker map[string]any) { marker["componentType"] = "netlabel" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, live := drawingFixture(t)
			marker := live["components"].([]any)[2].(map[string]any)
			tc.edit(live, marker)
			if err := e.check(live); err == nil {
				t.Fatal("drawing mismatch was accepted")
			}
		})
	}
}

func TestSchematicDrawingRequiresWireGeometry(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"absent-inventory", func(live map[string]any) { delete(live, "wires") }},
		{"null-inventory", func(live map[string]any) { live["wires"] = nil }},
		{"absent-coordinate", func(live map[string]any) { delete(live["wires"].([]any)[0].(map[string]any), "x0") }},
		{"null-coordinate", func(live map[string]any) { live["wires"].([]any)[0].(map[string]any)["x0"] = nil }},
		{"diagonal", func(live map[string]any) { live["wires"].([]any)[0].(map[string]any)["y1"] = 25.0 }},
		{"off-grid", func(live map[string]any) { live["wires"].([]any)[0].(map[string]any)["x0"] = 0.5 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, live := drawingFixture(t)
			tc.edit(live)
			if err := e.check(live); err == nil {
				t.Fatal("missing or invalid geometry passed")
			}
		})
	}
	// Only an explicitly measured empty array proves that there are no wires.
	empty := &schematicDrawingExpectation{}
	if err := empty.check(map[string]any{"connectivitySummary": emptyDrawingSummary(), "wires": []any{}, "components": []any{}}); err != nil {
		t.Fatalf("known empty drawing rejected: %v", err)
	}
}

func TestSchematicDrawingNetPortUsesStoredRotation(t *testing.T) {
	// These are the published, calibrated stored rotations in orientation.json.
	for _, tc := range []struct {
		direction string
		x, y, rot float64
	}{{"right", 40, 20, 0}, {"up", 20, 40, 90}, {"left", 0, 20, 180}, {"down", 20, 0, 270}} {
		t.Run(tc.direction, func(t *testing.T) {
			e := &schematicDrawingExpectation{Flags: []powerLayoutFlag{{Net: "SIGNAL", Kind: "net_port_bi", PinX: 20, PinY: 20, Direction: tc.direction, Offset: 20}}}
			live := map[string]any{"connectivitySummary": emptyDrawingSummary(), "wires": []any{drawingWire(20, 20, tc.x, tc.y)}, "components": []any{map[string]any{"componentType": "netport", "net": "SIGNAL", "x": tc.x, "y": tc.y, "rotation": tc.rot}}}
			if err := e.check(live); err != nil {
				t.Fatalf("calibrated netport direction rejected: %v", err)
			}
		})
	}
}

func TestSchematicDrawingGateRequiresWireReadFlag(t *testing.T) {
	e, _ := drawingFixture(t)
	s := playbookStep{Action: "schematic.components.list", Payload: map[string]any{"includePins": true}, ExpectSchematic: &schematicStateExpectation{ExactParts: true, Parts: map[string]schematicPartExpectation{}, Drawing: e}}
	if err := validateSchematicExpectationStep(&s); err == nil {
		t.Fatal("drawing expectation without a wire inventory passed preflight")
	}
	s.Payload["includeWires"] = true
	if err := validateSchematicExpectationStep(&s); err == nil {
		t.Fatal("drawing requires summary inventory")
	}
	s.Payload["includeConnectivitySummary"] = true
	if err := validateSchematicExpectationStep(&s); err != nil {
		t.Fatal(err)
	}
}

func TestSchematicDrawingRejectsUnsupportedOrUnknownPrimitives(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"extra bus", func(r map[string]any) { r["connectivitySummary"].(map[string]any)["buses"] = 1.0 }},
		{"extra short symbol", func(r map[string]any) { r["connectivitySummary"].(map[string]any)["shortSymbols"] = 1.0 }},
		{"missing summary", func(r map[string]any) { delete(r, "connectivitySummary") }},
		{"missing bus count", func(r map[string]any) { delete(r["connectivitySummary"].(map[string]any), "buses") }},
		{"missing short count", func(r map[string]any) { delete(r["connectivitySummary"].(map[string]any), "shortSymbols") }},
		{"wrong scope", func(r map[string]any) { r["connectivitySummary"].(map[string]any)["scope"] = "allPages" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, r := drawingFixture(t)
			tc.edit(r)
			if err := e.check(r); err == nil {
				t.Fatal("identical wire/marker geometry masked unsupported or unknown primitives")
			}
		})
	}
}
