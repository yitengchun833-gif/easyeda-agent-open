package pcbsolve_test

import (
	"context"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcblayout"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbmodel"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbsolve"
)

type multiGroupScenario struct {
	ID     string `json:"id"`
	Groups []struct {
		ID              string   `json:"id"`
		Refs            []string `json:"refs"`
		MaxDisplacement float64  `json:"maxDisplacement"`
	} `json:"groups"`
	FixedRefs      []string `json:"fixedRefs"`
	ExternalDemand struct {
		ID    string `json:"id"`
		From  string `json:"from"`
		To    string `json:"to"`
		Layer int    `json:"layer"`
	} `json:"externalDemand"`
	DemandCount          int     `json:"demandCount"`
	MaxTotalDisplacement float64 `json:"maxTotalDisplacement"`
	MaxStates            int     `json:"maxStates"`
}

func readMultiGroupScenario(t *testing.T) (pcbmodel.Board, pcbsolve.Request, multiGroupScenario) {
	t.Helper()
	const dir = "testdata/multi-group-clearance/"
	var board pcbmodel.Board
	var request pcbsolve.Request
	var scenario multiGroupScenario
	readCaseJSON(t, dir+"board.json", &board)
	readCaseJSON(t, dir+"request.json", &request)
	readCaseJSON(t, dir+"scenario.json", &scenario)
	return board, request, scenario
}

func TestMultiGroupClearanceScenario(t *testing.T) {
	board, request, expect := readMultiGroupScenario(t)
	ctx := context.Background()
	if findings := pcblayout.Check(board, request.Layout); len(findings) != 0 {
		t.Fatalf("scenario must begin with legal relative placement: %+v", findings)
	}
	if len(request.Layout.Groups) != len(expect.Groups) {
		t.Fatal("request omitted a group from the independent ownership specification")
	}
	for i, group := range request.Layout.Groups {
		if group.ID != expect.Groups[i].ID || !slices.Equal(group.Refs, expect.Groups[i].Refs) {
			t.Fatal("request ownership differs from the independent scenario specification")
		}
		if group.TranslationSearch == nil || len(group.Offsets) != 0 {
			t.Fatal("scenario must give bounded search parameters, never answer offsets")
		}
	}

	t.Run("baseline-is-route-blocked", func(t *testing.T) {
		baseline := request
		baseline.Layout.Groups = nil
		report, err := pcbsolve.Solve(ctx, board, baseline)
		if err != nil || report.Status == pcbsolve.Pass || report.Exhausted {
			t.Fatalf("baseline must fail routing without exhausting budget: %v status=%s exhausted=%t", err, report.Status, report.Exhausted)
		}
		observed := false
		for _, attempt := range report.Attempts {
			for _, conflict := range attempt.Conflicts {
				observed = observed || conflict.Ref == "M1" && conflict.Object != "" && conflict.DemandID != "" && conflict.Segment[0] != conflict.Segment[1]
			}
		}
		if !observed {
			t.Fatal("no rejected route segment actually hit the blocker group")
		}
	})

	report, err := pcbsolve.Solve(ctx, board, request)
	if err != nil || report.Status != pcbsolve.Pass || len(report.Candidates) == 0 {
		t.Fatalf("route-driven recursive displacement failed: %v status=%s reason=%s", err, report.Status, report.Reason)
	}
	candidate := report.Candidates[0]
	if len(report.Attempts) < 2 || report.Attempts[0].Status == "found" || len(report.Attempts[0].Blockers) == 0 {
		t.Fatal("search did not start from actual routing failure feedback")
	}
	if candidate.Metrics.MovedGroups != len(expect.Groups) || candidate.Metrics.TotalDistance <= 0 || candidate.Metrics.TotalDistance > expect.MaxTotalDisplacement || candidate.Metrics.NewVias != 0 || candidate.Metrics.Rotations != 0 || report.States > expect.MaxStates {
		t.Fatalf("repair exceeded independently declared bounds: %+v states=%d", candidate.Metrics, report.States)
	}
	if len(candidate.Moves) != len(expect.Groups) || len(candidate.Routes) != expect.DemandCount || len(candidate.Reservations) != expect.DemandCount {
		t.Fatal("candidate omitted a complete group or declared connection")
	}

	for _, group := range expect.Groups {
		firstBefore, _ := board.Component(group.Refs[0])
		firstAfter, _ := candidate.Board.Component(group.Refs[0])
		dx, dy := firstAfter.Anchor[0]-firstBefore.Anchor[0], firstAfter.Anchor[1]-firstBefore.Anchor[1]
		if d := math.Hypot(dx, dy); d <= 0 || d > group.MaxDisplacement {
			t.Fatalf("group %s did not move within its independent bounds", group.ID)
		}
		for _, ref := range group.Refs {
			before, okBefore := board.Component(ref)
			after, okAfter := candidate.Board.Component(ref)
			if !okBefore || !okAfter {
				t.Fatalf("missing independently declared member %s", ref)
			}
			// Normalize by the observed first-member displacement, without using
			// production Transform/ApplyGroup or trusting Move.Refs and metrics.
			after.Anchor[0] -= dx
			after.Anchor[1] -= dy
			after.BBox.MinX -= dx
			after.BBox.MaxX -= dx
			after.BBox.MinY -= dy
			after.BBox.MaxY -= dy
			after.Pads = append([]pcbmodel.Pad(nil), after.Pads...)
			for i := range after.Pads {
				after.Pads[i].Center[0] -= dx
				after.Pads[i].Center[1] -= dy
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("group %s lost member geometry or relative placement: %s", group.ID, ref)
			}
		}
	}
	for _, ref := range expect.FixedRefs {
		before, okBefore := board.Component(ref)
		after, okAfter := candidate.Board.Component(ref)
		if !okBefore || !okAfter || !reflect.DeepEqual(before, after) {
			t.Fatalf("fixed component %s changed", ref)
		}
	}
	if !reflect.DeepEqual(board.Tracks, candidate.Board.Tracks) || !reflect.DeepEqual(board.Vias, candidate.Board.Vias) || !reflect.DeepEqual(board.Regions, candidate.Board.Regions) || !reflect.DeepEqual(board.Outline, candidate.Board.Outline) || !reflect.DeepEqual(board.Rules, candidate.Board.Rules) || !reflect.DeepEqual(board.Stackup, candidate.Board.Stackup) {
		t.Fatal("trial reservation changed board copper, mechanical boundary or rules")
	}
	mechanicalFeedback := false
	for _, finding := range report.Layout.Rejected {
		mechanicalFeedback = mechanicalFeedback || slices.Contains(finding.Objects, "M1") && slices.Contains(finding.Objects, "M2")
	}
	if !mechanicalFeedback {
		t.Fatal("no observed collision explains the dependent group's displacement")
	}
	if len(pcblayout.Check(candidate.Board, request.Layout)) != 0 {
		t.Fatal("recursive displacement retained a mechanical violation")
	}

	externalFound := false
	for _, route := range candidate.Routes {
		if route.DemandID != expect.ExternalDemand.ID {
			continue
		}
		externalFound = true
		from, _, _ := candidate.Board.Pad(expect.ExternalDemand.From)
		to, _, _ := candidate.Board.Pad(expect.ExternalDemand.To)
		oldTo, _, _ := board.Pad(expect.ExternalDemand.To)
		if to.Center == oldTo.Center || len(route.Routes) != 1 || route.Routes[0].Layer != expect.ExternalDemand.Layer {
			t.Fatal("scenario did not re-route the displaced external endpoint")
		}
		points := route.Routes[0].Points
		if len(points) < 2 || points[0] != from.Center || points[len(points)-1] != to.Center {
			t.Fatal("external path retained a stale endpoint")
		}
	}
	if !externalFound {
		t.Fatal("candidate dropped the moving group's external connection")
	}
	if err := pcbsolve.Check(ctx, board, request, candidate); err != nil {
		t.Fatalf("independent joint check: %v", err)
	}
	t.Logf("states=%d layoutStates=%d attempts=%d mechanicalRejections=%d movedGroups=%d displacement=%g routes=%d exhausted=%t searchComplete=%t", report.States, report.Layout.States, len(report.Attempts), len(report.Layout.Rejected), candidate.Metrics.MovedGroups, candidate.Metrics.TotalDistance, len(candidate.Routes), report.Exhausted, report.SearchComplete)

	t.Run("locked-dependent-group", func(t *testing.T) {
		lockedBoard, lockedRequest, _ := readMultiGroupScenario(t)
		for i := range lockedBoard.Components {
			if slices.Contains(expect.Groups[1].Refs, lockedBoard.Components[i].Ref) {
				lockedBoard.Components[i].Locked = true
			}
		}
		locked, err := pcbsolve.Solve(ctx, lockedBoard, lockedRequest)
		if err != nil || locked.Status == pcbsolve.Pass || len(locked.Candidates) != 0 || locked.Exhausted {
			t.Fatalf("fixed group must reject this bounded repair: %v status=%s exhausted=%t", err, locked.Status, locked.Exhausted)
		}
	})

	t.Run("omitted-external-demand", func(t *testing.T) {
		missingBoard, missingRequest, _ := readMultiGroupScenario(t)
		missingRequest.Routing.Demands = slices.DeleteFunc(missingRequest.Routing.Demands, func(d pcbsolve.Demand) bool {
			return d.ID == expect.ExternalDemand.ID
		})
		if _, err := pcbsolve.Solve(ctx, missingBoard, missingRequest); err == nil || !strings.Contains(err.Error(), "external connection") {
			t.Fatalf("moving a group must not silently drop its external net: %v", err)
		}
	})
}
