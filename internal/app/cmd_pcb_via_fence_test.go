package app

import (
	"math"
	"strings"
	"testing"
)

func TestRectViaFencePointsKeepsCornersUniqueAndPitchBounded(t *testing.T) {
	points, err := rectViaFencePoints(0, 0, 100, 60, 40, 10)
	if err != nil {
		t.Fatal(err)
	}
	// Expanded rectangle is -10,-10 .. 110,70.  The horizontal edges use
	// 3 intervals and the vertical edges use 2: 2*(3+2) = 10 unique points.
	if len(points) != 10 {
		t.Fatalf("got %d points, want 10: %#v", len(points), points)
	}
	seen := map[[2]float64]bool{}
	for _, point := range points {
		if seen[point] {
			t.Fatalf("duplicate corner/point: %#v", point)
		}
		seen[point] = true
	}
	for _, corner := range [][2]float64{{-10, -10}, {110, -10}, {110, 70}, {-10, 70}} {
		if !seen[corner] {
			t.Fatalf("missing corner %#v in %#v", corner, points)
		}
	}
	for i := range points {
		next := points[(i+1)%len(points)]
		if distance := math.Hypot(next[0]-points[i][0], next[1]-points[i][1]); distance > 40+1e-9 {
			t.Fatalf("edge spacing %.6f exceeds pitch: %#v -> %#v", distance, points[i], next)
		}
	}
}

func TestRectViaFencePointsNormalizesRectAndRejectsInvalidInputs(t *testing.T) {
	forward, err := rectViaFencePoints(0, 0, 100, 60, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	reversed, err := rectViaFencePoints(100, 60, 0, 0, 50, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(forward) != len(reversed) {
		t.Fatalf("normalization changed count: %d != %d", len(forward), len(reversed))
	}
	for i := range forward {
		if forward[i] != reversed[i] {
			t.Fatalf("normalization changed point %d: %#v != %#v", i, forward[i], reversed[i])
		}
	}
	if _, err := rectViaFencePoints(0, 0, 0, 60, 40, 0); err == nil {
		t.Fatal("zero-width rectangle should fail")
	}
	if _, err := rectViaFencePoints(0, 0, 100, 60, 0, 0); err == nil {
		t.Fatal("zero pitch should fail")
	}
	if _, err := rectViaFencePoints(0, 0, 100, 60, 40, -1); err == nil {
		t.Fatal("negative margin should fail")
	}
	if _, err := rectViaFencePoints(math.NaN(), 0, 100, 60, 40, 0); err == nil {
		t.Fatal("NaN coordinate should fail")
	}
	if _, err := rectViaFencePoints(0, 0, 100000, 60, 1, 0); err == nil {
		t.Fatal("excessive point count should fail")
	}
}

func TestPreflightViaFenceRejectsEdgesPadsAndOtherNetsButKeepsSameNetPointIdempotent(t *testing.T) {
	outline := &boardOutline{BBox: layoutBBox{MinX: 0, MinY: 0, MaxX: 200, MaxY: 200}}
	points := [][2]float64{{10, 100}, {60, 60}, {100, 100}, {140, 140}, {170, 170}}
	pads := []pcbPadP{{Designator: "U1", Number: "1", X: 60, Y: 60, W: 20, H: 20, Shape: "RECT", ShapeW: 20, ShapeH: 20, ShapeOK: true}}
	tracks := []pcbTrack{{ID: "t1", Net: "SIG", Layer: 1, X1: 80, Y1: 100, X2: 120, Y2: 100, Width: 10}}
	vias := []pcbViaP{
		{ID: "same", Net: "GND", X: 140, Y: 140, Dia: 24, Hole: 12},
		{ID: "other", Net: "SIG", X: 170, Y: 170, Dia: 24, Hole: 12},
	}
	got := preflightViaFence(points, "GND", 12, 24, 6, 8, outline, pads, tracks, nil, vias, nil)
	if len(got.Existing) != 1 || got.Existing[0] != ([2]float64{140, 140}) {
		t.Fatalf("existing=%v", got.Existing)
	}
	if len(got.Create) != 0 {
		t.Fatalf("unexpected creatable points: %v", got.Create)
	}
	if len(got.Problems) != 4 {
		t.Fatalf("problems=%v", got.Problems)
	}
	joined := strings.Join(got.Problems, "\n")
	for _, want := range []string{"board-edge", "pad U1.1", "other-net track t1", "via other"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("missing %q in %s", want, joined)
		}
	}
}

func TestPreflightViaFenceFailsClosedForUnknownPadAndMismatchedSameNetVia(t *testing.T) {
	outline := &boardOutline{BBox: layoutBBox{MinX: 0, MinY: 0, MaxX: 200, MaxY: 200}}
	point := [][2]float64{{100, 100}}
	unknown := []pcbPadP{{Designator: "U1", Number: "1", ShapeIssue: "POLYGON pad geometry is unsupported"}}
	got := preflightViaFence(point, "GND", 12, 24, 6, 8, outline, unknown, nil, nil, nil, nil)
	if len(got.Problems) != 1 || !strings.Contains(got.Problems[0], "unknown/unsupported") {
		t.Fatalf("unknown pad accepted: %+v", got)
	}
	special := pcbPadP{Designator: "U2", Number: "2", X: 150, Y: 150}
	if err := parseNetPathPadShape(map[string]any{"shape": []any{"RECT", 10.0, 10.0, 0.0}, "specialPad": []any{"L", 1.0, 1.0}}, &special, "U2.2"); err != nil {
		t.Fatal(err)
	}
	got = preflightViaFence(point, "GND", 12, 24, 6, 8, outline, []pcbPadP{special}, nil, nil, nil, nil)
	if len(got.Problems) != 1 || !strings.Contains(got.Problems[0], "specialPad") {
		t.Fatalf("special pad accepted: %+v", got)
	}

	exactPads := []pcbPadP{{Designator: "U1", Number: "1", X: 20, Y: 20, W: 10, H: 10, Shape: "RECT", ShapeW: 10, ShapeH: 10, ShapeOK: true}}
	wrongSize := []pcbViaP{{ID: "old", Net: "GND", X: 100, Y: 100, Hole: 10, Dia: 24}}
	got = preflightViaFence(point, "GND", 12, 24, 6, 8, outline, exactPads, nil, nil, wrongSize, nil)
	if len(got.Existing) != 0 || len(got.Problems) != 1 || !strings.Contains(got.Problems[0], "want 12.000/24.000mil") {
		t.Fatalf("mismatched same-net via treated as idempotent: %+v", got)
	}
}

func TestPreflightViaFenceChecksAreaCopperAndAllowsSameNetBond(t *testing.T) {
	outline := &boardOutline{BBox: layoutBBox{MinX: 0, MinY: 0, MaxX: 200, MaxY: 200}}
	pads := []pcbPadP{{Designator: "U1", Number: "1", X: 20, Y: 20, W: 10, H: 10, Shape: "RECT", ShapeW: 10, ShapeH: 10, ShapeOK: true}}
	contour := [][2]float64{{80, 80}, {120, 80}, {120, 120}, {80, 120}}
	point := [][2]float64{{100, 100}}
	other := []pcbCopperArea{{ID: "fill-1", Kind: "static fill", Net: "SIG", Layer: 1, Contours: [][][2]float64{contour}}}
	got := preflightViaFence(point, "GND", 12, 24, 6, 8, outline, pads, nil, nil, nil, other)
	if len(got.Problems) != 1 || !strings.Contains(got.Problems[0], "other-net static fill") {
		t.Fatalf("other-net area copper accepted: %+v", got)
	}
	same := []pcbCopperArea{{ID: "fill-1", Kind: "static fill", Net: "GND", Layer: 1, Contours: [][][2]float64{contour}}}
	got = preflightViaFence(point, "GND", 12, 24, 6, 8, outline, pads, nil, nil, nil, same)
	if len(got.Problems) != 0 || len(got.Create) != 1 {
		t.Fatalf("same-net area bond rejected: %+v", got)
	}
}

func TestViaFenceRoutingFailsClosedWhenArcsUnavailable(t *testing.T) {
	_, _, err := parseViaFenceRouting(map[string]any{"lines": []any{}, "arcs": []any{}, "arcsAvailable": false})
	if err == nil || !strings.Contains(err.Error(), "arcsAvailable") {
		t.Fatalf("arcsAvailable=false accepted: %v", err)
	}
}

func TestParseCopperAreaObstaclesRejectsUnknownComplexSource(t *testing.T) {
	_, err := parseCopperAreaObstacles([]any{map[string]any{
		"primitiveId": "f1", "net": "SIG", "layer": 1.0, "geometryAvailable": true,
		"source": []any{0.0, 0.0, "BEZIER", 10.0, 10.0},
	}}, nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("unsupported area source accepted: %v", err)
	}
}
