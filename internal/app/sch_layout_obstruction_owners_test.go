package app

import (
	"errors"
	"reflect"
	"testing"
)

// The obstructing bodies are outside the child's corridor. Only a wire owned
// by an earlier component crosses it, so body-only attribution loses the
// checkpoint which can actually remove the obstruction.
func TestPlacementConflictRetainsPriorWireOwnerAndRelocationRepairs(t *testing.T) {
	core := powerLayoutPlacement{Designator: "CORE", X: 0, Y: -100, BBox: layoutBBox{-10, -110, 10, -90}, TextBBoxes: []layoutBBox{{-5, -125, 5, -120}}, Pins: []powerLayoutPin{{Number: "1", Net: "OWN", X: 0, Y: -80}}}
	a := powerLayoutPlacement{Designator: "A", X: -100, BBox: layoutBBox{-110, -10, -90, 10}, TextBBoxes: []layoutBBox{{-105, -25, -95, -20}}, Pins: []powerLayoutPin{{Number: "1", Net: "BLOCK", X: -80, Y: 0}}}
	b := powerLayoutPlacement{Designator: "B", X: 100, BBox: layoutBBox{90, -10, 110, 10}, TextBBoxes: []layoutBBox{{95, -25, 105, -20}}, Pins: []powerLayoutPin{{Number: "1", Net: "BLOCK", X: 80, Y: 0}}}
	child := powerLayoutPlacement{Designator: "CHILD", BBox: layoutBBox{-10, -10, 10, 10}, TextBBoxes: []layoutBBox{{-5, 15, 5, 20}}, Pins: []powerLayoutPin{{Number: "1", Net: "OWN", X: 0, Y: -20}}}
	base := powerLayoutPlan{Placements: []powerLayoutPlacement{core, a, b}, Wires: []powerLayoutWire{{Net: "BLOCK", Points: [][2]float64{{-80, 0}, {80, 0}}}}}
	if err := validateLibGeometry(&base); err != nil {
		t.Fatal("invalid prior checkpoint", err)
	}
	trial := base
	trial.Placements = append(append([]powerLayoutPlacement{}, base.Placements...), child)
	trial.Wires = append(append([]powerLayoutWire{}, base.Wires...), powerLayoutWire{Net: "OWN", Points: [][2]float64{{0, -80}, {0, -20}}})
	err := validateLibGeometry(&trial)
	var obstruction *schGeometryObstruction
	if !errors.As(err, &obstruction) || obstruction.kind != "wire-body" {
		t.Fatal("fixture must fail on the existing wire, not a body/stem", err)
	}
	measured := map[string]powerLayoutPlacement{"core": core, "prior-owner": a, "other-owner": b, "child": child}
	s := schematicRepairSearch{measured: measured, hints: map[string]SchematicLayoutPeripheral{}}
	raw := newPlacementCandidateConflict()
	raw.observe(err)
	raw.candidates = 1
	conflict := s.placementConflict("child", []libAttachmentPair{{host: core.Pins[0], own: child.Pins[0]}}, map[string]powerLayoutPlacement{"core": core, "prior-owner": a, "other-owner": b}, []string{"child"}, &base, raw.finish("coordinate-window", nil))
	if !conflict.OwnersComplete || !reflect.DeepEqual(conflict.BlockerOwners, []string{"child", "other-owner", "prior-owner"}) || !s.placementParticipates("prior-owner", conflict) {
		t.Fatalf("lost actual wire relocation dependency: %+v", conflict)
	}
	// Move only A, preserving its rigid measurements. Recompute its route with
	// both endpoint directions unchanged; no final geometry check is bypassed.
	repaired := trial
	repaired.Placements = append([]powerLayoutPlacement{}, trial.Placements...)
	repaired.Placements[1] = plTranslate(a, 0, 50)
	repaired.Wires = append(libPointsRoute("BLOCK", [2]float64{-80, 50}, [2]float64{50, 50}, [2]float64{50, 0}, [2]float64{80, 0}), trial.Wires[1])
	if err := validateLibGeometry(&repaired); err != nil {
		t.Fatal("moving attributed prior owner did not repair full geometry", err)
	}
	if !reflect.DeepEqual(base.Placements[1], a) || !reflect.DeepEqual(base.Wires[0].Points, [][2]float64{{-80, 0}, {80, 0}}) {
		t.Fatal("repair mutated the original checkpoint")
	}
}

func TestWireComponentObstructionsKeepMissingWireOwnerUnknown(t *testing.T) {
	for _, scenario := range []struct {
		name string
		part powerLayoutPlacement
		wire powerLayoutWire
	}{
		{"wire-body", powerLayoutPlacement{Designator: "P", BBox: layoutBBox{-10, -10, 10, 10}}, powerLayoutWire{Net: "UNKNOWN", Points: [][2]float64{{-30, 0}, {30, 0}}}},
		{"pin-exit-direction", powerLayoutPlacement{Designator: "P", BBox: layoutBBox{-10, -30, 10, -10}, Pins: []powerLayoutPin{{Number: "1", Net: "OTHER", X: 0, Y: 0}}}, powerLayoutWire{Net: "UNKNOWN", Points: [][2]float64{{-30, 0}, {30, 0}}}},
		{"foreign-pin", powerLayoutPlacement{Designator: "P", BBox: layoutBBox{-10, -30, 10, -10}, Pins: []powerLayoutPin{{Number: "1", Net: "OTHER", X: 0, Y: 0}}}, powerLayoutWire{Net: "UNKNOWN", Points: [][2]float64{{0, 0}, {0, 30}}}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			plan := powerLayoutPlan{Placements: []powerLayoutPlacement{scenario.part}, Wires: []powerLayoutWire{scenario.wire}}
			err := validateLibGeometry(&plan)
			var obstruction *schGeometryObstruction
			if !errors.As(err, &obstruction) || obstruction.kind != scenario.name || obstruction.complete {
				t.Fatalf("unknown wire ownership falsely complete: %+v, %v", obstruction, err)
			}
		})
	}
}
