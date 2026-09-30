package pcbrouting_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/zhoushoujianwork/easyeda-agent/pkg/pcbrouting"
)

func jointCandidate(demand, route string, y float64) pcbrouting.JointCandidate {
	return pcbrouting.JointCandidate{DemandID: demand, Routes: []pcbrouting.LayerRoute{{ID: route, Net: demand, Layer: 1, Width: 4, Points: []pcbrouting.Point{{0, y}, {100, y}}}}}
}

func TestSolveJointBacktracksWhenFirstRouteConsumesOnlySharedChannel(t *testing.T) {
	req := pcbrouting.JointRequest{MaxStates: 100, Demands: []pcbrouting.Demand{{ID: "A", Net: "A"}, {ID: "B", Net: "B"}}}
	factory := func(_ context.Context, d pcbrouting.Demand, selected []pcbrouting.JointCandidate, _ int) ([]pcbrouting.JointCandidate, int, string, error) {
		usedNarrow := false
		for _, c := range selected {
			for _, r := range c.Routes {
				usedNarrow = usedNarrow || r.ID == "narrow"
			}
		}
		if d.ID == "A" {
			return []pcbrouting.JointCandidate{jointCandidate("A", "narrow", 50), jointCandidate("A", "wide", 80)}, 2, "", nil
		}
		if usedNarrow {
			return nil, 1, "shared channel occupied", nil
		}
		return []pcbrouting.JointCandidate{jointCandidate("B", "narrow", 50)}, 1, "", nil
	}
	checker := func(_ context.Context, _ pcbrouting.Demand, candidate pcbrouting.JointCandidate, selected []pcbrouting.JointCandidate) error {
		for _, old := range selected {
			if old.Routes[0].ID == candidate.Routes[0].ID {
				return fmt.Errorf("route collision")
			}
		}
		return nil
	}
	result, err := pcbrouting.SolveJoint(context.Background(), req, factory, checker)
	if err != nil || result.Status != pcbrouting.Found || len(result.Selected) != 2 {
		t.Fatalf("joint solve: %+v %v", result, err)
	}
	for _, candidate := range result.Selected {
		if candidate.DemandID == "A" && candidate.Routes[0].ID != "wide" {
			t.Fatalf("solver did not backtrack A: %+v", result)
		}
	}
	if err := pcbrouting.CheckJoint(context.Background(), req, result.Selected, checker); err != nil {
		t.Fatal(err)
	}
}

func TestCheckJointRejectsTamperingAndMissingDemand(t *testing.T) {
	req := pcbrouting.JointRequest{MaxStates: 10, Demands: []pcbrouting.Demand{{ID: "A", Net: "A"}, {ID: "B", Net: "B"}}}
	checker := func(_ context.Context, d pcbrouting.Demand, c pcbrouting.JointCandidate, _ []pcbrouting.JointCandidate) error {
		if len(c.Routes) != 1 || c.Routes[0].Net != d.Net {
			return fmt.Errorf("net mismatch")
		}
		return nil
	}
	if err := pcbrouting.CheckJoint(context.Background(), req, []pcbrouting.JointCandidate{jointCandidate("A", "a", 10)}, checker); err == nil {
		t.Fatal("missing demand accepted")
	}
	tampered := []pcbrouting.JointCandidate{jointCandidate("A", "a", 10), jointCandidate("B", "b", 20)}
	tampered[1].Routes[0].Net = "A"
	if err := pcbrouting.CheckJoint(context.Background(), req, tampered, checker); err == nil {
		t.Fatal("tampered net accepted")
	}
}

// A greedily consumes B's only channel. Once B is routed first, the same
// single-route factory finds A's detour against B's actual reservation.
func orderConflictFactory(onCall func(pcbrouting.Demand, []pcbrouting.JointCandidate)) pcbrouting.CandidateFactory {
	return func(ctx context.Context, d pcbrouting.Demand, selected []pcbrouting.JointCandidate, _ int) ([]pcbrouting.JointCandidate, int, string, error) {
		if onCall != nil {
			onCall(d, selected)
		}
		if d.ID == "B" && len(selected) > 0 {
			return nil, 1, "shared channel occupied", nil
		}
		if d.ID == "A" && len(selected) > 0 {
			return []pcbrouting.JointCandidate{jointCandidate("A", "detour", 80)}, 1, "", nil
		}
		return []pcbrouting.JointCandidate{jointCandidate(d.ID, "shared", 50)}, 1, "", nil
	}
}

func orderConflictChecker(_ context.Context, _ pcbrouting.Demand, c pcbrouting.JointCandidate, selected []pcbrouting.JointCandidate) error {
	for _, prior := range selected {
		if prior.Routes[0].ID == c.Routes[0].ID {
			return fmt.Errorf("shared channel occupied")
		}
	}
	return nil
}

func TestSolveJointBacktracksDemandOrder(t *testing.T) {
	req := pcbrouting.JointRequest{MaxStates: 100, Demands: []pcbrouting.Demand{{ID: "B", Net: "B"}, {ID: "A", Net: "A"}}}
	var firstChoices []string
	factory := orderConflictFactory(func(_ pcbrouting.Demand, selected []pcbrouting.JointCandidate) {
		if len(selected) == 1 {
			firstChoices = append(firstChoices, selected[0].DemandID)
		}
	})
	result, err := pcbrouting.SolveJoint(context.Background(), req, factory, orderConflictChecker)
	if err != nil || result.Status != pcbrouting.Found || len(result.Selected) != 2 {
		t.Fatalf("order backtracking failed: %+v %v", result, err)
	}
	if len(firstChoices) < 2 || firstChoices[0] != "A" || firstChoices[1] != "B" {
		t.Fatalf("MRV/ID priority must precede alternative order: %v", firstChoices)
	}
	if err := pcbrouting.CheckJoint(context.Background(), req, result.Selected, orderConflictChecker); err != nil {
		t.Fatal(err)
	}
}

func TestSolveJointOrderBacktrackingHonorsBudgetAndCancellation(t *testing.T) {
	req := pcbrouting.JointRequest{MaxStates: 100, Demands: []pcbrouting.Demand{{ID: "A", Net: "A"}, {ID: "B", Net: "B"}}}
	for _, budget := range []int{1, 4, 7} {
		bounded := req
		bounded.MaxStates = budget
		retriedOrder := false
		factory := orderConflictFactory(func(_ pcbrouting.Demand, selected []pcbrouting.JointCandidate) {
			retriedOrder = retriedOrder || len(selected) == 1 && selected[0].DemandID == "B"
		})
		result, err := pcbrouting.SolveJoint(context.Background(), bounded, factory, orderConflictChecker)
		if err != nil || result.Status != pcbrouting.Incomplete || !result.Exhausted || result.States > budget || len(result.Selected) > 0 {
			t.Fatalf("order retries escaped budget %d: %+v %v", budget, result, err)
		}
		if budget == 7 && !retriedOrder {
			t.Fatal("budget case did not exercise the alternative order")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	factory := orderConflictFactory(func(_ pcbrouting.Demand, selected []pcbrouting.JointCandidate) {
		if len(selected) == 1 && selected[0].DemandID == "B" {
			cancel()
		}
	})
	result, err := pcbrouting.SolveJoint(ctx, req, factory, orderConflictChecker)
	if !errors.Is(err, context.Canceled) || result.Status == pcbrouting.Found || len(result.Selected) > 0 {
		t.Fatalf("cancellation during alternative order was lost: %+v %v", result, err)
	}
}
