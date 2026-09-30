package pcbsolve_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcblayout"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbmodel"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbsolve"
)

// Inputs contain a permitted search radius, never an answer coordinate.
func automaticRequest(t *testing.T) pcbsolve.Request {
	t.Helper()
	r := solveRequest()
	r.Layout.Groups[0].Offsets = nil
	r.Layout.Groups[0].TranslationSearch = &pcblayout.TranslationSearch{Step: 10, MaxDistance: 80}
	r.Layout.MaxStates = 200
	return r
}

func TestAutomaticRouteFeedbackNeedsNoAnswerOffsets(t *testing.T) {
	b, r := solveBoard(), automaticRequest(t)
	rep, err := pcbsolve.Solve(context.Background(), b, r)
	if err != nil || rep.Status != pcbsolve.Pass || len(rep.Candidates) == 0 {
		t.Fatalf("bounded automatic displacement failed: %v status=%s reason=%s", err, rep.Status, rep.Reason)
	}
	c := rep.Candidates[0]
	if len(c.Moves) != 1 || c.Metrics.TotalDistance <= 0 || c.Metrics.TotalDistance > 80 {
		t.Fatalf("invalid move: %+v", c.Moves)
	}
	m0, _ := b.Component("M1")
	m1, _ := c.Board.Component("M1")
	p0, _ := b.Component("C1")
	p1, _ := c.Board.Component("C1")
	if m1.Anchor[0]-m0.Anchor[0] != p1.Anchor[0]-p0.Anchor[0] || m1.Anchor[1]-m0.Anchor[1] != p1.Anchor[1]-p0.Anchor[1] {
		t.Fatal("group was torn apart")
	}
	if len(pcblayout.Check(c.Board, r.Layout)) != 0 {
		t.Fatal("automatic displacement left a mechanical violation")
	}
	if !reflect.DeepEqual(b.Tracks, c.Board.Tracks) || !reflect.DeepEqual(b.Vias, c.Board.Vias) {
		t.Fatal("reservation became actual copper")
	}
	if err := pcbsolve.Check(context.Background(), b, r, c); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(rep)
	for _, field := range []string{`"conflicts"`, `"demandId"`, `"object"`, `"segment"`, `"reservations"`} {
		if !strings.Contains(string(encoded), field) {
			t.Fatalf("missing trace/reservation %s", field)
		}
	}
}

func TestAutomaticMovesCannotHideOmittedExternalConnection(t *testing.T) {
	b, r := solveBoard(), automaticRequest(t)
	b.Components[0].Pads = append(b.Components[0].Pads, pad("3", "X", 30, 100))
	_, err := pcbsolve.Solve(context.Background(), b, r)
	if err == nil || !strings.Contains(err.Error(), "external connection") {
		t.Fatalf("missing external net X was ignored: %v", err)
	}
}

func TestCheckRejectsUndeclaredMoveEvenWhenRoutesRemainValid(t *testing.T) {
	b, r := solveBoard(), solveRequest()
	rep, err := pcbsolve.Solve(context.Background(), b, r)
	if err != nil || len(rep.Candidates) == 0 {
		t.Fatal(err)
	}
	c := rep.Candidates[0]
	c.Moves[0].DY = 61 // Still clears both lines, but is not an allowed offset.
	c.Board, _, err = pcblayout.ApplyGroup(b, r.Layout.Groups[0], c.Moves[0])
	if err != nil {
		t.Fatal(err)
	}
	if err := pcbsolve.Check(context.Background(), b, r, c); err == nil {
		t.Fatal("checker accepted a move outside independently declared choices")
	}
}

func TestMovedBaselineOverlapIsNotWhitelisted(t *testing.T) {
	b := solveBoard()
	b.Components = b.Components[2:]
	b.Components[1].BBox = pcbmodel.BBox{MinX: 155, MinY: 90, MaxX: 180, MaxY: 110}
	r := pcblayout.Request{MinGap: 5, MaxStates: 20, MaxCandidates: 10,
		Groups: []pcblayout.Group{{ID: "m", Refs: []string{"M1"}, AnchorRef: "M1", Offsets: []pcblayout.Offset{{}, {X: 10}}}}}
	rep, err := pcblayout.Generate(b, r)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range rep.Candidates {
		if len(c.Moves) > 0 {
			t.Fatal("worsened baseline overlap was accepted by object-pair whitelist")
		}
	}
}

func TestAutomaticSharedChannel(t *testing.T) {
	var b pcbmodel.Board
	var r pcbsolve.Request
	raw, err := os.ReadFile("testdata/automatic-channel/board.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &b); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile("testdata/automatic-channel/request.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(raw, &r); err != nil {
		t.Fatal(err)
	}
	baseline := r
	baseline.Layout.Groups = nil
	for _, d := range r.Routing.Demands {
		one := baseline
		one.Routing.Demands = []pcbsolve.Demand{d}
		rep, err := pcbsolve.Solve(context.Background(), b, one)
		if err != nil || rep.Status != pcbsolve.Pass {
			t.Fatalf("single demand %s must fit: %v %+v", d.ID, err, rep)
		}
	}
	blocked, err := pcbsolve.Solve(context.Background(), b, baseline)
	if err != nil || blocked.Status == pcbsolve.Pass || blocked.Exhausted {
		t.Fatalf("baseline must demonstrate channel conflict: %v %+v", err, blocked)
	}
	conflictingNets := false
	for _, a := range blocked.Attempts {
		for _, c := range a.Conflicts {
			conflictingNets = conflictingNets || c.ConflictDemandID != ""
		}
	}
	if !conflictingNets {
		t.Fatal("no actual selected-path competition observed")
	}
	rep, err := pcbsolve.Solve(context.Background(), b, r)
	if err != nil || rep.Status != pcbsolve.Pass {
		t.Fatalf("automatic widening failed: %v %+v", err, rep)
	}
	c := rep.Candidates[0]
	if c.Metrics.MovedGroups != 1 || c.Metrics.NewVias != 0 {
		t.Fatalf("unexpected repair: %+v", c.Metrics)
	}
	for _, ref := range []string{"U1", "J1"} {
		before, _ := b.Component(ref)
		after, _ := c.Board.Component(ref)
		if !reflect.DeepEqual(before, after) {
			t.Fatal("fixed object moved")
		}
	}
	if !reflect.DeepEqual(b.Regions, c.Board.Regions) {
		t.Fatal("fixed walls changed")
	}
	if err := pcbsolve.Check(context.Background(), b, r, c); err != nil {
		t.Fatal(err)
	}
	t.Logf("states=%d attempts=%d displacement=%g routes=%d", rep.States, len(rep.Attempts), c.Metrics.TotalDistance, len(c.Routes))
}

func TestAutomaticRecursiveDisplacementAndFixedGroupBacktrack(t *testing.T) {
	for _, locked := range []bool{false, true} {
		t.Run(fmt.Sprint("locked=", locked), func(t *testing.T) {
			b, r := solveBoard(), automaticRequest(t)
			r.MaxCandidates = 1
			r.Layout.MaxStates = 2000
			r.Layout.Groups[0].FixedAxes = []string{"x", "rotation"}
			r.Layout.Keepouts = []pcblayout.Keepout{{ID: "lower-mechanical-area", BBox: pcbmodel.BBox{MinX: 100, MinY: 0, MaxX: 210, MaxY: 60}, Layers: []int{1}}}
			b.Components = append(b.Components,
				pcbmodel.Component{Ref: "M2", Side: 1, Locked: locked, Anchor: pcbmodel.Point{150, 160}, BBox: pcbmodel.BBox{MinX: 140, MinY: 140, MaxX: 160, MaxY: 180}, Pads: []pcbmodel.Pad{pad("1", "Z", 150, 150)}},
				pcbmodel.Component{Ref: "C2", Side: 1, Locked: locked, Anchor: pcbmodel.Point{180, 160}, BBox: pcbmodel.BBox{MinX: 170, MinY: 150, MaxX: 190, MaxY: 170}, Pads: []pcbmodel.Pad{pad("1", "Z", 180, 160)}})
			r.Layout.Groups = append(r.Layout.Groups, pcblayout.Group{ID: "dependent", Refs: []string{"M2", "C2"}, AnchorRef: "M2", FixedAxes: []string{"y", "rotation"}, TranslationSearch: &pcblayout.TranslationSearch{Step: 10, MaxDistance: 60}})
			rep, err := pcbsolve.Solve(context.Background(), b, r)
			if err != nil {
				t.Fatal(err)
			}
			if locked {
				if rep.Status == pcbsolve.Pass {
					t.Fatal("solver moved locked group")
				}
				return
			}
			if rep.Status != pcbsolve.Pass {
				t.Fatalf("recursive displacement failed: %s states=%d rejected=%d", rep.Reason, rep.States, len(rep.Layout.Rejected))
			}
			c := rep.Candidates[0]
			if c.Metrics.MovedGroups != 2 {
				t.Fatalf("expected two whole groups: %+v", c.Moves)
			}
			if err := pcbsolve.Check(context.Background(), b, r, c); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAutomaticBudgetHasNoInventedBlocker(t *testing.T) {
	b, r := solveBoard(), automaticRequest(t)
	r.Routing.MaxStates = 1
	rep, err := pcbsolve.Solve(context.Background(), b, r)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Exhausted || len(rep.Candidates) != 0 || len(rep.Attempts) != 1 {
		t.Fatalf("unexpected exhausted search: %+v", rep)
	}
	if len(rep.Attempts[0].Blockers) != 0 || len(rep.Attempts[0].Conflicts) != 0 {
		t.Fatal("budget became an obstruction diagnosis")
	}
}

func TestAutomaticGridLimitHasNoInventedBlocker(t *testing.T) {
	b, r := solveBoard(), automaticRequest(t)
	r.Routing.Step = .001
	r.Routing.MaxDetour = 80
	rep, err := pcbsolve.Solve(context.Background(), b, r)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.Exhausted || len(rep.Attempts) != 1 || len(rep.Attempts[0].Conflicts) != 0 || len(rep.Attempts[0].Blockers) != 0 {
		t.Fatalf("grid resource limit became displacement evidence: exhausted=%t attempts=%d", rep.Exhausted, len(rep.Attempts))
	}
}

func TestCheckRejectsTruncatedMembersAndTamperedReservations(t *testing.T) {
	b, r := solveBoard(), automaticRequest(t)
	rep, err := pcbsolve.Solve(context.Background(), b, r)
	if err != nil || len(rep.Candidates) == 0 {
		t.Fatal(err)
	}
	for _, tamper := range []string{"members", "reservation", "radius", "empty-route", "extra-demand", "endpoint-layer", "metrics"} {
		t.Run(tamper, func(t *testing.T) {
			raw, _ := json.Marshal(rep.Candidates[0])
			var c pcbsolve.Candidate
			_ = json.Unmarshal(raw, &c)
			switch tamper {
			case "members":
				c.Moves[0].Refs = c.Moves[0].Refs[:1]
			case "reservation":
				c.Reservations[0].Clearance = 0
			case "radius":
				c.Moves[0].DY = 100
			case "empty-route":
				c.Routes[0].Routes[0].Points = nil
			case "extra-demand":
				extra := c.Routes[0]
				extra.DemandID = "extra"
				c.Routes = append(c.Routes, extra)
			case "endpoint-layer":
				c.Routes[0].Routes[0].Layer = 2
			case "metrics":
				c.Metrics.TotalDistance = 0
			}
			if err := pcbsolve.Check(context.Background(), b, r, c); err == nil {
				t.Fatal("tampered candidate accepted")
			}
		})
	}
}

func TestDisplacementMustPreserveExistingPadCopperContact(t *testing.T) {
	b, r := solveBoard(), solveRequest()
	b.Tracks = []pcbmodel.Track{{ID: "existing-feed", Net: "GND", Layer: 1, Width: 4, Points: []pcbmodel.Point{{185, 100}, {205, 100}}}}
	rep, err := pcbsolve.Solve(context.Background(), b, r)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status == pcbsolve.Pass {
		t.Fatal("module moved away from existing fixed feed copper")
	}
}

func TestAutomaticSearchRecomputesExternalEndpoints(t *testing.T) {
	b, r := solveBoard(), automaticRequest(t)
	left, right := pad("3", "EXT", 30, 100), pad("3", "EXT", 150, 100)
	left.Layer, right.Layer = pcbmodel.LayerMulti, pcbmodel.LayerMulti
	b.Components[0].Pads = append(b.Components[0].Pads, left)
	b.Components[2].Pads = append(b.Components[2].Pads, right)
	r.Routing.Demands = append(r.Routing.Demands, pcbsolve.Demand{ID: "external", Net: "EXT", From: "U1.3", To: "M1.3", Width: 4, Layers: []int{2}})
	rep, err := pcbsolve.Solve(context.Background(), b, r)
	if err != nil || rep.Status != pcbsolve.Pass {
		t.Fatalf("external graph repair: %v status=%s reason=%s", err, rep.Status, rep.Reason)
	}
	c := rep.Candidates[0]
	if c.Metrics.MovedGroups != 1 {
		t.Fatal("test did not exercise endpoint movement")
	}
	for _, path := range c.Routes {
		if path.DemandID == "external" {
			target, _, _ := c.Board.Pad("M1.3")
			last := path.Routes[len(path.Routes)-1]
			if last.Points[len(last.Points)-1] != target.Center {
				t.Fatal("route endpoint stayed at old group position")
			}
		}
	}
	if err := pcbsolve.Check(context.Background(), b, r, c); err != nil {
		t.Fatal(err)
	}
}

func TestAutomaticFeedbackMetamorphic(t *testing.T) {
	board, req := solveBoard(), automaticRequest(t)
	req.MaxCandidates = 1
	base, err := pcbsolve.Solve(context.Background(), board, req)
	if err != nil || len(base.Candidates) == 0 {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		tr   pcbmodel.Transform
	}{
		{"translation", pcbmodel.Transform{DX: 400, DY: -250}},
		{"rotation", pcbmodel.Transform{Pivot: pcbmodel.Point{150, 110}, RotationDelta: 90}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := transformSolveBoard(board, tc.tr)
			slices.Reverse(b.Components)
			r := req
			r.Routing.Demands = append([]pcbsolve.Demand(nil), req.Routing.Demands...)
			slices.Reverse(r.Routing.Demands)
			rep, err := pcbsolve.Solve(context.Background(), b, r)
			if err != nil || rep.Status != pcbsolve.Pass {
				t.Fatalf("transformed automatic solve: %v %s", err, rep.Reason)
			}
			a, z := base.Candidates[0].Metrics, rep.Candidates[0].Metrics
			if a.MovedGroups != z.MovedGroups || a.NewVias != z.NewVias || math.Abs(a.TotalDistance-z.TotalDistance) > 1e-6 {
				t.Fatalf("changed repair facts: %+v vs %+v", a, z)
			}
			if err := pcbsolve.Check(context.Background(), b, r, rep.Candidates[0]); err != nil {
				t.Fatal(err)
			}
		})
	}
}
