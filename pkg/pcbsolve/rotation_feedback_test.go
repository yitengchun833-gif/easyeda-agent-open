package pcbsolve_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcblayout"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbmodel"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbsolve"
)

func TestRouteAvoidanceNeedsTranslationThenWholeGroupRotation(t *testing.T) {
	b, r := solveBoard(), automaticRequest(t)
	// A tall pad blocks both straight channels at every allowed translation.
	// Rotating at the initial anchor would place C1 on the fixed F1. The only
	// feasible family first changes the group's position, then its orientation.
	b.Components[2].Pads = []pcbmodel.Pad{{Number: "1", Net: "BLOCK", Layer: 1, Center: pcbmodel.Point{150, 100}, Width: 10, Height: 60, Shape: "rect"}}
	b.Components[3].Anchor = pcbmodel.Point{150, 40}
	b.Components[3].BBox = pcbmodel.BBox{MinX: 140, MinY: 30, MaxX: 160, MaxY: 50}
	b.Components[3].Pads = []pcbmodel.Pad{pad("1", "LOCAL", 150, 40)}
	b.Components = append(b.Components, pcbmodel.Component{Ref: "F1", Side: 1, Locked: true, Anchor: pcbmodel.Point{210, 100}, BBox: pcbmodel.BBox{MinX: 200, MinY: 90, MaxX: 220, MaxY: 110}, Pads: []pcbmodel.Pad{pad("1", "FIXED", 210, 100)}})
	r.Layout.FixedRefs = append(r.Layout.FixedRefs, "F1")
	r.Layout.Groups[0].FixedAxes = []string{"y"}
	r.Layout.Groups[0].AllowedRotationDeltas = []float64{0, 90}
	r.Layout.Groups[0].TranslationSearch = &pcblayout.TranslationSearch{Step: 10, MaxDistance: 40}
	r.Layout.MaxStates = 500
	r.MaxCandidates = 1
	seeds, err := pcblayout.Generate(b, r.Layout)
	if err != nil || len(seeds.Candidates) != 1 || len(seeds.Candidates[0].Moves) != 0 {
		t.Fatalf("initial rotated pose must be mechanically unavailable: %v %+v", err, seeds)
	}
	lockedOrientation := r
	lockedOrientation.Layout.Groups = append([]pcblayout.Group(nil), r.Layout.Groups...)
	lockedOrientation.Layout.Groups[0].AllowedRotationDeltas = []float64{0}
	blocked, err := pcbsolve.Solve(context.Background(), b, lockedOrientation)
	if err != nil || blocked.Status == pcbsolve.Pass || blocked.Exhausted {
		t.Fatalf("translation alone must not open the declared straight channels: %v %+v", err, blocked)
	}
	report, err := pcbsolve.Solve(context.Background(), b, r)
	if err != nil || report.Status != pcbsolve.Pass {
		t.Fatalf("allowed translated rotation was missed: %v status=%s reason=%s", err, report.Status, report.Reason)
	}
	c := report.Candidates[0]
	if len(c.Moves) != 1 || c.Moves[0].RotationDelta != 90 || c.Moves[0].DX == 0 || c.Moves[0].DY != 0 || len(c.Routes) != 2 || c.Metrics.NewVias != 0 {
		t.Fatalf("wrong rigid repair or joint routes: %+v", c)
	}
	m, _ := c.Board.Component("M1")
	cap, _ := c.Board.Component("C1")
	if cap.Anchor[0]-m.Anchor[0] != 60 || cap.Anchor[1]-m.Anchor[1] != 0 || m.Side != 1 || cap.Side != 1 {
		t.Fatal("rotation did not preserve the complete group's relative layout")
	}
	for _, ref := range []string{"U1", "J1", "F1"} {
		old, _ := b.Component(ref)
		got, _ := c.Board.Component(ref)
		if !reflect.DeepEqual(old, got) {
			t.Fatalf("fixed object %s changed", ref)
		}
	}
	if err := pcbsolve.Check(context.Background(), b, r, c); err != nil {
		t.Fatal(err)
	}
	if len(report.Attempts) < 2 || len(report.Attempts[0].Conflicts) == 0 || len(c.MoveReasons) == 0 {
		t.Fatal("missing actual conflict and move-reason evidence")
	}
	t.Logf("states=%d attempts=%d displacement=%g rotation=%g", report.States, len(report.Attempts), c.Metrics.TotalDistance, c.Moves[0].RotationDelta)
}
