package pcbsolve_test

import (
	"context"
	"math"
	"slices"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcblayout"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbmodel"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbsolve"
)

func pad(number, net string, x, y float64) pcbmodel.Pad {
	return pcbmodel.Pad{Number: number, Net: net, Layer: 1, Center: pcbmodel.Point{x, y}, Width: 10, Height: 10, Shape: "rect"}
}

func solveBoard() pcbmodel.Board {
	return pcbmodel.Board{SchemaVersion: 1, Units: "mil", SemanticHash: "fixture-v1",
		Outline: pcbmodel.Outline{Source: "polygon", BBox: pcbmodel.BBox{MinX: 0, MinY: 0, MaxX: 300, MaxY: 220}, Points: []pcbmodel.Point{{0, 0}, {300, 0}, {300, 220}, {0, 220}}},
		Stackup: pcbmodel.Stackup{Copper: []pcbmodel.Layer{{ID: 1, Name: "TOP", Role: "signal"}, {ID: 2, Name: "BOTTOM", Role: "signal"}}},
		Rules:   pcbmodel.Rules{Source: "fixture", Clearance: 5, TrackTrack: 5, CopperToEdge: 5, TrackWidthMinimum: 4, ViaDiameter: 16, ViaDrill: 8},
		Components: []pcbmodel.Component{
			{Ref: "U1", Side: 1, Anchor: pcbmodel.Point{30, 100}, Locked: true, BBox: pcbmodel.BBox{MinX: 20, MinY: 65, MaxX: 40, MaxY: 135}, Pads: []pcbmodel.Pad{pad("1", "A", 30, 80), pad("2", "B", 30, 120)}},
			{Ref: "J1", Side: 1, Anchor: pcbmodel.Point{270, 100}, Locked: true, BBox: pcbmodel.BBox{MinX: 260, MinY: 65, MaxX: 280, MaxY: 135}, Pads: []pcbmodel.Pad{pad("1", "A", 270, 80), pad("2", "B", 270, 120)}},
			{Ref: "M1", Side: 1, Anchor: pcbmodel.Point{150, 100}, BBox: pcbmodel.BBox{MinX: 140, MinY: 70, MaxX: 160, MaxY: 130}, Pads: []pcbmodel.Pad{pad("1", "X", 150, 80), pad("2", "Y", 150, 120)}},
			{Ref: "C1", Side: 1, Anchor: pcbmodel.Point{180, 100}, BBox: pcbmodel.BBox{MinX: 170, MinY: 90, MaxX: 190, MaxY: 110}, Pads: []pcbmodel.Pad{pad("1", "X", 175, 100), pad("2", "GND", 185, 100)}},
		},
	}
}

func solveRequest() pcbsolve.Request {
	return pcbsolve.Request{SchemaVersion: 1, Units: "mil", MaxStates: 200000, MaxCandidates: 3,
		Layout:  pcblayout.Request{MinGap: 5, MaxStates: 100, MaxCandidates: 3, FixedRefs: []string{"U1", "J1"}, Groups: []pcblayout.Group{{ID: "blocker", AnchorRef: "M1", Refs: []string{"M1", "C1"}, Offsets: []pcblayout.Offset{{}, {Y: 60}}}}},
		Routing: pcbsolve.RoutingRequest{Step: 10, MaxDetour: 0, MaxStates: 100000, Demands: []pcbsolve.Demand{{ID: "a", Net: "A", From: "U1.1", To: "J1.1", Width: 4, Layers: []int{1}}, {ID: "b", Net: "B", From: "U1.2", To: "J1.2", Width: 4, Layers: []int{1}}}},
	}
}

func TestSharedBudgetExhaustionIsIncompleteWithoutMoveAttribution(t *testing.T) {
	board, req := solveBoard(), solveRequest()
	req.MaxStates = 2
	req.Layout.MaxStates = 1
	req.Routing.MaxStates = 100
	rep, err := pcbsolve.Solve(context.Background(), board, req)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != pcbsolve.Incomplete || !rep.Exhausted || rep.States > rep.Budget {
		t.Fatalf("shared budget was not enforced: %+v", rep)
	}
	if len(rep.Candidates) != 0 {
		t.Fatalf("budget-exhausted solve returned candidate: %+v", rep.Candidates)
	}
}

func TestRouteFeedbackMovesWholeGroupAndPreservesFixedObjects(t *testing.T) {
	board, req := solveBoard(), solveRequest()
	rep, err := pcbsolve.Solve(context.Background(), board, req)
	if err != nil || rep.Status != pcbsolve.Pass || len(rep.Candidates) != 1 {
		t.Fatalf("solve: %v %+v", err, rep)
	}
	if len(rep.Attempts) < 2 || rep.Attempts[0].Status == "found" || len(rep.Attempts[0].Blockers) == 0 {
		t.Fatalf("baseline did not produce actionable route feedback: %+v", rep.Attempts)
	}
	candidate := rep.Candidates[0]
	m, _ := candidate.Board.Component("M1")
	c, _ := candidate.Board.Component("C1")
	u, _ := candidate.Board.Component("U1")
	j, _ := candidate.Board.Component("J1")
	if m.Anchor != (pcbmodel.Point{150, 160}) || c.Anchor != (pcbmodel.Point{180, 160}) {
		t.Fatalf("whole group did not move: M=%v C=%v", m.Anchor, c.Anchor)
	}
	if u.Anchor != (pcbmodel.Point{30, 100}) || j.Anchor != (pcbmodel.Point{270, 100}) {
		t.Fatalf("fixed endpoints moved: U=%v J=%v", u.Anchor, j.Anchor)
	}
	if len(candidate.MoveReasons) == 0 || candidate.Metrics.MovedGroups != 1 || candidate.Metrics.NewVias != 0 {
		t.Fatalf("missing feedback evidence/metrics: %+v", candidate)
	}
	if err := pcbsolve.Check(context.Background(), board, req, candidate); err != nil {
		t.Fatalf("independent check rejected result: %v", err)
	}
}

func TestTinyIndependentEnumerationAgreesOnMinimumMoveVector(t *testing.T) {
	// Independent reference for this deliberately tiny discrete case. It does
	// not call production placement, routing or geometry helpers: two fixed
	// horizontal centerlines are checked against hand-enumerated pad squares.
	type point struct{ x, y float64 }
	offsets := []float64{0, 60}
	bestDistance := math.Inf(1)
	for _, dy := range offsets {
		obstacles := []point{{150, 80 + dy}, {150, 120 + dy}, {175, 100 + dy}, {185, 100 + dy}}
		legal := true
		for _, routeY := range []float64{80, 120} {
			for _, obstacle := range obstacles {
				// pad half extent 5 + route half width 2 + clearance 5.
				if obstacle.x > 30 && obstacle.x < 270 && math.Abs(obstacle.y-routeY) < 12 {
					legal = false
				}
			}
		}
		if legal && math.Abs(dy) < bestDistance {
			bestDistance = math.Abs(dy)
		}
	}
	if bestDistance != 60 {
		t.Fatalf("independent enumeration bug: best=%v", bestDistance)
	}
	report, err := pcbsolve.Solve(context.Background(), solveBoard(), solveRequest())
	if err != nil || len(report.Candidates) == 0 {
		t.Fatal(err)
	}
	if report.Candidates[0].Metrics.MovedGroups != 1 || report.Candidates[0].Metrics.TotalDistance != bestDistance {
		t.Fatalf("solver missed finite-model optimum: %+v", report.Candidates[0].Metrics)
	}
}

func TestTwoLayerSolveUsesMeasuredViaPairWhenTopIsBlocked(t *testing.T) {
	board := solveBoard()
	board.Components = board.Components[:2]
	board.Tracks = []pcbmodel.Track{{ID: "wall", Net: "WALL", Layer: 1, Width: 4, Points: []pcbmodel.Point{{150, 5}, {150, 215}}}}
	req := solveRequest()
	req.Layout.Groups = nil
	req.Layout.MaxCandidates = 1
	req.Routing.MaxDetour = 30
	req.Routing.Demands = []pcbsolve.Demand{{ID: "a", Net: "A", From: "U1.1", To: "J1.1", Width: 4, Layers: []int{1, 2}, MaxVias: 2}}
	rep, err := pcbsolve.Solve(context.Background(), board, req)
	if err != nil || rep.Status != pcbsolve.Pass || len(rep.Candidates) != 1 {
		t.Fatalf("two-layer solve: %v %+v", err, rep)
	}
	candidate := rep.Candidates[0]
	if candidate.Metrics.NewVias != 2 || len(candidate.Routes) != 1 || len(candidate.Routes[0].Routes) != 3 || candidate.Routes[0].Routes[1].Layer != 2 {
		t.Fatalf("missing TOP/BOTTOM/TOP witness: %+v", candidate.Routes)
	}
	if err := pcbsolve.Check(context.Background(), board, req, candidate); err != nil {
		t.Fatal(err)
	}

	topOnly := req
	topOnly.Routing.Demands[0].Layers = []int{1}
	topOnly.Routing.Demands[0].MaxVias = 0
	blocked, err := pcbsolve.Solve(context.Background(), board, topOnly)
	if err != nil || blocked.Status == pcbsolve.Pass {
		t.Fatalf("TOP-only demand crossed wall: %v %+v", err, blocked)
	}
}

func TestCheckRejectsTamperedPathAndStaleBoard(t *testing.T) {
	board, req := solveBoard(), solveRequest()
	rep, err := pcbsolve.Solve(context.Background(), board, req)
	if err != nil || len(rep.Candidates) != 1 {
		t.Fatal(err)
	}
	candidate := rep.Candidates[0]
	candidate.Routes[0].Routes[0].Points[0][0]++
	if err := pcbsolve.Check(context.Background(), board, req, candidate); err == nil {
		t.Fatal("tampered route accepted")
	}
	candidate = rep.Candidates[0]
	board.SemanticHash = "fresh-board"
	if err := pcbsolve.Check(context.Background(), board, req, candidate); err == nil {
		t.Fatal("stale board hash accepted")
	}
}

func TestFourLayerStackupIsModelledButNotClaimedByTwoLayerSolver(t *testing.T) {
	board, req := solveBoard(), solveRequest()
	board.Stackup.Copper = []pcbmodel.Layer{{ID: 1, Name: "TOP", Role: "signal"}, {ID: 15, Name: "GND", Role: "plane", PlaneNet: "GND"}, {ID: 16, Name: "PWR", Role: "plane", PlaneNet: "+3V3"}, {ID: 2, Name: "BOTTOM", Role: "signal"}}
	if _, err := pcbsolve.Solve(context.Background(), board, req); err == nil {
		t.Fatal("four-layer board was claimed by the two-layer solver")
	}
}

func TestExistingDifferentNetCopperRegionBlocksTracksAndVias(t *testing.T) {
	board := solveBoard()
	board.Components = board.Components[:2]
	board.Regions = []pcbmodel.Region{{ID: "fixed-copper", Kind: "copper", Net: "GND", Layer: 1, Points: []pcbmodel.Point{{140, 5}, {160, 5}, {160, 215}, {140, 215}}}}
	req := solveRequest()
	req.Layout.Groups = nil
	req.Layout.MaxCandidates = 1
	req.Routing.MaxDetour = 20
	req.Routing.Demands = []pcbsolve.Demand{{ID: "a", Net: "A", From: "U1.1", To: "J1.1", Width: 4, Layers: []int{1}, MaxVias: 0}}
	rep, err := pcbsolve.Solve(context.Background(), board, req)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status == pcbsolve.Pass {
		t.Fatalf("route crossed different-net materialized copper: %+v", rep.Candidates)
	}
}

func TestMetamorphicTranslationRotationAndInputOrderPreserveConclusion(t *testing.T) {
	baseBoard, baseReq := solveBoard(), solveRequest()
	base, err := pcbsolve.Solve(context.Background(), baseBoard, baseReq)
	if err != nil || len(base.Candidates) == 0 {
		t.Fatal(err)
	}
	assertEquivalent := func(name string, board pcbmodel.Board, req pcbsolve.Request) {
		t.Helper()
		report, err := pcbsolve.Solve(context.Background(), board, req)
		if err != nil || report.Status != base.Status || len(report.Candidates) == 0 {
			t.Fatalf("%s: %v %+v", name, err, report)
		}
		a, b := base.Candidates[0].Metrics, report.Candidates[0].Metrics
		if a.MovedGroups != b.MovedGroups || a.Rotations != b.Rotations || a.NewVias != b.NewVias || math.Abs(a.TotalDistance-b.TotalDistance) > 1e-6 || math.Abs(a.RouteLength-b.RouteLength) > 1e-6 || a.Bends != b.Bends {
			t.Fatalf("%s changed electrical/metric conclusion: base=%+v got=%+v", name, a, b)
		}
		if err := pcbsolve.Check(context.Background(), board, req, report.Candidates[0]); err != nil {
			t.Fatalf("%s independent check: %v", name, err)
		}
	}

	translated := transformSolveBoard(baseBoard, pcbmodel.Transform{DX: 400, DY: -250})
	translated.SemanticHash = "translated"
	assertEquivalent("translate", translated, baseReq)

	rotation := pcbmodel.Transform{Pivot: pcbmodel.Point{150, 110}, RotationDelta: 90}
	rotated := transformSolveBoard(baseBoard, rotation)
	rotated.SemanticHash = "rotated"
	rotatedReq := baseReq
	rotatedReq.Layout.Groups = append([]pcblayout.Group(nil), baseReq.Layout.Groups...)
	for i := range rotatedReq.Layout.Groups {
		rotatedReq.Layout.Groups[i].Offsets = append([]pcblayout.Offset(nil), rotatedReq.Layout.Groups[i].Offsets...)
		for j, offset := range rotatedReq.Layout.Groups[i].Offsets {
			rotatedReq.Layout.Groups[i].Offsets[j] = pcblayout.Offset{X: -offset.Y, Y: offset.X}
		}
	}
	assertEquivalent("rotate-90", rotated, rotatedReq)

	reorderedBoard := baseBoard
	reorderedBoard.Components = append([]pcbmodel.Component(nil), baseBoard.Components...)
	slices.Reverse(reorderedBoard.Components)
	reorderedReq := baseReq
	reorderedReq.Routing.Demands = append([]pcbsolve.Demand(nil), baseReq.Routing.Demands...)
	slices.Reverse(reorderedReq.Routing.Demands)
	assertEquivalent("input-order", reorderedBoard, reorderedReq)
}

func transformSolveBoard(board pcbmodel.Board, transform pcbmodel.Transform) pcbmodel.Board {
	out := board
	out.Outline.Points = make([]pcbmodel.Point, len(board.Outline.Points))
	box := pcbmodel.BBox{MinX: math.Inf(1), MinY: math.Inf(1), MaxX: math.Inf(-1), MaxY: math.Inf(-1)}
	for i, point := range board.Outline.Points {
		out.Outline.Points[i] = transform.Point(point)
		box.MinX, box.MaxX = math.Min(box.MinX, out.Outline.Points[i][0]), math.Max(box.MaxX, out.Outline.Points[i][0])
		box.MinY, box.MaxY = math.Min(box.MinY, out.Outline.Points[i][1]), math.Max(box.MaxY, out.Outline.Points[i][1])
	}
	out.Outline.BBox = box
	out.Components = make([]pcbmodel.Component, len(board.Components))
	for i, component := range board.Components {
		out.Components[i] = transform.Component(component)
	}
	out.Tracks = make([]pcbmodel.Track, len(board.Tracks))
	for i, track := range board.Tracks {
		out.Tracks[i] = transform.Track(track)
	}
	out.Vias = make([]pcbmodel.Via, len(board.Vias))
	for i, via := range board.Vias {
		out.Vias[i] = transform.Via(via)
	}
	out.Regions = make([]pcbmodel.Region, len(board.Regions))
	for i, region := range board.Regions {
		out.Regions[i] = transform.Region(region)
	}
	return out
}

func TestThroughHolePadBlocksBothCopperLayers(t *testing.T) {
	board := solveBoard()
	board.Components = board.Components[:2]
	board.Components[0].Pads[0].Layer = pcbmodel.LayerMulti
	board.Components[1].Pads[0].Layer = pcbmodel.LayerMulti
	board.Components = append(board.Components, pcbmodel.Component{Ref: "TH1", Side: 1, Anchor: pcbmodel.Point{150, 80}, BBox: pcbmodel.BBox{MinX: 145, MinY: 75, MaxX: 155, MaxY: 85}, Pads: []pcbmodel.Pad{{Number: "1", Net: "BLOCK", Layer: pcbmodel.LayerMulti, Center: pcbmodel.Point{150, 80}, Width: 12, Height: 12, Shape: "circle"}}})
	req := solveRequest()
	req.Layout.Groups = nil
	req.Layout.MaxCandidates = 1
	req.Routing.Demands = []pcbsolve.Demand{{ID: "a", Net: "A", From: "U1.1", To: "J1.1", Width: 4, Layers: []int{2}, MaxVias: 0}}
	report, err := pcbsolve.Solve(context.Background(), board, req)
	if err != nil {
		t.Fatal(err)
	}
	if report.Status == pcbsolve.Pass {
		t.Fatalf("BOTTOM route crossed through-hole pad: %+v", report.Candidates)
	}
}

func TestRoutingExhaustionDoesNotTriggerFurtherMoveAttribution(t *testing.T) {
	board, req := solveBoard(), solveRequest()
	layout, err := pcblayout.Generate(board, req.Layout)
	if err != nil {
		t.Fatal(err)
	}
	req.MaxStates = layout.States + 1
	req.Routing.MaxStates = 100
	report, err := pcbsolve.Solve(context.Background(), board, req)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Exhausted || len(report.Candidates) != 0 || len(report.Attempts) != 1 {
		t.Fatalf("budget exhaustion was treated as route feedback: %+v", report)
	}
}

func TestRouteFeedbackCanDisplaceASecondCompleteGroup(t *testing.T) {
	board, req := solveBoard(), solveRequest()
	board.Components = append(board.Components,
		pcbmodel.Component{Ref: "M2", Side: 1, Anchor: pcbmodel.Point{150, 160}, BBox: pcbmodel.BBox{MinX: 140, MinY: 140, MaxX: 160, MaxY: 180}, Pads: []pcbmodel.Pad{pad("1", "Z", 150, 150), pad("2", "W", 150, 170)}},
		pcbmodel.Component{Ref: "C2", Side: 1, Anchor: pcbmodel.Point{180, 160}, BBox: pcbmodel.BBox{MinX: 170, MinY: 150, MaxX: 190, MaxY: 170}, Pads: []pcbmodel.Pad{pad("1", "Z", 175, 160), pad("2", "GND", 185, 160)}},
	)
	req.Layout.Groups = append(req.Layout.Groups, pcblayout.Group{ID: "dependent", AnchorRef: "M2", Refs: []string{"M2", "C2"}, Offsets: []pcblayout.Offset{{}, {X: 60}}})
	report, err := pcbsolve.Solve(context.Background(), board, req)
	if err != nil || report.Status != pcbsolve.Pass || len(report.Candidates) == 0 {
		t.Fatalf("multi-group solve: %v %+v", err, report)
	}
	candidate := report.Candidates[0]
	if candidate.Metrics.MovedGroups != 2 {
		t.Fatalf("dependent group did not make way as a complete group: %+v", candidate)
	}
	m2, _ := candidate.Board.Component("M2")
	c2, _ := candidate.Board.Component("C2")
	if m2.Anchor != (pcbmodel.Point{210, 160}) || c2.Anchor != (pcbmodel.Point{240, 160}) {
		t.Fatalf("dependent group projection is incomplete: M2=%v C2=%v", m2.Anchor, c2.Anchor)
	}
	if err := pcbsolve.Check(context.Background(), board, req, candidate); err != nil {
		t.Fatal(err)
	}
}
