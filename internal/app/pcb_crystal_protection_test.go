package app

import (
	"math"
	"testing"
)

func TestCrystalFenceMovesSitesWithoutDroppingPitch(t *testing.T) {
	rect := layoutBBox{MinX: 0, MinY: 0, MaxX: 160, MaxY: 80}
	pts, gap, err := crystalFenceSelect(rect, 80, 2, 20, func(p [2]float64) bool { return math.Hypot(p[0]-80, p[1]) > 18 })
	if err != nil || len(pts) < 4 || gap > 80 {
		t.Fatalf("%v %.3f %v", pts, gap, err)
	}
	b := &pcbLayoutModuleBundle{FenceContour: rectPoints(rect), FencePitchMil: 80}
	for _, p := range pts {
		b.Vias = append(b.Vias, pcbModuleVia{Role: "fence", X: p[0], Y: p[1]})
	}
	if err := verifyCrystalFencePitch(pcbLayoutCandidate{Bundle: b}); err != nil {
		t.Fatal(err)
	}
	b.Vias = b.Vias[:2]
	if verifyCrystalFencePitch(pcbLayoutCandidate{Bundle: b}) == nil {
		t.Fatal("missing via passed pitch check")
	}
}
func TestCrystalFenceRejectsBlockedPerimeter(t *testing.T) {
	_, _, err := crystalFenceSelect(layoutBBox{MinX: 0, MinY: 0, MaxX: 160, MaxY: 80}, 60, 2, 20, func(p [2]float64) bool { return p[1] > 0 })
	if err == nil {
		t.Fatal("silently omitted blocked side")
	}
}
func TestCrystalProtectionPathCanDetour(t *testing.T) {
	p, ok := crystalProtectionPath([2]float64{0, 0}, [2]float64{100, 0}, &pcbCrystalProtectionSearch{StepMil: 10, MaxDetourMil: 40}, func(p [][2]float64) bool {
		for i := 0; i+1 < len(p); i++ {
			a, z := p[i], p[i+1]
			if rectSegDist(40, -15, 60, 15, a[0], a[1], z[0], z[1]) < 1 {
				return false
			}
		}
		return true
	})
	if !ok || len(p) <= 2 {
		t.Fatalf("failed bounded detour %v", p)
	}
}
func TestCrystalProtectionKeepsCompleteSignalSensitiveRegion(t *testing.T) {
	in, snap := crystalGuardFixture()
	in.Modules[0].CrystalGuard.ProtectionSearch = &pcbCrystalProtectionSearch{StepMil: 4, MaxDetourMil: 40}
	in.Modules[0].Search.GapsMil = []float64{40}
	in.Modules[0].Search.RotationDeltasDeg = []float64{0}
	rep, err := planPCBLayoutModule(in, snap, "crystal-guard", 1)
	if err != nil {
		t.Fatalf("%s", crystalPlanFailureText(rep, err))
	}
	b := rep.Candidates[0].Bundle
	if len(b.Regions) != 2 || len(b.FenceContour) < 4 || b.FencePitchMil != 80 {
		t.Fatal("incomplete protection contract")
	}
	if err := verifyCrystalFencePitch(rep.Candidates[0]); err != nil {
		t.Fatal(err)
	}
}

func TestCrystalGridRouteAvoidsRotatedPadAndHonorsBound(t *testing.T) {
	_, snap := crystalGuardFixture()
	snap.Rules.ClearanceMil = 6
	pad := pcbPadP{ID: "obstacle", Designator: "FIXED", Number: "1", Net: "OTHER", Layer: 1, X: 100, Y: 100, W: 60, H: 60, Rotation: 45, Shape: "RECT", ShapeW: 70, ShapeH: 10, ShapeOK: true}
	clear, err := crystalRouteClear("OSC", 1, 8, snap, []pcbPadP{pad}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, z := [2]float64{50, 100}, [2]float64{150, 100}
	if _, err := crystalGridRoute(a, z, 2, 10, clear); err == nil {
		t.Fatal("silently exceeded detour bound")
	}
	p, err := crystalGridRoute(a, z, 2, 50, clear)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i+1 < len(p); i++ {
		d, err := pcbExactPadSegmentGap(pad, p[i], p[i+1])
		if err != nil || d < 10-1e-6 {
			t.Fatalf("unsafe segment %v %v %.3f", p[i], p[i+1], d)
		}
	}
}
func TestCrystalSensitiveContourRetainsStrokeWithoutBoundingEmptyCorner(t *testing.T) {
	rects := []layoutBBox{{MinX: 0, MinY: 0, MaxX: 100, MaxY: 40}, {MinX: 45, MinY: 30, MaxX: 55, MaxY: 100}}
	contour, err := crystalRectangleUnionOuter(rects)
	if err != nil {
		t.Fatal(err)
	}
	if compoundWinding([][][2]float64{contour}, [2]float64{50, 90}) == 0 {
		t.Fatal("lost sensitive branch")
	}
	if compoundWinding([][][2]float64{contour}, [2]float64{90, 90}) != 0 {
		t.Fatal("filled unrelated bounding-box corner")
	}
	if _, err := crystalRectangleUnionOuter([]layoutBBox{rects[0], {MinX: 200, MinY: 200, MaxX: 210, MaxY: 210}}); err == nil {
		t.Fatal("silently lost disconnected region")
	}
}
func TestCrystalFenceUsesRotatedPadShape(t *testing.T) {
	_, snap := crystalGuardFixture()
	pad := pcbPadP{Designator: "U", Number: "1", X: 100, Y: 100, W: 50, H: 50, Rotation: 45, Shape: "RECT", ShapeW: 60, ShapeH: 8, ShapeOK: true}
	// A point inside the pad's axis-aligned box but outside its actual diagonal.
	good := [2]float64{120, 80}
	got := preflightViaFence([][2]float64{good}, "GND", 4, 8, 2, 1, snap.Outline, []pcbPadP{pad}, nil, nil, nil, nil)
	if len(got.Create) != 1 {
		t.Fatalf("bbox-only rejection: %+v", got)
	}
	bad := [2]float64{120, 120}
	got = preflightViaFence([][2]float64{bad}, "GND", 4, 8, 2, 1, snap.Outline, []pcbPadP{pad}, nil, nil, nil, nil)
	if len(got.Problems) == 0 {
		t.Fatal("rotated copper collision missed")
	}
}

func TestCrystalGridRouteRetainsHeadingAtJunction(t *testing.T) {
	paths := [][][2]float64{{{0, 0}, {1, 0}, {2, 0}, {3, 0}, {4, 0}}, {{0, 0}, {1, -1}, {2, -2}, {3, -2}, {4, -1}, {4, 0}, {4, 1}, {4, 2}, {4, 3}, {4, 4}}}
	edges := map[[4]float64]bool{}
	sites := map[[2]float64]bool{}
	for _, p := range paths {
		for i, a := range p {
			sites[a] = true
			if i+1 < len(p) {
				b := p[i+1]
				edges[[4]float64{a[0], a[1], b[0], b[1]}] = true
				edges[[4]float64{b[0], b[1], a[0], a[1]}] = true
			}
		}
	}
	clear := func(a, b [2]float64) bool {
		if a == b {
			return sites[a]
		}
		n := int(math.Ceil(math.Max(math.Abs(b[0]-a[0]), math.Abs(b[1]-a[1]))))
		p := a
		for i := 1; i <= n; i++ {
			q := [2]float64{a[0] + float64(i)/float64(n)*(b[0]-a[0]), a[1] + float64(i)/float64(n)*(b[1]-a[1])}
			for j := range q {
				if math.Abs(q[j]-math.Round(q[j])) > 1e-7 {
					return false
				}
				q[j] = math.Round(q[j])
			}
			if !edges[[4]float64{p[0], p[1], q[0], q[1]}] {
				return false
			}
			p = q
		}
		return true
	}
	route, err := crystalGridRoute([2]float64{0, 0}, [2]float64{4, 4}, 1, 3, clear)
	if err != nil {
		t.Fatal(err)
	}
	lower := false
	for _, p := range route {
		if p[1] < 0 {
			lower = true
		}
	}
	if !lower {
		t.Fatalf("lost the longer usable arrival heading: %v", route)
	}
}
func TestCrystalProtectionRejectsDiagonalBoundExpansion(t *testing.T) {
	_, ok := crystalProtectionPath([2]float64{0, 0}, [2]float64{10, 0}, &pcbCrystalProtectionSearch{StepMil: 10, MaxDetourMil: 10}, func(p [][2]float64) bool {
		for _, q := range p {
			if q[0] >= 20 && q[1] >= 10 {
				return true
			}
		}
		return false
	})
	if ok {
		t.Fatal("accepted sqrt(2) times the declared detour")
	}
}
func TestCrystalObstacleContourMigratesBlockedSeed(t *testing.T) {
	clear := func(a, b [2]float64) bool { return segPtDist(0, 0, a[0], a[1], b[0], b[1]) > 6 }
	p, err := crystalObstacleContour(rectPoints(layoutBBox{MinX: 0, MinY: 0, MaxX: 100, MaxY: 100}), &pcbCrystalProtectionSearch{StepMil: 4, MaxDetourMil: 20}, clear)
	if err != nil {
		t.Fatal(err)
	}
	if p[0] == ([2]float64{0, 0}) {
		t.Fatal("illegal seed was fixed")
	}
	for i, a := range p {
		if !clear(a, p[(i+1)%len(p)]) {
			t.Fatal("migrated contour still collides")
		}
	}
}
func TestCrystalFenceReferenceLinksMayCrossUnusableSites(t *testing.T) {
	legal := func(p [2]float64) bool { return p[0] < 40 || p[0] > 60 }
	p, err := crystalFenceSitePath([2]float64{0, 0}, [2]float64{100, 0}, 2, 20, 70, 20, func(a, b [2]float64) bool { return true }, legal)
	if err != nil {
		t.Fatal(err)
	}
	for i, a := range p {
		if !legal(a) {
			t.Fatal("placed via at illegal site")
		}
		if i+1 < len(p) && math.Hypot(p[i+1][0]-a[0], p[i+1][1]-a[1]) > 70+1e-6 {
			t.Fatal("violated declared pitch")
		}
	}
}
