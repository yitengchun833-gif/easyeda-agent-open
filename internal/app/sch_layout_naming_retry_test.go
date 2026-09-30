package app

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

func wireTreeNamingFixture() powerLayoutPlan {
	text := func(b layoutBBox) []layoutBBox {
		return []layoutBBox{{MinX: b.MinX + 1, MinY: b.MinY + 1, MaxX: b.MinX + 2, MaxY: b.MinY + 2}}
	}
	part := func(ref string, b layoutBBox) powerLayoutPlacement {
		return powerLayoutPlacement{Designator: ref, BBox: b, TextBBoxes: text(b)}
	}
	a := part("A1", layoutBBox{-20, -5, -10, 5})
	a.Pins = []powerLayoutPin{{Number: "1", Net: "N", X: 0, Y: 0}}
	b := part("B1", layoutBBox{210, -5, 220, 5})
	b.Pins = []powerLayoutPin{{Number: "1", Net: "N", X: 200, Y: 0}}
	return powerLayoutPlan{
		Placements: []powerLayoutPlacement{
			a, b,
			part("LT", layoutBBox{4, 4, 51, 60}),
			part("LB", layoutBBox{4, -60, 51, -4}),
			part("RT", layoutBBox{149, 4, 196, 60}),
			part("RB", layoutBBox{149, -60, 196, -4}),
		},
		Wires: []powerLayoutWire{{Net: "N", Points: [][2]float64{{0, 0}, {200, 0}}}},
	}
}

func TestWireTreeMarkerNamesCongestedDirectIsland(t *testing.T) {
	p := wireTreeNamingFixture()
	if err := validateLibGeometry(&p); err != nil {
		t.Fatal(err)
	}
	for _, pin := range []powerLayoutPin{p.Placements[0].Pins[0], p.Placements[1].Pins[0]} {
		trial := p
		if libPlaceMarker(&trial, pin, "net_port_bi") {
			t.Fatalf("fixture pin unexpectedly admits naming lead: %+v flags=%+v wires=%+v", pin, trial.Flags, trial.Wires)
		}
	}
	beforePlacements, beforeWires := append([]powerLayoutPlacement(nil), p.Placements...), append([]powerLayoutWire(nil), p.Wires...)
	islands := libIslands(&p)
	if len(islands) != 1 || !libPlaceWireTreeMarker(&p, islands[0], "net_port_bi") {
		t.Fatal("connected wire tree was not named")
	}
	if !reflect.DeepEqual(beforePlacements, p.Placements) || !reflect.DeepEqual(beforeWires, p.Wires) {
		t.Fatal("tree naming changed placement or physical routing")
	}
	if len(p.Flags) != 1 || p.Flags[0].PinX != 100 || p.Flags[0].PinY != 0 {
		t.Fatal("marker did not use the legal tree midpoint", p.Flags)
	}
	if err := validateLibGeometry(&p); err != nil {
		t.Fatal(err)
	}
	if err := validateSchCompositionNets(&p); err != nil {
		t.Fatal(err)
	}
}

func TestWireTreeMarkerRejectsForeignEndpointContacts(t *testing.T) {
	p := wireTreeNamingFixture()
	p.Wires = append(p.Wires,
		powerLayoutWire{Net: "X", Points: [][2]float64{{100, 10}, {130, 10}}},
		powerLayoutWire{Net: "Y", Points: [][2]float64{{100, -10}, {130, -10}}},
	)
	before, _ := json.Marshal(p)
	islands := libIslands(&p)
	var island *libIsland
	for i := range islands {
		if islands[i].net == "N" {
			island = &islands[i]
		}
	}
	if island == nil {
		t.Fatal("missing N island")
	}
	if libPlaceWireTreeMarker(&p, *island, "net_port_bi") {
		t.Fatal("foreign endpoint/T contact was accepted")
	}
	after, _ := json.Marshal(p)
	if string(before) != string(after) {
		t.Fatal("failed tree naming modified source")
	}
}

func TestInterleavedPortsMayKeepNamedIslandsButDirectMustJoin(t *testing.T) {
	// Synthetic four-pin connector, independent of any vendor/training circuit.
	in := SchematicLayoutInput{SchemaVersion: 1, CoreComponentID: "connector", NetPolicies: map[string]string{"A": "module_port", "B": "module_port"}, Components: []SchematicLayoutComponent{{ID: "connector", Measurement: SchematicPlacement{Designator: "J1", BBox: SchematicBox{-10, -30, 10, 30}, Pins: []SchematicPin{{Number: "1", Net: "A", X: 30, Y: 15}, {Number: "2", Net: "B", X: 30, Y: 5}, {Number: "3", Net: "A", X: 30, Y: -5}, {Number: "4", Net: "B", X: 30, Y: -15}}}}}}
	before, _ := json.Marshal(in)
	out, err := PlanSchematicLayout(in)
	if err != nil {
		t.Fatal(err)
	}
	p := powerLayoutPlan{Placements: out.Placements, Wires: out.Wires, Flags: out.Flags}
	if err = validateLibGeometry(&p); err != nil {
		t.Fatal(err)
	}
	if err = validateSchCompositionNets(&p); err != nil {
		t.Fatal(err)
	}
	pins, err := schematicVariantPhysicalPinIslands(out)
	if err != nil {
		t.Fatal(err)
	}
	a1, a3 := pins[schematicVariantPinIdentity{"connector", "1"}], pins[schematicVariantPinIdentity{"connector", "3"}]
	b2, b4 := pins[schematicVariantPinIdentity{"connector", "2"}], pins[schematicVariantPinIdentity{"connector", "4"}]
	if a1 != a3 || b2 != b4 || a1.root == b2.root {
		t.Fatal("same-side repeated module ports were not joined into distinct local trees", pins)
	}
	again, err := PlanSchematicLayout(in)
	if err != nil || !reflect.DeepEqual(out, again) {
		t.Fatal("nondeterministic", err)
	}
	after, _ := json.Marshal(in)
	if string(before) != string(after) {
		t.Fatal("modified evidence")
	}
	in.NetPolicies["A"] = "direct"
	in.NetPolicies["B"] = "direct"
	direct, err := PlanSchematicLayout(in)
	if err != nil {
		t.Fatal("verified proper crossings should permit direct ABAB", err)
	}
	directPins, err := schematicVariantPhysicalPinIslands(direct)
	if err != nil {
		t.Fatal(err)
	}
	a, b := directPins[schematicVariantPinIdentity{"connector", "1"}], directPins[schematicVariantPinIdentity{"connector", "2"}]
	if a != directPins[schematicVariantPinIdentity{"connector", "3"}] || b != directPins[schematicVariantPinIdentity{"connector", "4"}] || a.root == b.root {
		t.Fatal("direct ABAB split or shorted", directPins)
	}
}

func TestNamingBudgetExhaustionDoesNotPublishPartial(t *testing.T) {
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{{Designator: "U1", BBox: layoutBBox{-20, -45, 20, 45}, Pins: []powerLayoutPin{{Number: "1", Net: "R", X: 35, Y: 0}, {Number: "2", Net: "S", X: 35, Y: 10}}}}}
	before, _ := json.Marshal(p)
	budget := 1
	err := libNameIslands(&p, map[string]string{"R": "local_ground", "S": "local_power"}, &budget)
	if !errors.Is(err, errLibLayoutBudget) || budget != 0 {
		t.Fatal(err, budget)
	}
	after, _ := json.Marshal(p)
	if string(before) != string(after) {
		t.Fatal("partial naming escaped on exhaustion")
	}
}

func TestDenseMixedNamingRetriesPreserveGeometry(t *testing.T) {
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{{Designator: "U1", BBox: layoutBBox{-20, -45, 20, 45}, Pins: []powerLayoutPin{{Number: "1", Net: "RETURN", X: 35, Y: 0}, {Number: "2", Net: "SUPPLY", X: 35, Y: 10}, {Number: "3", Net: "CONTROL", X: 35, Y: 20}}}}}
	original := p.Placements
	if err := libNameIslands(&p, map[string]string{"RETURN": "local_ground", "SUPPLY": "local_power", "CONTROL": "module_port"}); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, p.Placements) {
		t.Fatal("naming moved component")
	}
	if err := validateLibGeometry(&p); err != nil {
		t.Fatal(err)
	}
	if err := validateSchCompositionNets(&p); err != nil {
		t.Fatal(err)
	}
	if len(p.Flags) != 3 {
		t.Fatal("incomplete naming")
	}
}
