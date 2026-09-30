package app

import (
	"errors"
	"strings"
	"testing"
)

func TestMandatoryDirectPlacementRejectsLocallyEnclosedIsland(t *testing.T) {
	connector := powerLayoutPlacement{Designator: "J1", BBox: layoutBBox{-20, -25, 0, 25}, Pins: []powerLayoutPin{
		{Number: "F1", Net: "FOREIGN_TOP", X: 30, Y: 5, Rotation: mazeTestRotation(0)},
		{Number: "N", Net: "MUST", X: 30, Y: 0, Rotation: mazeTestRotation(0)},
		{Number: "F2", Net: "FOREIGN_BOTTOM", X: 30, Y: -5, Rotation: mazeTestRotation(0)},
		{Number: "N2", Net: "MUST", X: 30, Y: 20, Rotation: mazeTestRotation(0)},
	}}
	before := powerLayoutPlan{Placements: []powerLayoutPlacement{connector}}
	blocker := powerLayoutPlacement{Designator: "R1", X: 45, Y: 0, BBox: layoutBBox{37.5, -2.5, 52.5, 2.5}}
	after := before
	after.Placements = append(append([]powerLayoutPlacement(nil), before.Placements...), blocker)
	if err := validateLibGeometry(&after); err != nil {
		t.Fatalf("fixture must pass the five-raw exit gate before local reachability: %v", err)
	}
	ctx, err := newSchematicRoutingContext(&SchematicRoutingOptions{MaxExpandedNodes: 20000, MaxReroutes: 1}, []SchematicLayoutComponent{
		{ID: "connector", Measurement: connector},
		{ID: "future", Measurement: powerLayoutPlacement{Designator: "U1", Pins: []powerLayoutPin{{Number: "1", Net: "MUST"}}}},
		{ID: "blocker", Measurement: blocker},
	})
	if err != nil {
		t.Fatal(err)
	}
	err = libValidateMandatoryDirectPlacementFrontiers(&before, &after, blocker, map[string]string{"MUST": "direct", "FOREIGN_TOP": "module_port", "FOREIGN_BOTTOM": "module_port"}, ctx)
	if err == nil || !strings.Contains(err.Error(), "no local frontier") {
		t.Fatalf("enclosed mandatory island accepted: %v", err)
	}
	var obstruction *schGeometryObstruction
	if !errors.As(err, &obstruction) || obstruction.kind != "direct-island-enclosed" || !obstruction.complete {
		t.Fatalf("missing structured direct-island evidence: %#v", err)
	}
	found := false
	for _, ref := range obstruction.blockers {
		found = found || ref == "R1"
	}
	if !found {
		t.Fatalf("actual placed blocker missing from evidence: %+v", obstruction.blockers)
	}
}

func TestMandatoryDirectPlacementAllowsFrontierAfterFiveRawMove(t *testing.T) {
	connector := powerLayoutPlacement{Designator: "J1", BBox: layoutBBox{-20, -25, 0, 25}, Pins: []powerLayoutPin{
		{Number: "F1", Net: "FOREIGN_TOP", X: 30, Y: 5, Rotation: mazeTestRotation(0)},
		{Number: "N", Net: "MUST", X: 30, Y: 0, Rotation: mazeTestRotation(0)},
		{Number: "F2", Net: "FOREIGN_BOTTOM", X: 30, Y: -5, Rotation: mazeTestRotation(0)},
		{Number: "N2", Net: "MUST", X: 30, Y: 20, Rotation: mazeTestRotation(0)},
	}}
	before := powerLayoutPlan{Placements: []powerLayoutPlacement{connector}}
	blocker := powerLayoutPlacement{Designator: "R1", X: 50, Y: 0, BBox: layoutBBox{42.5, -2.5, 57.5, 2.5}}
	after := before
	after.Placements = append(append([]powerLayoutPlacement(nil), before.Placements...), blocker)
	ctx, _ := newSchematicRoutingContext(&SchematicRoutingOptions{MaxExpandedNodes: 20000, MaxReroutes: 1}, []SchematicLayoutComponent{
		{ID: "connector", Measurement: connector},
		{ID: "future", Measurement: powerLayoutPlacement{Designator: "U1", Pins: []powerLayoutPin{{Number: "1", Net: "MUST"}}}},
	})
	if err := libValidateMandatoryDirectPlacementFrontiers(&before, &after, blocker, map[string]string{"MUST": "direct"}, ctx); err != nil {
		t.Fatalf("five-raw relocation should open a local frontier: %v", err)
	}
}

func TestMandatoryDirectPlacementDoesNotBlameUnrelatedCandidate(t *testing.T) {
	connector := powerLayoutPlacement{Designator: "J1", BBox: layoutBBox{-20, -25, 0, 25}, Pins: []powerLayoutPin{
		{Number: "F1", Net: "FOREIGN_TOP", X: 30, Y: 5, Rotation: mazeTestRotation(0)},
		{Number: "N", Net: "MUST", X: 30, Y: 0, Rotation: mazeTestRotation(0)},
		{Number: "F2", Net: "FOREIGN_BOTTOM", X: 30, Y: -5, Rotation: mazeTestRotation(0)},
		{Number: "N2", Net: "MUST", X: 30, Y: 20, Rotation: mazeTestRotation(0)},
	}}
	blocking := powerLayoutPlacement{Designator: "R1", X: 45, Y: 0, BBox: layoutBBox{37.5, -2.5, 52.5, 2.5}}
	before := powerLayoutPlan{Placements: []powerLayoutPlacement{connector, blocking}}
	unrelated := powerLayoutPlacement{Designator: "C9", X: 200, Y: 200, BBox: layoutBBox{190, 190, 210, 210}}
	after := before
	after.Placements = append(append([]powerLayoutPlacement(nil), before.Placements...), unrelated)
	ctx, _ := newSchematicRoutingContext(nil, []SchematicLayoutComponent{
		{ID: "connector", Measurement: connector},
		{ID: "future", Measurement: powerLayoutPlacement{Designator: "U1", Pins: []powerLayoutPin{{Number: "1", Net: "MUST"}}}},
	})
	if err := libValidateMandatoryDirectPlacementFrontiers(&before, &after, unrelated, map[string]string{"MUST": "direct"}, ctx); err != nil {
		t.Fatalf("unrelated checkpoint was blamed for a pre-existing closure: %v", err)
	}
}
