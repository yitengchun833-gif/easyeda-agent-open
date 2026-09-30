package app

import (
	"errors"
	"math"
	"testing"
)

func mazeTestRotation(value float64) *float64 { return &value }

func mazeTestPart(ref, net string, box layoutBBox, pin powerLayoutPin) powerLayoutPlacement {
	return powerLayoutPlacement{Designator: ref, X: (box.MinX + box.MaxX) / 2, Y: (box.MinY + box.MaxY) / 2,
		BBox: box, Pins: []powerLayoutPin{pin}}
}

func TestLibMazeRouteFindsMultiBendBeyondLegacyDetour(t *testing.T) {
	source := mazeTestPart("A1", "N", layoutBBox{-20, -10, 0, 10}, powerLayoutPin{Number: "1", Net: "N", X: 5, Y: 0, Rotation: mazeTestRotation(0)})
	target := mazeTestPart("B1", "N", layoutBBox{200, -10, 220, 10}, powerLayoutPin{Number: "1", Net: "N", X: 195, Y: 0, Rotation: mazeTestRotation(180)})
	blocker := powerLayoutPlacement{Designator: "X1", X: 100, Y: 0, BBox: layoutBBox{70, -100, 130, 100}}
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{source, target, blocker}}
	if routes := append(libRoutes(source.Pins[0], target.Pins[0], &p), libDetourRoutes(source.Pins[0], target.Pins[0], &p)...); func() bool {
		for _, route := range routes {
			trial := p
			trial.Wires = libAppendRoute(p.Wires, route)
			if validateLibGeometry(&trial) == nil {
				return true
			}
		}
		return false
	}() {
		t.Fatal("legacy straight/L/80-raw detours unexpectedly solved the fixture")
	}
	ctx, err := newSchematicRoutingContext(&SchematicRoutingOptions{MaxExpandedNodes: 200000, MaxReroutes: 1}, []SchematicLayoutComponent{{ID: "a", Measurement: source}, {ID: "b", Measurement: target}, {ID: "x", Measurement: blocker}})
	if err != nil {
		t.Fatal(err)
	}
	islands := libIslands(&p)
	if len(islands) != 2 {
		t.Fatalf("islands=%d", len(islands))
	}
	route, err := libMazeRoute(&p, islands[0], islands[1], ctx)
	if err != nil {
		t.Fatal(err)
	}
	p.Wires = libAppendRoute(p.Wires, route)
	if err := validateLibGeometry(&p); err != nil {
		t.Fatal(err)
	}
	if !libPinsShareIsland(&p, source.Pins[0], target.Pins[0]) {
		t.Fatal("specified islands were not merged")
	}
	if ctx.expanded <= 0 || ctx.expanded >= ctx.options.MaxExpandedNodes {
		t.Fatalf("expanded=%d", ctx.expanded)
	}
	beforeReuse := ctx.expanded
	p2 := powerLayoutPlan{Placements: []powerLayoutPlacement{source, target, blocker, {Designator: "FAR", BBox: layoutBBox{500, 500, 520, 520}}}}
	islands = libIslands(&p2)
	reused, err := libMazeRoute(&p2, islands[0], islands[1], ctx)
	if err != nil {
		t.Fatal(err)
	}
	p2.Wires = libAppendRoute(p2.Wires, reused)
	if ctx.expanded != beforeReuse || ctx.templateHits != 1 || !libPinsShareIsland(&p2, source.Pins[0], target.Pins[0]) {
		t.Fatalf("validated route template was not reused: expanded=%d/%d hits=%d", ctx.expanded, beforeReuse, ctx.templateHits)
	}
}

func TestMazeTemplateIsRevalidatedAgainstNewDesignatorObstacle(t *testing.T) {
	source := mazeTestPart("A1", "N", layoutBBox{-20, -10, 0, 10}, powerLayoutPin{Number: "1", Net: "N", X: 5, Y: 0, Rotation: mazeTestRotation(0)})
	target := mazeTestPart("B1", "N", layoutBBox{200, -10, 220, 10}, powerLayoutPin{Number: "1", Net: "N", X: 195, Y: 0, Rotation: mazeTestRotation(180)})
	labelOwner := powerLayoutPlacement{Designator: "R1", X: 100, Y: 100, BBox: layoutBBox{95, 95, 105, 105}}
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{source, target, labelOwner}}
	islands := libIslands(&p)
	ctx, _ := newSchematicRoutingContext(&SchematicRoutingOptions{MaxExpandedNodes: 20000, MaxReroutes: 1}, nil)
	ctx.policies = map[string]string{"N": "direct"}
	first, err := libMazeRoute(&p, islands[0], islands[1], ctx)
	if err != nil {
		t.Fatal(err)
	}
	blocked := p
	blocked.Placements = append([]powerLayoutPlacement(nil), p.Placements...)
	blocked.Placements[2].TextBBoxes = []layoutBBox{{90, -5, 110, 5}}
	beforeHits := ctx.templateHits
	second, err := libMazeRoute(&blocked, islands[0], islands[1], ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ctx.templateHits != beforeHits {
		t.Fatal("route template crossing a newly measured Designator was reused")
	}
	trial := blocked
	trial.Wires = libAppendRoute(trial.Wires, second)
	if err := validateLibGeometry(&trial); err != nil {
		t.Fatalf("replacement route is invalid: %v; first=%+v second=%+v", err, first, second)
	}
}

func TestMazeTransientBudgetFailureIsNotCached(t *testing.T) {
	source := mazeTestPart("A1", "N", layoutBBox{-20, -10, 0, 10}, powerLayoutPin{Number: "1", Net: "N", X: 5, Y: 0, Rotation: mazeTestRotation(0)})
	target := mazeTestPart("B1", "N", layoutBBox{200, -10, 220, 10}, powerLayoutPin{Number: "1", Net: "N", X: 195, Y: 0, Rotation: mazeTestRotation(180)})
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{source, target}}
	islands := libIslands(&p)
	ctx, _ := newSchematicRoutingContext(&SchematicRoutingOptions{MaxExpandedNodes: 1024, MaxReroutes: 1}, nil)
	ctx.policies = map[string]string{"N": "direct"}
	ctx.expanded = ctx.usableExpandedNodes()
	_, err := libMazeRoute(&p, islands[0], islands[1], ctx)
	var failure *schematicRoutingFailure
	if !errors.As(err, &failure) || failure.Kind != "relocation-budget-reserved" || len(ctx.cache) != 0 {
		t.Fatalf("transient failure was not classified or was cached: err=%v cache=%d", err, len(ctx.cache))
	}
	ctx.relocation++
	route, err := libMazeRoute(&p, islands[0], islands[1], ctx)
	ctx.relocation--
	if err != nil || len(route) == 0 {
		t.Fatalf("same geometry was not retried with relocation budget: %v", err)
	}
}

func TestLibMazeRouteConnectsToWireTreeMidspan(t *testing.T) {
	source := mazeTestPart("A1", "N", layoutBBox{-20, 40, 0, 60}, powerLayoutPin{Number: "1", Net: "N", X: 5, Y: 50, Rotation: mazeTestRotation(0)})
	top := mazeTestPart("B1", "N", layoutBBox{90, 80, 110, 100}, powerLayoutPin{Number: "1", Net: "N", X: 100, Y: 75, Rotation: mazeTestRotation(270)})
	bottom := mazeTestPart("C1", "N", layoutBBox{90, 0, 110, 20}, powerLayoutPin{Number: "1", Net: "N", X: 100, Y: 25, Rotation: mazeTestRotation(90)})
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{source, top, bottom}, Wires: []powerLayoutWire{{Net: "N", Points: [][2]float64{{100, 25}, {100, 75}}}}}
	islands := libIslands(&p)
	if len(islands) != 2 {
		t.Fatalf("islands=%d", len(islands))
	}
	ctx, _ := newSchematicRoutingContext(&SchematicRoutingOptions{MaxExpandedNodes: 20000, MaxReroutes: 1}, nil)
	route, err := libMazeRoute(&p, islands[0], islands[1], ctx)
	if err != nil {
		t.Fatal(err)
	}
	p.Wires = libAppendRoute(p.Wires, route)
	if !libPinsShareIsland(&p, source.Pins[0], top.Pins[0]) {
		t.Fatal("pin did not join target tree")
	}
	foundMidspan := false
	for _, wire := range p.Wires {
		foundMidspan = foundMidspan || (wire.Net == "N" && plOnSegment([2]float64{100, 50}, wire.Points[0], wire.Points[1]))
	}
	if !foundMidspan {
		t.Fatal("target midspan disappeared")
	}
}

func TestLibMazeRouteHonorsExpandedNodeBudget(t *testing.T) {
	source := mazeTestPart("A1", "N", layoutBBox{-20, -10, 0, 10}, powerLayoutPin{Number: "1", Net: "N", X: 5, Y: 0, Rotation: mazeTestRotation(0)})
	target := mazeTestPart("B1", "N", layoutBBox{200, -10, 220, 10}, powerLayoutPin{Number: "1", Net: "N", X: 195, Y: 0, Rotation: mazeTestRotation(180)})
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{source, target, {Designator: "X1", BBox: layoutBBox{50, -100, 150, 100}}}}
	ctx, _ := newSchematicRoutingContext(&SchematicRoutingOptions{MaxExpandedNodes: 1, MaxReroutes: 1}, nil)
	islands := libIslands(&p)
	_, err := libMazeRoute(&p, islands[0], islands[1], ctx)
	var failure *schematicRoutingFailure
	if !errors.As(err, &failure) || failure.Kind != "expanded-node-budget-exhausted" {
		t.Fatalf("error=%v", err)
	}
	if ctx.expanded != 1 {
		t.Fatalf("expanded=%d", ctx.expanded)
	}
}

func TestMazeAdvanceKeepsForeignXInsideOneSegment(t *testing.T) {
	p := powerLayoutPlan{Wires: []powerLayoutWire{{Net: "OTHER", Points: [][2]float64{{10, -10}, {10, 10}}}}}
	from := mazeState{X: 1, Y: 0, Dir: 1}
	next, ok := mazeAdvanceAcrossForeignX(&p, "N", from, 1, [4]int{-10, -10, 10, 10})
	if !ok || mazePoint(next) != [2]float64{15, 0} {
		t.Fatalf("next=%+v ok=%v", next, ok)
	}
	if err := validateLibRoutingEdge(&p, "N", mazePoint(from), mazePoint(next)); err != nil {
		t.Fatal(err)
	}
}

func TestLibJoinNetsDoesNotSealUnfinishedTreeBetweenFacingPins(t *testing.T) {
	a := mazeTestPart("A1", "N", layoutBBox{-20, -10, 0, 10}, powerLayoutPin{Number: "1", Net: "N", X: 5, Y: 0, Rotation: mazeTestRotation(0)})
	b := mazeTestPart("B1", "N", layoutBBox{15, -10, 35, 10}, powerLayoutPin{Number: "1", Net: "N", X: 10, Y: 0, Rotation: mazeTestRotation(180)})
	c := mazeTestPart("C1", "N", layoutBBox{100, -10, 120, 10}, powerLayoutPin{Number: "1", Net: "N", X: 95, Y: 0, Rotation: mazeTestRotation(180)})
	sealed := powerLayoutPlan{Placements: []powerLayoutPlacement{a, b, c}, Wires: []powerLayoutWire{{Net: "N", Points: [][2]float64{{5, 0}, {10, 0}}}}}
	if libIslandMergeCanContinue(&sealed, a.Pins[0], b.Pins[0]) {
		t.Fatal("one-grid opposed join incorrectly retained a future routing frontier")
	}
	completedSealed := powerLayoutPlan{Placements: []powerLayoutPlacement{a, b}, Wires: []powerLayoutWire{{Net: "N", Points: [][2]float64{{5, 0}, {10, 0}}}}}
	if libIslandMergeCanContinue(&completedSealed, a.Pins[0], b.Pins[0]) {
		t.Fatal("completed one-grid join incorrectly claimed a naming frontier")
	}

	branchableB := mazeTestPart("B1", "N", layoutBBox{20, -10, 40, 10}, powerLayoutPin{Number: "1", Net: "N", X: 15, Y: 0, Rotation: mazeTestRotation(180)})
	branchable := powerLayoutPlan{Placements: []powerLayoutPlacement{a, branchableB, c}, Wires: []powerLayoutWire{{Net: "N", Points: [][2]float64{{5, 0}, {15, 0}}}}}
	if !libIslandMergeCanContinue(&branchable, a.Pins[0], branchableB.Pins[0]) {
		t.Fatal("two-grid join should retain a branchable midspan")
	}

	p := powerLayoutPlan{Placements: []powerLayoutPlacement{a, b, c}}
	ctx, err := newSchematicRoutingContext(&SchematicRoutingOptions{MaxExpandedNodes: 20000, MaxReroutes: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := libJoinDirectNets(&p, map[string]string{"N": "direct"}, ctx); err == nil {
		t.Fatal("sealed placement should be rejected for placement repair")
	}
	if len(p.Wires) != 0 {
		t.Fatalf("failed pass leaked a partial sealed tree: %+v", p.Wires)
	}
}

func TestSchematicRoutingRerouteBudgetIsGlobal(t *testing.T) {
	ctx, err := newSchematicRoutingContext(&SchematicRoutingOptions{MaxExpandedNodes: 10, MaxReroutes: 4}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 10; i++ {
		ctx.beginReroute()
	}
	if ctx.reroutes != 4 {
		t.Fatalf("reroutes=%d, want global cap 4", ctx.reroutes)
	}
}

func TestModulePortDoesNotSpendMazeBudgetOnOptionalIslandMerge(t *testing.T) {
	a := mazeTestPart("A1", "PORT", layoutBBox{-20, -10, 0, 10}, powerLayoutPin{Number: "1", Net: "PORT", X: 5, Y: 0, Rotation: mazeTestRotation(0)})
	b := mazeTestPart("B1", "PORT", layoutBBox{200, -10, 220, 10}, powerLayoutPin{Number: "1", Net: "PORT", X: 195, Y: 0, Rotation: mazeTestRotation(180)})
	blocker := powerLayoutPlacement{Designator: "X1", BBox: layoutBBox{50, -100, 150, 100}}
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{a, b, blocker}}
	ctx, err := newSchematicRoutingContext(&SchematicRoutingOptions{MaxExpandedNodes: 20000, MaxReroutes: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := libJoinDirectNets(&p, map[string]string{"PORT": "module_port"}, ctx); err != nil {
		t.Fatal(err)
	}
	if ctx.expanded != 0 {
		t.Fatalf("module_port optional merge expanded %d maze nodes", ctx.expanded)
	}
	if len(libIslands(&p)) != 2 {
		t.Fatalf("module_port physical islands=%d, want 2 named islands", len(libIslands(&p)))
	}
	ctx.policies = map[string]string{"PORT": "module_port"}
	islands := libIslands(&p)
	if _, err := libMazeRoute(&p, islands[0], islands[1], ctx); err == nil || ctx.expanded != 0 {
		t.Fatalf("A* entry guard err=%v expanded=%d", err, ctx.expanded)
	}
}

func TestDirectNetBuildsSameSideFanoutBeforeExternalTree(t *testing.T) {
	connector := powerLayoutPlacement{Designator: "J1", BBox: layoutBBox{-20, -20, 0, 20}, Pins: []powerLayoutPin{
		{Number: "1", Net: "N", X: 10, Y: -10, Rotation: mazeTestRotation(0)},
		{Number: "2", Net: "OTHER", X: 10, Y: 0, Rotation: mazeTestRotation(0)},
		{Number: "3", Net: "N", X: 10, Y: 10, Rotation: mazeTestRotation(0)},
	}}
	source := mazeTestPart("A1", "N", layoutBBox{100, -10, 120, 10}, powerLayoutPin{Number: "1", Net: "N", X: 95, Y: 0, Rotation: mazeTestRotation(180)})
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{connector, source}}
	ctx, err := newSchematicRoutingContext(&SchematicRoutingOptions{MaxExpandedNodes: 20000, MaxReroutes: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := libJoinDirectNets(&p, map[string]string{"N": "direct", "OTHER": "module_port"}, ctx); err != nil {
		t.Fatal(err)
	}
	if len(libIslands(&p)) != 2 { // one N tree plus the unrelated OTHER pin
		t.Fatalf("physical islands=%d, wires=%+v", len(libIslands(&p)), p.Wires)
	}
	foundSafeTrunk := false
	for _, wire := range p.Wires {
		if wire.Net == "N" && len(wire.Points) == 2 && math.Abs(wire.Points[0][0]-20) <= 1e-6 && math.Abs(wire.Points[1][0]-20) <= 1e-6 {
			foundSafeTrunk = true
		}
	}
	if !foundSafeTrunk {
		t.Fatalf("same-side fanout did not use a safe trunk beyond foreign pin stems: %+v", p.Wires)
	}
}

func TestRepeatedDirectPinsSelectDirectFirstSchedule(t *testing.T) {
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{{Designator: "J1", BBox: layoutBBox{-20, -20, 0, 20}, Pins: []powerLayoutPin{
		{Number: "1", Net: "N", X: 10, Y: -10, Rotation: mazeTestRotation(0)},
		{Number: "2", Net: "N", X: 10, Y: 10, Rotation: mazeTestRotation(0)},
	}}}}
	if !libPreferDirectFirst(&p, map[string]string{"N": "direct"}) {
		t.Fatal("repeated same-side direct net did not select fanout-first routing")
	}
	p.Placements[0].Pins[1].Net = "OTHER"
	if libPreferDirectFirst(&p, map[string]string{"N": "direct", "OTHER": "direct"}) {
		t.Fatal("unique same-side direct nets should retain the rail-first fast path")
	}
}

func TestMandatoryDirectTreePrecedesOptionalModulePortJoin(t *testing.T) {
	connector := powerLayoutPlacement{Designator: "J1", BBox: layoutBBox{-20, -30, 0, 30}, Pins: []powerLayoutPin{
		{Number: "P1", Net: "PORT", X: 10, Y: -20, Rotation: mazeTestRotation(0)},
		{Number: "P2", Net: "PORT", X: 10, Y: -10, Rotation: mazeTestRotation(0)},
		{Number: "N1", Net: "MUST", X: 10, Y: 10, Rotation: mazeTestRotation(0)},
		{Number: "N2", Net: "MUST", X: 10, Y: 20, Rotation: mazeTestRotation(0)},
	}}
	p := powerLayoutPlan{Placements: []powerLayoutPlacement{connector}}
	if err := libJoinDirectNets(&p, map[string]string{"PORT": "module_port", "MUST": "direct"}); err != nil {
		t.Fatal(err)
	}
	if len(p.Wires) == 0 || p.Wires[0].Net != "MUST" {
		t.Fatalf("optional module_port consumed routing order before direct tree: %+v", p.Wires)
	}
}
