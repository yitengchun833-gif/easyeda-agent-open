package app

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestCrystalGuardPreSplitsEveryPlannedGroundJunction(t *testing.T) {
	bundle := &pcbLayoutModuleBundle{GroundRoutes: []pcbModuleRoute{
		{ID: "guard", Role: "guard", Net: "GND", Layer: 1, WidthMil: 8, Points: [][2]float64{{0, 0}, {100, 0}}},
		{ID: "spoke-a", Role: "ground-spoke", Net: "GND", Layer: 1, WidthMil: 8, Points: [][2]float64{{25, 20}, {25, 0}}},
		{ID: "entry", Role: "ground-entry-top", Net: "GND", Layer: 1, WidthMil: 8, Points: [][2]float64{{75, 0}, {75, -20}}},
		// An already explicit endpoint must not produce a zero-length split.
		{ID: "spoke-end", Role: "ground-spoke", Net: "GND", Layer: 1, WidthMil: 8, Points: [][2]float64{{100, 0}, {120, 20}}},
		// Different layer/net copper is not a junction of this guard primitive.
		{ID: "bottom", Role: "ground-entry-bottom", Net: "GND", Layer: 2, WidthMil: 8, Points: [][2]float64{{50, 0}, {50, -20}}},
		{ID: "other", Role: "test", Net: "OTHER", Layer: 1, WidthMil: 8, Points: [][2]float64{{60, 0}, {60, -20}}},
	}}
	if err := splitCrystalGuardAtGroundJunctions(bundle); err != nil {
		t.Fatal(err)
	}
	want := [][2]float64{{0, 0}, {25, 0}, {75, 0}, {100, 0}}
	if got := bundle.GroundRoutes[0].Points; !reflect.DeepEqual(got, want) {
		t.Fatalf("guard points=%v want %v", got, want)
	}
	// Re-normalization is idempotent; a regenerated candidate has the same apply
	// step IDs and therefore the same journal capture names.
	if err := splitCrystalGuardAtGroundJunctions(bundle); err != nil {
		t.Fatal(err)
	}
	if got := bundle.GroundRoutes[0].Points; !reflect.DeepEqual(got, want) {
		t.Fatalf("second normalization changed guard points: %v", got)
	}
}

func TestCrystalGuardRejectsUnmaterializedInteriorCrossing(t *testing.T) {
	bundle := &pcbLayoutModuleBundle{GroundRoutes: []pcbModuleRoute{
		{ID: "guard", Role: "guard", Net: "GND", Layer: 1, WidthMil: 8, Points: [][2]float64{{0, 0}, {100, 0}}},
		{ID: "crossing", Role: "ground-spoke", Net: "GND", Layer: 1, WidthMil: 8, Points: [][2]float64{{50, -20}, {50, 20}}},
	}}
	err := splitCrystalGuardAtGroundJunctions(bundle)
	if err == nil || !strings.Contains(err.Error(), "away from a planned branch endpoint") {
		t.Fatalf("interior crossing did not fail closed: %v", err)
	}
}

func TestCrystalGuardApplyCapturesStablePreSplitPrimitives(t *testing.T) {
	in, snap := crystalGuardFixture()
	in.Modules[0].CrystalGuard.ProtectionSearch = &pcbCrystalProtectionSearch{StepMil: 4, MaxDetourMil: 40}
	in.Modules[0].Search.GapsMil = []float64{40}
	in.Modules[0].Search.RotationDeltasDeg = []float64{0}
	rep, err := planPCBLayoutModule(in, snap, "crystal-guard", 1)
	if err != nil {
		t.Fatalf("plan: %s", crystalPlanFailureText(rep, err))
	}
	candidate := rep.Candidates[0]
	repeated, err := planPCBLayoutModule(in, snap, "crystal-guard", 1)
	if err != nil {
		t.Fatalf("repeat plan: %s", crystalPlanFailureText(repeated, err))
	}
	if got, want := canonicalJSON(repeated.Candidates[0].Apply), canonicalJSON(candidate.Apply); got != want {
		t.Fatal("identical input changed apply steps or journal capture names")
	}

	expected := map[string]bool{}
	junctions := 0
	for _, route := range candidate.Bundle.GroundRoutes {
		if route.Role != "guard" {
			continue
		}
		junctions += len(route.Points) - 2
		for segment := 0; segment+1 < len(route.Points); segment++ {
			expected[fmt.Sprintf("route-%s-%02d", route.ID, segment+1)] = true
		}
	}
	if junctions == 0 {
		t.Fatal("fixture produced no guard T junctions to pre-split")
	}

	captured := map[string]bool{}
	for _, step := range candidate.Apply.Steps {
		if !expected[step.ID] {
			continue
		}
		if step.Action != "pcb.line.create" || len(step.Capture) != 1 {
			t.Fatalf("guard step lacks an exact primitive capture: %+v", step)
		}
		captureName := strings.ToUpper(strings.ReplaceAll(step.ID, "-", "_")) + "_PID"
		if step.Capture[captureName] != "$.primitiveId" || captured[captureName] {
			t.Fatalf("unstable/duplicate guard capture %s: %+v", captureName, step.Capture)
		}
		captured[captureName] = true
	}
	if len(captured) != len(expected) {
		t.Fatalf("captured %d guard primitives, want %d: expected=%v captured=%v", len(captured), len(expected), expected, captured)
	}

	// A later branch can only touch a guard primitive at one of its endpoints.
	// Thus the host has no reason to replace any PID already captured above.
	for _, guard := range candidate.Bundle.GroundRoutes {
		if guard.Role != "guard" {
			continue
		}
		for _, branch := range candidate.Bundle.GroundRoutes {
			if branch.Role == "guard" || branch.Net != guard.Net || branch.Layer != guard.Layer {
				continue
			}
			for i := 0; i+1 < len(guard.Points); i++ {
				for j := 0; j+1 < len(branch.Points); j++ {
					intersections, intersectionErr := netPathTrackTrackIntersections(
						pcbNetPathNode{kind: "track", x1: guard.Points[i][0], y1: guard.Points[i][1], x2: guard.Points[i+1][0], y2: guard.Points[i+1][1]},
						pcbNetPathNode{kind: "track", x1: branch.Points[j][0], y1: branch.Points[j][1], x2: branch.Points[j+1][0], y2: branch.Points[j+1][1]},
					)
					if intersectionErr != nil {
						t.Fatalf("ambiguous planned junction: %v", intersectionErr)
					}
					for _, p := range intersections {
						atGuardEndpoint := crystalPointsClose([2]float64{p.x, p.y}, guard.Points[i]) || crystalPointsClose([2]float64{p.x, p.y}, guard.Points[i+1])
						if !atGuardEndpoint {
							t.Fatalf("branch %s would split captured guard %s segment %d at (%.3f,%.3f)", branch.ID, guard.ID, i+1, p.x, p.y)
						}
					}
				}
			}
		}
	}
}
