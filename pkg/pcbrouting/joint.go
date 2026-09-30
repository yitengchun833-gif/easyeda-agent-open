package pcbrouting

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// Demand identifies one independently declared connection. Electrical and
// layer-specific fields stay in the host adapter; ID and Net are sufficient for
// the deterministic joint-search kernel.
type Demand struct {
	ID  string `json:"id"`
	Net string `json:"net"`
}

type LayerRoute struct {
	ID     string  `json:"id"`
	Net    string  `json:"net"`
	Layer  int     `json:"layer"`
	Width  float64 `json:"width"`
	Points []Point `json:"points"`
}

type RouteVia struct {
	ID       string  `json:"id"`
	Net      string  `json:"net"`
	At       Point   `json:"at"`
	Diameter float64 `json:"diameter"`
	Drill    float64 `json:"drill"`
	From     int     `json:"fromLayer"`
	To       int     `json:"toLayer"`
}

type JointCandidate struct {
	DemandID string       `json:"demandId"`
	Routes   []LayerRoute `json:"routes"`
	Vias     []RouteVia   `json:"vias,omitempty"`
}

// CandidateFactory must enumerate candidates against the complete set of
// already selected routes. It reports the number of internal states consumed;
// the joint solver accounts them against the shared budget. A nil candidate
// list means this state cannot satisfy the demand, not that the physical board
// is globally unroutable.
// Reasons "state-budget" and "grid-limit" mark incomplete candidate generation;
// callers must not interpret such a branch as geometric obstruction evidence.
type CandidateFactory func(ctx context.Context, demand Demand, selected []JointCandidate, remainingStates int) (candidates []JointCandidate, states int, reason string, err error)

// CandidateChecker independently validates one candidate against the selected
// routes. It must not trust metrics stored in the candidate.
type CandidateChecker func(ctx context.Context, demand Demand, candidate JointCandidate, selected []JointCandidate) error

type JointRequest struct {
	Demands   []Demand `json:"demands"`
	MaxStates int      `json:"maxStates"`
}

type JointResult struct {
	Status    string           `json:"status"`
	Reason    string           `json:"reason,omitempty"`
	Selected  []JointCandidate `json:"selected,omitempty"`
	Partial   []JointCandidate `json:"partial,omitempty"`
	States    int              `json:"states"`
	Exhausted bool             `json:"exhausted,omitempty"`
}

func (r JointRequest) Validate() error {
	if len(r.Demands) == 0 || len(r.Demands) > 1024 || r.MaxStates < 1 || r.MaxStates > MaxSearchStates {
		return fmt.Errorf("joint routing requires 1..1024 demands and 1 <= maxStates <= %d", MaxSearchStates)
	}
	seen := map[string]bool{}
	for _, d := range r.Demands {
		if strings.TrimSpace(d.ID) == "" || strings.TrimSpace(d.Net) == "" || seen[d.ID] {
			return fmt.Errorf("joint routing has empty/duplicate demand or net")
		}
		seen[d.ID] = true
	}
	return nil
}

// SolveJoint performs bounded MRV backtracking. A path selected for an earlier
// demand is visible to every later CandidateFactory call, so all returned paths
// coexist in one board state. When a later demand cannot fit, it tries the
// current demand's alternative candidates, then other MRV-ordered demands.
// Earlier routes are thereby recomputed against a different selected set,
// within the same budget. This does not enumerate all geometric paths.
func SolveJoint(ctx context.Context, req JointRequest, factory CandidateFactory, checker CandidateChecker) (JointResult, error) {
	result := JointResult{Status: Incomplete}
	if err := req.Validate(); err != nil {
		result.Reason = "invalid-request"
		return result, err
	}
	if ctx == nil || factory == nil || checker == nil {
		result.Reason = "invalid-request"
		return result, fmt.Errorf("joint routing requires context, candidate factory and checker")
	}
	visited := map[string]bool{}
	var solution []JointCandidate
	var search func([]JointCandidate, map[string]bool) (bool, error)
	search = func(selected []JointCandidate, done map[string]bool) (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if result.States >= req.MaxStates {
			result.Exhausted = true
			return false, nil
		}
		result.States++
		if len(selected) > len(result.Partial) {
			result.Partial = cloneJointCandidates(selected)
		}
		if len(done) == len(req.Demands) {
			solution = cloneJointCandidates(selected)
			return true, nil
		}
		key := jointStateKey(selected)
		if visited[key] {
			return false, nil
		}
		visited[key] = true
		type nextDemand struct {
			demand     Demand
			candidates []JointCandidate
		}
		var choices []nextDemand
		for _, demand := range req.Demands {
			if done[demand.ID] {
				continue
			}
			remaining := req.MaxStates - result.States
			if remaining <= 0 {
				result.Exhausted = true
				return false, nil
			}
			candidates, used, reason, err := factory(ctx, demand, cloneJointCandidates(selected), remaining)
			if err != nil {
				return false, err
			}
			if err := ctx.Err(); err != nil {
				return false, err
			}
			if used < 0 || used > remaining {
				return false, fmt.Errorf("candidate factory reported invalid state usage for %s", demand.ID)
			}
			result.States += used
			if reason == "state-budget" || reason == "grid-limit" {
				result.Exhausted = true
				result.Reason = reason
			}
			if result.States >= req.MaxStates && len(candidates) == 0 {
				result.Exhausted = true
				return false, nil
			}
			valid := candidates[:0]
			for _, candidate := range candidates {
				if candidate.DemandID != demand.ID {
					return false, fmt.Errorf("candidate demand %s does not match %s", candidate.DemandID, demand.ID)
				}
				if checker(ctx, demand, candidate, selected) == nil {
					valid = append(valid, candidate)
				}
			}
			candidates = valid
			if err := ctx.Err(); err != nil {
				return false, err
			}
			if len(candidates) == 0 {
				result.Reason = reason
				return false, nil
			}
			choices = append(choices, nextDemand{demand: demand, candidates: cloneJointCandidates(candidates)})
		}
		sort.SliceStable(choices, func(i, j int) bool {
			if len(choices[i].candidates) != len(choices[j].candidates) {
				return len(choices[i].candidates) < len(choices[j].candidates)
			}
			return choices[i].demand.ID < choices[j].demand.ID
		})
		for _, next := range choices {
			for _, candidate := range next.candidates {
				done[next.demand.ID] = true
				ok, err := search(append(selected, candidate), done)
				delete(done, next.demand.ID)
				if err != nil || ok {
					return ok, err
				}
				if result.Exhausted {
					return false, nil
				}
			}
		}
		return false, nil
	}
	ok, err := search(nil, map[string]bool{})
	if err != nil {
		result.Reason = "canceled-or-error"
		return result, err
	}
	if !ok {
		if result.Exhausted {
			if result.Reason != "grid-limit" && result.Reason != "state-budget" {
				result.Reason = "joint-state-budget"
			}
		} else if result.Reason == "" {
			result.Reason = "no-joint-candidate-within-bounds"
		}
		return result, nil
	}
	result.Status, result.Reason, result.Selected = Found, "", solution
	result.Partial = nil
	sort.Slice(result.Selected, func(i, j int) bool { return result.Selected[i].DemandID < result.Selected[j].DemandID })
	return result, nil
}

// CheckJoint independently replays a complete solution without invoking the
// candidate factory or search. This prevents a solver report from serving as
// its own authority.
func CheckJoint(ctx context.Context, req JointRequest, selected []JointCandidate, checker CandidateChecker) error {
	if err := req.Validate(); err != nil {
		return err
	}
	if ctx == nil || checker == nil {
		return fmt.Errorf("joint check requires context and checker")
	}
	byID := map[string]JointCandidate{}
	if len(selected) != len(req.Demands) {
		return fmt.Errorf("candidate set must exactly match declared demands")
	}
	for _, candidate := range selected {
		if _, duplicate := byID[candidate.DemandID]; duplicate {
			return fmt.Errorf("duplicate candidate for demand %s", candidate.DemandID)
		}
		byID[candidate.DemandID] = candidate
	}
	var accepted []JointCandidate
	for _, demand := range req.Demands {
		candidate, ok := byID[demand.ID]
		if !ok {
			return fmt.Errorf("missing candidate for demand %s", demand.ID)
		}
		if candidate.DemandID != demand.ID {
			return fmt.Errorf("candidate identity mismatch for demand %s", demand.ID)
		}
		if err := checker(ctx, demand, candidate, accepted); err != nil {
			return fmt.Errorf("demand %s: %w", demand.ID, err)
		}
		accepted = append(accepted, candidate)
	}
	return nil
}

func cloneJointCandidates(in []JointCandidate) []JointCandidate {
	out := make([]JointCandidate, len(in))
	for i, candidate := range in {
		out[i] = candidate
		out[i].Routes = append([]LayerRoute(nil), candidate.Routes...)
		for j := range out[i].Routes {
			out[i].Routes[j].Points = append([]Point(nil), candidate.Routes[j].Points...)
		}
		out[i].Vias = append([]RouteVia(nil), candidate.Vias...)
	}
	return out
}

func jointStateKey(selected []JointCandidate) string {
	parts := make([]string, 0, len(selected))
	for _, candidate := range selected {
		raw, _ := json.Marshal(candidate)
		parts = append(parts, string(raw))
	}
	sort.Strings(parts)
	return strings.Join(parts, "|")
}
